package guestagent

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestFilePutGetAndFetch(t *testing.T) {
	root := t.TempDir()
	policy, err := NewPathPolicy(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := httptest.NewServer(NewServer(policy, 1024).Handler())
	defer s.Close()

	req, err := http.NewRequest(http.MethodPut, s.URL+"/files?path=tools/a.bin", bytes.NewBufferString("abc"))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("put status %d", resp.StatusCode)
	}
	resp, err = http.Get(s.URL + "/files?path=tools/a.bin")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "abc" {
		t.Fatalf("unexpected get body %q", body)
	}

	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("fetched"))
	}))
	defer src.Close()
	payload, _ := json.Marshal(map[string]string{"url": src.URL, "path": "dl/file.bin"})
	resp, err = http.Post(s.URL+"/fetch", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("fetch status %d", resp.StatusCode)
	}
	got, err := os.ReadFile(filepath.Join(root, "dl", "file.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "fetched" {
		t.Fatalf("unexpected fetched file %q", got)
	}
}

func TestRunEndpointExecutesProgram(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses /bin/sh")
	}
	root := t.TempDir()
	policy, err := NewPathPolicy(root, []string{"/bin"})
	if err != nil {
		t.Fatal(err)
	}
	s := httptest.NewServer(NewServer(policy, 1024).Handler())
	defer s.Close()

	payload, _ := json.Marshal(map[string]any{
		"sample_ref":  "/bin/sh",
		"args":        []string{"-c", "echo ok"},
		"timeout_sec": 5,
	})
	resp, err := http.Post(s.URL+"/run", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("run status %d", resp.StatusCode)
	}
	var started struct {
		RunID string `json:"run_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&started); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(s.URL + "/run/" + started.RunID)
		if err != nil {
			t.Fatal(err)
		}
		var rec RunRecord
		if err := json.NewDecoder(resp.Body).Decode(&rec); err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if rec.Status == RunComplete {
			if rec.Stdout != "ok\n" {
				t.Fatalf("unexpected stdout %q", rec.Stdout)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("run did not complete")
}
