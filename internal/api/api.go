package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"slope/internal/artifact"
	"slope/internal/model"
	"slope/internal/store"
)

type Server struct {
	Store         *store.Store
	Artifacts     *artifact.Store
	Log           *slog.Logger
	MaxTimeout    int
	MaxSampleSize int64
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /tasks", s.createTask)
	mux.HandleFunc("GET /tasks/{id}", s.getTask)
	mux.HandleFunc("GET /tasks/{id}/screenshots", s.getScreenshots)
	mux.HandleFunc("GET /tasks/{id}/artifacts/{name...}", s.getArtifact)
	return requestLog(s.Log, mux)
}

type createTaskRequest struct {
	SampleRef  string `json:"sample_ref"`
	VMName     string `json:"vm_name"`
	TimeoutSec int    `json:"timeout_sec"`
}

func (s *Server) createTask(w http.ResponseWriter, r *http.Request) {
	var req createTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	req.SampleRef = cleanString(req.SampleRef)
	req.VMName = cleanString(req.VMName)
	if err := s.validateCreate(r.Context(), req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	id, err := newID()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "id generation failed")
		return
	}
	task := model.Task{
		ID:          id,
		SubmittedAt: time.Now().UTC(),
		Status:      model.StatusPending,
		SampleRef:   req.SampleRef,
		VMName:      req.VMName,
		TimeoutSec:  req.TimeoutSec,
	}
	if err := s.Artifacts.InitTask(id); err != nil {
		writeError(w, http.StatusInternalServerError, "artifact init failed")
		return
	}
	if err := s.Store.CreateTask(r.Context(), task); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, task)
}

func (s *Server) validateCreate(ctx context.Context, req createTaskRequest) error {
	if req.SampleRef == "" || req.VMName == "" {
		return fmt.Errorf("sample_ref and vm_name are required")
	}
	if req.TimeoutSec <= 0 || req.TimeoutSec > s.MaxTimeout {
		return fmt.Errorf("timeout_sec must be between 1 and %d", s.MaxTimeout)
	}
	m, err := s.Store.Machine(ctx, req.VMName)
	if err != nil {
		return fmt.Errorf("unknown vm_name")
	}
	if strings.ToLower(m.Platform) != "windows" {
		return fmt.Errorf("vm profile is not windows")
	}
	if strings.Contains(req.SampleRef, "..") || strings.Contains(req.SampleRef, "\x00") {
		return fmt.Errorf("unsafe sample_ref")
	}
	if looksLikePath(req.SampleRef) {
		if err := artifact.ValidateSample(req.SampleRef, s.MaxSampleSize); err != nil {
			return fmt.Errorf("invalid sample_ref: %w", err)
		}
	}
	return nil
}

func (s *Server) getTask(w http.ResponseWriter, r *http.Request) {
	task, err := s.Store.GetTask(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (s *Server) getScreenshots(w http.ResponseWriter, r *http.Request) {
	shots, err := s.Store.ListScreenshots(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, shots)
}

func (s *Server) getArtifact(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	path, err := s.Artifacts.ArtifactPath(r.PathValue("id"), name)
	if err != nil {
		writeError(w, http.StatusBadRequest, "unsafe artifact name")
		return
	}
	st, err := os.Stat(path)
	if err != nil || st.IsDir() {
		writeError(w, http.StatusNotFound, "artifact not found")
		return
	}
	dir, _ := s.Artifacts.TaskDir(r.PathValue("id"))
	rel, err := filepath.Rel(dir, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		writeError(w, http.StatusBadRequest, "artifact escapes task root")
		return
	}
	http.ServeFile(w, r, path)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": cleanString(msg)})
}

func requestLog(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Info("http request", "method", r.Method, "path", r.URL.Path, "duration_ms", strconv.FormatInt(time.Since(start).Milliseconds(), 10))
	})
}

func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

var hashRe = regexp.MustCompile(`^[a-fA-F0-9]{32,128}$`)

func looksLikePath(v string) bool {
	return strings.ContainsAny(v, `/\`) || strings.Contains(v, ".") && !hashRe.MatchString(v)
}

func cleanString(v string) string {
	v = strings.TrimSpace(v)
	v = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, v)
	if len(v) > 512 {
		return v[:512]
	}
	return v
}

var ErrNotFound = errors.New("not found")
