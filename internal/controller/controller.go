package controller

import (
	"context"
	"errors"
	"strings"
)

var ErrVMNotReady = errors.New("vm not ready")

type VMController interface {
	RevertSnapshot(ctx context.Context, domain, snapshot string) error
	Start(ctx context.Context, domain string) error
	WaitReady(ctx context.Context, domain string) error
	GuestEndpoint(ctx context.Context, domain string, port int) (string, error)
	CaptureScreenshot(ctx context.Context, domain, dest string) (ScreenshotInfo, error)
	Stop(ctx context.Context, domain string) error
	Close() error
}

type ScreenshotInfo struct {
	Mime   string
	Width  int
	Height int
}

func cleanLog(v string) string {
	v = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, v)
	if len(v) > 128 {
		return v[:128]
	}
	return v
}
