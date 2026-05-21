package worker

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"slope/internal/artifact"
	"slope/internal/config"
	"slope/internal/controller"
	"slope/internal/guest"
	"slope/internal/model"
	"slope/internal/store"
)

type mockVM struct {
	mu      sync.Mutex
	stopped bool
	shots   int
}

func (m *mockVM) RevertSnapshot(ctx context.Context, domain, snapshot string) error { return nil }
func (m *mockVM) Start(ctx context.Context, domain string) error                    { return nil }
func (m *mockVM) WaitReady(ctx context.Context, domain string) error                { return nil }
func (m *mockVM) GuestEndpoint(ctx context.Context, domain string, port int) (string, error) {
	return "http://guest", nil
}
func (m *mockVM) Stop(ctx context.Context, domain string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopped = true
	return nil
}
func (m *mockVM) Close() error { return nil }
func (m *mockVM) CaptureScreenshot(ctx context.Context, domain, dest string) (controller.ScreenshotInfo, error) {
	m.mu.Lock()
	m.shots++
	m.mu.Unlock()
	return controller.ScreenshotInfo{}, os.WriteFile(dest, tinyJPEG, 0o644)
}

type mockGuest struct {
	polls         int
	alwaysRunning bool
}

func (m *mockGuest) WaitReady(ctx context.Context, endpoint string) error {
	return nil
}

func (m *mockGuest) PrepareSample(ctx context.Context, endpoint, sampleRef string) (string, error) {
	return sampleRef, nil
}

func (m *mockGuest) Start(ctx context.Context, endpoint, sampleRef string) (string, error) {
	return "run1", nil
}

func (m *mockGuest) Status(ctx context.Context, endpoint, runID string) (guest.Status, string, error) {
	m.polls++
	if m.alwaysRunning {
		return guest.StatusRunning, "", nil
	}
	if m.polls >= 2 {
		return guest.StatusComplete, "", nil
	}
	return guest.StatusRunning, "", nil
}

func TestWorkerTimeoutWindowCompletesTask(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	db, err := store.Open(root + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.UpsertMachines(ctx, []config.MachineConfig{{Name: "win1", Platform: "windows", Snapshot: "clean", GuestEndpoint: "http://guest"}}); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateTask(ctx, model.Task{ID: "t1", SubmittedAt: time.Now().UTC(), Status: model.StatusPending, SampleRef: "abcd", VMName: "win1", TimeoutSec: 1}); err != nil {
		t.Fatal(err)
	}
	vm := &mockVM{}
	w := &Worker{
		Store:              db,
		Artifacts:          artifact.New(root),
		VM:                 vm,
		Guest:              &mockGuest{alwaysRunning: true},
		Log:                slog.New(slog.NewTextHandler(os.Stderr, nil)),
		PollInterval:       time.Millisecond,
		GuestPollInterval:  20 * time.Millisecond,
		ScreenshotInterval: 20 * time.Millisecond,
		VMReadyTimeout:     time.Second,
		GuestReadyTimeout:  time.Second,
	}
	if err := w.Once(ctx); err != nil {
		t.Fatal(err)
	}
	task, err := db.GetTask(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != model.StatusCompleted {
		t.Fatalf("expected completed timeout window, got %s: %s", task.Status, task.ErrorMessage)
	}
	if task.ErrorMessage != "" {
		t.Fatalf("timeout window should not store error, got %q", task.ErrorMessage)
	}
	m, err := db.Machine(ctx, "win1")
	if err != nil {
		t.Fatal(err)
	}
	if m.Locked {
		t.Fatal("machine lock was not released")
	}
	shots, err := db.ListScreenshots(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if len(shots) == 0 || shots[len(shots)-1].Kind != model.ShotFinal {
		t.Fatalf("expected final screenshot, got %#v", shots)
	}
}

func TestWorkerCompletesAndReleasesMachineWithFinalShot(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	db, err := store.Open(root + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.UpsertMachines(ctx, []config.MachineConfig{{Name: "win1", Platform: "windows", Snapshot: "clean", GuestEndpoint: "http://guest"}}); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateTask(ctx, model.Task{ID: "t1", SubmittedAt: time.Now().UTC(), Status: model.StatusPending, SampleRef: "abcd", VMName: "win1", TimeoutSec: 2}); err != nil {
		t.Fatal(err)
	}
	vm := &mockVM{}
	w := &Worker{
		Store:              db,
		Artifacts:          artifact.New(root),
		VM:                 vm,
		Guest:              &mockGuest{},
		Log:                slog.New(slog.NewTextHandler(os.Stderr, nil)),
		PollInterval:       time.Millisecond,
		GuestPollInterval:  20 * time.Millisecond,
		ScreenshotInterval: 10 * time.Millisecond,
		VMReadyTimeout:     time.Second,
		GuestReadyTimeout:  time.Second,
	}
	if err := w.Once(ctx); err != nil {
		t.Fatal(err)
	}
	task, err := db.GetTask(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != model.StatusCompleted {
		t.Fatalf("expected completed, got %s: %s", task.Status, task.ErrorMessage)
	}
	m, err := db.Machine(ctx, "win1")
	if err != nil {
		t.Fatal(err)
	}
	if m.Locked {
		t.Fatal("machine lock was not released")
	}
	shots, err := db.ListScreenshots(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if len(shots) == 0 || shots[len(shots)-1].Kind != model.ShotFinal {
		t.Fatalf("expected final screenshot, got %#v", shots)
	}
	if !vm.stopped {
		t.Fatal("vm stop was not called")
	}
}

var tinyJPEG = []byte{
	0xff, 0xd8, 0xff, 0xdb, 0x00, 0x43, 0x00, 0x08, 0x06, 0x06, 0x07, 0x06,
	0x05, 0x08, 0x07, 0x07, 0x07, 0x09, 0x09, 0x08, 0x0a, 0x0c, 0x14, 0x0d,
	0x0c, 0x0b, 0x0b, 0x0c, 0x19, 0x12, 0x13, 0x0f, 0x14, 0x1d, 0x1a, 0x1f,
	0x1e, 0x1d, 0x1a, 0x1c, 0x1c, 0x20, 0x24, 0x2e, 0x27, 0x20, 0x22, 0x2c,
	0x23, 0x1c, 0x1c, 0x28, 0x37, 0x29, 0x2c, 0x30, 0x31, 0x34, 0x34, 0x34,
	0x1f, 0x27, 0x39, 0x3d, 0x38, 0x32, 0x3c, 0x2e, 0x33, 0x34, 0x32, 0xff,
	0xc0, 0x00, 0x0b, 0x08, 0x00, 0x01, 0x00, 0x01, 0x01, 0x01, 0x11, 0x00,
	0xff, 0xc4, 0x00, 0x14, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x08, 0xff, 0xc4,
	0x00, 0x14, 0x10, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0xff, 0xda, 0x00, 0x08,
	0x01, 0x01, 0x00, 0x00, 0x3f, 0x00, 0x7f, 0xff, 0xd9,
}
