//go:build libvirt

package controller

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLibvirtBootScreenshotTeardownSmoke(t *testing.T) {
	domain := os.Getenv("SLOPE_LIBVIRT_SMOKE_DOMAIN")
	snapshot := os.Getenv("SLOPE_LIBVIRT_SMOKE_SNAPSHOT")
	if domain == "" || snapshot == "" {
		t.Skip("set SLOPE_LIBVIRT_SMOKE_DOMAIN and SLOPE_LIBVIRT_SMOKE_SNAPSHOT to run")
	}
	ctrl, err := NewLibvirtController("qemu:///system")
	if err != nil {
		t.Fatal(err)
	}
	defer ctrl.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	if err := ctrl.RevertSnapshot(ctx, domain, snapshot); err != nil {
		t.Fatalf("revert snapshot: %v", err)
	}
	if err := ctrl.Start(ctx, domain); err != nil {
		t.Fatalf("start domain: %v", err)
	}
	if err := ctrl.WaitReady(ctx, domain); err != nil {
		t.Fatalf("wait ready: %v", err)
	}
	shot := filepath.Join(t.TempDir(), "smoke.png")
	if _, err := ctrl.CaptureScreenshot(ctx, domain, shot); err != nil {
		t.Fatalf("capture screenshot: %v", err)
	}
	if st, err := os.Stat(shot); err != nil {
		t.Fatalf("stat screenshot: %v", err)
	} else if st.Size() == 0 {
		t.Fatal("screenshot is empty")
	}
	if err := ctrl.Stop(ctx, domain); err != nil {
		t.Fatalf("stop domain: %v", err)
	}
}
