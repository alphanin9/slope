package guest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

type Status string

const (
	StatusRunning  Status = "running"
	StatusComplete Status = "completed"
	StatusFailed   Status = "failed"
)

type Runner interface {
	WaitReady(ctx context.Context, endpoint string) error
	PrepareSample(ctx context.Context, endpoint, sampleRef string) (string, error)
	Start(ctx context.Context, endpoint, sampleRef string) (string, error)
	Status(ctx context.Context, endpoint, runID string) (Status, string, error)
}

type HTTPRunner struct {
	Client *http.Client
}

func NewHTTPRunner() *HTTPRunner {
	return &HTTPRunner{Client: &http.Client{Timeout: 10 * time.Second}}
}

func (r *HTTPRunner) WaitReady(ctx context.Context, endpoint string) error {
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	var lastErr error
	for {
		reqCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, endpoint+"/health", nil)
		if err != nil {
			lastErr = err
			cancel()
		} else {
			resp, err := r.Client.Do(req)
			if err != nil {
				lastErr = err
			} else {
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
				if resp.StatusCode/100 == 2 {
					cancel()
					return nil
				}
				lastErr = fmt.Errorf("guest health returned %s", resp.Status)
			}
			cancel()
		}
		select {
		case <-ctx.Done():
			if lastErr != nil {
				return fmt.Errorf("guest agent not ready: %w", lastErr)
			}
			return ctx.Err()
		case <-tick.C:
		}
	}
}

func (r *HTTPRunner) PrepareSample(ctx context.Context, endpoint, sampleRef string) (string, error) {
	st, err := os.Stat(sampleRef)
	if err != nil || st.IsDir() {
		return sampleRef, nil
	}
	f, err := os.Open(sampleRef)
	if err != nil {
		return "", err
	}
	defer f.Close()
	guestPath := "samples/" + filepath.Base(sampleRef)
	u := endpoint + "/files?path=" + url.QueryEscape(guestPath)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u, f)
	if err != nil {
		return "", err
	}
	req.ContentLength = st.Size()
	resp, err := r.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("guest upload returned %s", resp.Status)
	}
	return guestPath, nil
}

func (r *HTTPRunner) Start(ctx context.Context, endpoint, sampleRef string) (string, error) {
	body, _ := json.Marshal(map[string]string{"sample_ref": sampleRef})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/run", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("guest start returned %s", resp.Status)
	}
	var out struct {
		RunID string `json:"run_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.RunID == "" {
		return "", fmt.Errorf("guest start missing run_id")
	}
	return out.RunID, nil
}

func (r *HTTPRunner) Status(ctx context.Context, endpoint, runID string) (Status, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/run/"+runID, nil)
	if err != nil {
		return "", "", err
	}
	resp, err := r.Client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return "", "", fmt.Errorf("guest status returned %s", resp.Status)
	}
	var out struct {
		Status Status `json:"status"`
		Error  string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", "", err
	}
	return out.Status, out.Error, nil
}
