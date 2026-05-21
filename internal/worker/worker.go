package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"io"
	"log/slog"
	"os"
	"time"

	"slope/internal/artifact"
	"slope/internal/controller"
	"slope/internal/guest"
	"slope/internal/model"
	"slope/internal/store"
)

type Worker struct {
	Store              *store.Store
	Artifacts          *artifact.Store
	VM                 controller.VMController
	Guest              guest.Runner
	Log                *slog.Logger
	PollInterval       time.Duration
	GuestPollInterval  time.Duration
	ScreenshotInterval time.Duration
	VMReadyTimeout     time.Duration
	GuestReadyTimeout  time.Duration
	DedupeScreenshots  bool
}

func (w *Worker) Run(ctx context.Context) {
	tick := time.NewTicker(w.PollInterval)
	defer tick.Stop()
	for {
		if err := w.once(ctx); err != nil && !errors.Is(err, context.Canceled) {
			w.Log.Error("worker iteration failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (w *Worker) Once(ctx context.Context) error { return w.once(ctx) }

func (w *Worker) once(ctx context.Context) error {
	task, machine, err := w.Store.ClaimNextTask(ctx)
	if err != nil || task == nil {
		return err
	}
	w.Log.Info("task claimed", "task_id", task.ID, "vm", machine.Name)
	w.runTask(ctx, task, machine)
	return nil
}

func (w *Worker) runTask(parent context.Context, task *model.Task, machine *model.Machine) {
	status := model.StatusCompleted
	message := ""
	var lastHash string
	finalAttempted := false
	defer func() {
		tearCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if !finalAttempted {
			finalAttempted = true
			if err := w.capture(tearCtx, task.ID, machine.Name, model.ShotFinal, &lastHash); err != nil {
				w.event(tearCtx, task.ID, "warn", "final screenshot failed: "+err.Error())
			}
		}
		if err := w.VM.Stop(tearCtx, machine.Name); err != nil {
			w.event(tearCtx, task.ID, "error", "vm stop failed: "+err.Error())
			if status == model.StatusCompleted {
				status = model.StatusFailed
				message = err.Error()
			}
		}
		if err := w.Store.ReleaseMachine(tearCtx, machine.Name); err != nil {
			w.Log.Error("release machine failed", "task_id", task.ID, "machine", machine.Name, "error", err)
		}
		if err := w.Store.FinishTask(tearCtx, task.ID, status, message); err != nil {
			w.Log.Error("finish task failed", "task_id", task.ID, "error", err)
		}
		_ = w.writeMeta(tearCtx, task.ID)
		w.Log.Info("task finished", "task_id", task.ID, "status", status, "error", message)
	}()

	if err := w.Artifacts.InitTask(task.ID); err != nil {
		status, message = model.StatusFailed, err.Error()
		return
	}
	runLog, _ := w.openRunLog(task.ID)
	if runLog != nil {
		defer runLog.Close()
	}
	logLine(runLog, "starting task")

	runCtx, cancel := context.WithTimeout(parent, time.Duration(task.TimeoutSec)*time.Second)
	defer cancel()

	if err := w.VM.RevertSnapshot(runCtx, machine.Name, machine.Snapshot); err != nil {
		status, message = classifyErr(runCtx, err)
		return
	}
	if err := w.VM.Start(runCtx, machine.Name); err != nil {
		status, message = classifyErr(runCtx, err)
		return
	}
	readyCtx, readyCancel := context.WithTimeout(runCtx, w.VMReadyTimeout)
	if err := w.VM.WaitReady(readyCtx, machine.Name); err != nil {
		readyCancel()
		status, message = classifyErr(runCtx, err)
		return
	}
	readyCancel()
	endpoint := machine.GuestEndpoint
	if endpoint == "" {
		var err error
		endpoint, err = w.VM.GuestEndpoint(runCtx, machine.Name, machine.GuestPort)
		if err != nil {
			status, message = classifyErr(runCtx, err)
			return
		}
		logLine(runLog, "discovered guest endpoint: "+endpoint)
	}
	guestReadyCtx, guestReadyCancel := context.WithTimeout(runCtx, w.GuestReadyTimeout)
	if err := w.Guest.WaitReady(guestReadyCtx, endpoint); err != nil {
		guestReadyCancel()
		status, message = classifyErr(runCtx, err)
		return
	}
	guestReadyCancel()
	logLine(runLog, "guest agent ready: "+endpoint)
	guestSample, err := w.Guest.PrepareSample(runCtx, endpoint, task.SampleRef)
	if err != nil {
		status, message = classifyErr(runCtx, err)
		return
	}
	runID, err := w.Guest.Start(runCtx, endpoint, guestSample)
	if err != nil {
		status, message = classifyErr(runCtx, err)
		return
	}
	logLine(runLog, "guest run id: "+runID)

	ticker := time.NewTicker(w.ScreenshotInterval)
	defer ticker.Stop()
	guestTick := time.NewTicker(w.GuestPollInterval)
	defer guestTick.Stop()

	for {
		select {
		case <-runCtx.Done():
			status, message = model.StatusCompleted, ""
			w.event(context.Background(), task.ID, "info", "run window reached timeout")
			return
		case <-ticker.C:
			if err := w.capture(runCtx, task.ID, machine.Name, model.ShotPeriodic, &lastHash); err != nil {
				w.event(runCtx, task.ID, "warn", "periodic screenshot failed: "+err.Error())
			}
		case <-guestTick.C:
			gs, gmsg, err := w.Guest.Status(runCtx, endpoint, runID)
			if err != nil {
				if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
					status, message = model.StatusCompleted, ""
					w.event(context.Background(), task.ID, "info", "run window reached timeout")
					return
				}
				status, message = classifyErr(runCtx, err)
				return
			}
			switch gs {
			case guest.StatusComplete:
				return
			case guest.StatusFailed:
				status = model.StatusFailed
				message = gmsg
				return
			}
		}
	}
}

func (w *Worker) capture(ctx context.Context, taskID, vm string, kind model.ScreenshotKind, lastHash *string) error {
	seq, err := w.Store.NextScreenshotSeq(ctx, taskID)
	if err != nil {
		return err
	}
	tmpPath, err := w.Artifacts.ShotTempPath(taskID, seq)
	if err != nil {
		return err
	}
	info, err := w.VM.CaptureScreenshot(ctx, vm, tmpPath)
	if err != nil {
		return err
	}
	mime := info.Mime
	if mime == "" {
		mime = imageMime(tmpPath)
	}
	path, err := w.Artifacts.ShotPath(taskID, seq, mime)
	if err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	hash, _ := hashFile(path)
	if kind == model.ShotPeriodic && w.DedupeScreenshots && hash != "" && *lastHash == hash {
		_ = os.Remove(path)
		return nil
	}
	if hash != "" {
		*lastHash = hash
	}
	width, height := imageSize(path)
	if info.Width != 0 {
		width = info.Width
	}
	if info.Height != 0 {
		height = info.Height
	}
	return w.Store.AddScreenshot(ctx, model.Screenshot{
		TaskID:     taskID,
		Seq:        seq,
		Kind:       kind,
		Path:       path,
		CapturedAt: time.Now().UTC(),
		Width:      width,
		Height:     height,
	})
}

func (w *Worker) writeMeta(ctx context.Context, taskID string) error {
	task, err := w.Store.GetTask(ctx, taskID)
	if err != nil {
		return err
	}
	shots, err := w.Store.ListScreenshots(ctx, taskID)
	if err != nil {
		return err
	}
	dir, err := w.Artifacts.TaskDir(taskID)
	if err != nil {
		return err
	}
	f, err := os.Create(dir + "/meta.json")
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(map[string]any{"task": task, "screenshots": shots})
}

func (w *Worker) openRunLog(taskID string) (*os.File, error) {
	dir, err := w.Artifacts.TaskDir(taskID)
	if err != nil {
		return nil, err
	}
	return os.OpenFile(dir+"/run.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
}

func (w *Worker) event(ctx context.Context, taskID, level, msg string) {
	_ = w.Store.AddEvent(ctx, taskID, level, msg)
	w.Log.Log(ctx, slog.LevelWarn, msg, "task_id", taskID, "level", level)
}

func classifyErr(ctx context.Context, err error) (model.TaskStatus, string) {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return model.StatusTimeout, ctx.Err().Error()
	}
	return model.StatusFailed, err.Error()
}

func imageSize(path string) (int, int) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return 0, 0
	}
	return cfg.Width, cfg.Height
}

func imageMime(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	_, format, err := image.DecodeConfig(f)
	if err != nil {
		return ""
	}
	switch format {
	case "png":
		return "image/png"
	case "jpeg":
		return "image/jpeg"
	case "gif":
		return "image/gif"
	default:
		return ""
	}
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func logLine(w io.Writer, msg string) {
	if w != nil {
		fmt.Fprintf(w, "%s %s\n", time.Now().UTC().Format(time.RFC3339Nano), msg)
	}
}
