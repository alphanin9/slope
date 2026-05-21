//go:build !libvirt

package controller

import (
	"context"
	"fmt"
)

type LibvirtController struct{}

func NewLibvirtController(dsn string) (*LibvirtController, error) {
	return nil, fmt.Errorf("binary was built without libvirt support; rebuild with: go build -tags libvirt ./cmd/slope-sandbox")
}

func (c *LibvirtController) RevertSnapshot(ctx context.Context, domain, snapshot string) error {
	return fmt.Errorf("libvirt support not compiled in")
}
func (c *LibvirtController) Start(ctx context.Context, domain string) error {
	return fmt.Errorf("libvirt support not compiled in")
}
func (c *LibvirtController) WaitReady(ctx context.Context, domain string) error {
	return fmt.Errorf("libvirt support not compiled in")
}
func (c *LibvirtController) GuestEndpoint(ctx context.Context, domain string, port int) (string, error) {
	return "", fmt.Errorf("libvirt support not compiled in")
}
func (c *LibvirtController) CaptureScreenshot(ctx context.Context, domain, dest string) (ScreenshotInfo, error) {
	return ScreenshotInfo{}, fmt.Errorf("libvirt support not compiled in")
}
func (c *LibvirtController) Stop(ctx context.Context, domain string) error {
	return fmt.Errorf("libvirt support not compiled in")
}
func (c *LibvirtController) Close() error { return nil }
