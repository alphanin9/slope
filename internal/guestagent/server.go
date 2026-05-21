package guestagent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Server struct {
	Policy      *PathPolicy
	MaxFileSize int64
	Runs        *RunStore
	Client      *http.Client
}

type RunStatus string

const (
	RunRunning  RunStatus = "running"
	RunComplete RunStatus = "completed"
	RunFailed   RunStatus = "failed"
)

type RunRecord struct {
	ID         string    `json:"run_id"`
	Status     RunStatus `json:"status"`
	Path       string    `json:"path"`
	PID        int       `json:"pid,omitempty"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
	ExitCode   int       `json:"exit_code,omitempty"`
	Error      string    `json:"error,omitempty"`
	Stdout     string    `json:"stdout,omitempty"`
	Stderr     string    `json:"stderr,omitempty"`
}

type RunStore struct {
	mu   sync.RWMutex
	runs map[string]*RunRecord
}

func NewRunStore() *RunStore {
	return &RunStore{runs: map[string]*RunRecord{}}
}

func (s *RunStore) Put(r *RunRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *r
	s.runs[r.ID] = &cp
}

func (s *RunStore) Get(id string) (*RunRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.runs[id]
	if !ok {
		return nil, false
	}
	cp := *r
	return &cp, true
}

func (s *RunStore) Update(id string, fn func(*RunRecord)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r, ok := s.runs[id]; ok {
		fn(r)
	}
}

func NewServer(policy *PathPolicy, maxFileSize int64) *Server {
	if maxFileSize <= 0 {
		maxFileSize = 512 << 20
	}
	return &Server{
		Policy:      policy,
		MaxFileSize: maxFileSize,
		Runs:        NewRunStore(),
		Client:      &http.Client{Timeout: 30 * time.Minute},
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("PUT /files", s.putFile)
	mux.HandleFunc("GET /files", s.getFile)
	mux.HandleFunc("POST /fetch", s.fetchFile)
	mux.HandleFunc("POST /run", s.startRun)
	mux.HandleFunc("GET /run/{id}", s.getRun)
	return mux
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) putFile(w http.ResponseWriter, r *http.Request) {
	dest, err := s.Policy.ResolveWrite(r.URL.Query().Get("path"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if r.ContentLength > s.MaxFileSize {
		writeError(w, http.StatusRequestEntityTooLarge, "file too large")
		return
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer f.Close()
	n, err := io.Copy(f, http.MaxBytesReader(w, r.Body, s.MaxFileSize))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": dest, "bytes": n})
}

func (s *Server) getFile(w http.ResponseWriter, r *http.Request) {
	src, err := s.Policy.ResolveRead(r.URL.Query().Get("path"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	st, err := os.Stat(src)
	if err != nil || st.IsDir() {
		writeError(w, http.StatusNotFound, "file not found")
		return
	}
	if st.Size() > s.MaxFileSize {
		writeError(w, http.StatusRequestEntityTooLarge, "file too large")
		return
	}
	http.ServeFile(w, r, src)
}

func (s *Server) fetchFile(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL  string `json:"url"`
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	dest, err := s.Policy.ResolveWrite(req.Path)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Minute)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, req.URL, nil)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	resp, err := s.Client.Do(httpReq)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		writeError(w, http.StatusBadGateway, "fetch returned "+resp.Status)
		return
	}
	if resp.ContentLength > s.MaxFileSize {
		writeError(w, http.StatusRequestEntityTooLarge, "file too large")
		return
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer f.Close()
	n, err := io.Copy(f, io.LimitReader(resp.Body, s.MaxFileSize+1))
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if n > s.MaxFileSize {
		_ = os.Remove(dest)
		writeError(w, http.StatusRequestEntityTooLarge, "file too large")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": dest, "bytes": n})
}

func (s *Server) startRun(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SampleRef  string   `json:"sample_ref"`
		Path       string   `json:"path"`
		Args       []string `json:"args"`
		TimeoutSec int      `json:"timeout_sec"`
		Workdir    string   `json:"workdir"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	target := req.Path
	if target == "" {
		target = req.SampleRef
	}
	path, err := s.Policy.ResolveExec(target)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.TimeoutSec <= 0 {
		req.TimeoutSec = 120
	}
	if req.TimeoutSec > 3600 {
		writeError(w, http.StatusBadRequest, "timeout_sec exceeds limit")
		return
	}
	workdir := filepath.Dir(path)
	if req.Workdir != "" {
		workdir, err = s.Policy.ResolveRead(req.Workdir)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	id, err := newID()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "id generation failed")
		return
	}
	rec := &RunRecord{ID: id, Status: RunRunning, Path: path, StartedAt: time.Now().UTC()}
	s.Runs.Put(rec)
	go s.runProcess(id, path, req.Args, workdir, time.Duration(req.TimeoutSec)*time.Second)
	writeJSON(w, http.StatusAccepted, map[string]string{"run_id": id})
}

func (s *Server) getRun(w http.ResponseWriter, r *http.Request) {
	rec, ok := s.Runs.Get(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

func (s *Server) runProcess(id, path string, args []string, workdir string, timeout time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = workdir
	out, err := cmd.CombinedOutput()
	exitCode := 0
	status := RunComplete
	msg := ""
	if err != nil {
		status = RunFailed
		msg = err.Error()
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
	}
	if ctx.Err() != nil {
		status = RunFailed
		msg = ctx.Err().Error()
	}
	stdout := string(out)
	if len(stdout) > 64*1024 {
		stdout = stdout[:64*1024]
	}
	s.Runs.Update(id, func(r *RunRecord) {
		r.Status = status
		r.FinishedAt = time.Now().UTC()
		r.ExitCode = exitCode
		r.Error = msg
		r.Stdout = stdout
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": cleanInput(msg)})
}

func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func ParseBytes(v string, fallback int64) int64 {
	if v == "" {
		return fallback
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

func SplitRoots(v string) []string {
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ";")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func ErrString(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprint(err)
}
