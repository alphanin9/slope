//go:build libvirt

package controller

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"libvirt.org/go/libvirt"
)

type LibvirtController struct {
	dsn         string
	readyPeriod time.Duration
	connectFunc func(string) (*libvirt.Connect, error)
}

func NewLibvirtController(dsn string) (*LibvirtController, error) {
	if err := validateLibvirtDSN(dsn); err != nil {
		return nil, err
	}
	return &LibvirtController{
		dsn:         dsn,
		readyPeriod: time.Second,
		connectFunc: libvirt.NewConnect,
	}, nil
}

func validateLibvirtDSN(dsn string) error {
	u, err := url.Parse(dsn)
	if err != nil {
		return fmt.Errorf("invalid libvirt dsn: %w", err)
	}
	if u.Scheme != "qemu" || u.Host != "" || u.Path != "/system" {
		return fmt.Errorf("only qemu:///system libvirt DSN is supported")
	}
	return nil
}

func (c *LibvirtController) Close() error { return nil }

func (c *LibvirtController) withDomain(ctx context.Context, name string, fn func(*libvirt.Connect, *libvirt.Domain) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	conn, err := c.connectFunc(c.dsn)
	if err != nil {
		return fmt.Errorf("libvirt connect: %w", err)
	}
	defer conn.Close()
	dom, err := conn.LookupDomainByName(name)
	if err != nil {
		return fmt.Errorf("lookup domain %q: %w", cleanLog(name), err)
	}
	defer dom.Free()
	done := make(chan error, 1)
	go func() { done <- fn(conn, dom) }()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-done:
		return err
	}
}

func (c *LibvirtController) RevertSnapshot(ctx context.Context, domain, snapshot string) error {
	return c.withDomain(ctx, domain, func(_ *libvirt.Connect, dom *libvirt.Domain) error {
		snap, err := dom.SnapshotLookupByName(snapshot, 0)
		if err != nil {
			return fmt.Errorf("lookup snapshot %q: %w", cleanLog(snapshot), err)
		}
		defer snap.Free()
		if err := snap.RevertToSnapshot(libvirt.DOMAIN_SNAPSHOT_REVERT_FORCE); err != nil {
			return fmt.Errorf("revert snapshot: %w", err)
		}
		return nil
	})
}

func (c *LibvirtController) Start(ctx context.Context, domain string) error {
	return c.withDomain(ctx, domain, func(_ *libvirt.Connect, dom *libvirt.Domain) error {
		active, err := dom.IsActive()
		if err != nil {
			return fmt.Errorf("check active: %w", err)
		}
		if active {
			return nil
		}
		if err := dom.CreateWithFlags(0); err != nil {
			return fmt.Errorf("start domain: %w", err)
		}
		return nil
	})
}

func (c *LibvirtController) WaitReady(ctx context.Context, domain string) error {
	t := time.NewTicker(c.readyPeriod)
	defer t.Stop()
	for {
		err := c.withDomain(ctx, domain, func(_ *libvirt.Connect, dom *libvirt.Domain) error {
			active, err := dom.IsActive()
			if err != nil {
				return err
			}
			if !active {
				return ErrVMNotReady
			}
			return nil
		})
		if err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
}

func (c *LibvirtController) GuestEndpoint(ctx context.Context, domain string, port int) (string, error) {
	if port == 0 {
		port = 9000
	}
	sources := []libvirt.DomainInterfaceAddressesSource{
		libvirt.DOMAIN_INTERFACE_ADDRESSES_SRC_LEASE,
		libvirt.DOMAIN_INTERFACE_ADDRESSES_SRC_AGENT,
		libvirt.DOMAIN_INTERFACE_ADDRESSES_SRC_ARP,
	}
	var lastErr error
	for _, src := range sources {
		var endpoint string
		err := c.withDomain(ctx, domain, func(_ *libvirt.Connect, dom *libvirt.Domain) error {
			ifaces, err := dom.ListAllInterfaceAddresses(src)
			if err != nil {
				return err
			}
			for _, iface := range ifaces {
				for _, addr := range iface.Addrs {
					if addr.Type == libvirt.IP_ADDR_TYPE_IPV4 && !strings.HasPrefix(addr.Addr, "169.254.") && addr.Addr != "127.0.0.1" {
						endpoint = "http://" + addr.Addr + ":" + strconv.Itoa(port)
						return nil
					}
				}
			}
			return fmt.Errorf("no ipv4 address found")
		})
		if err == nil && endpoint != "" {
			return endpoint, nil
		}
		lastErr = err
	}
	return "", fmt.Errorf("discover guest endpoint: %w", lastErr)
}

func (c *LibvirtController) CaptureScreenshot(ctx context.Context, domain, dest string) (ScreenshotInfo, error) {
	var info ScreenshotInfo
	err := c.withDomain(ctx, domain, func(conn *libvirt.Connect, dom *libvirt.Domain) error {
		stream, err := conn.NewStream(0)
		if err != nil {
			return fmt.Errorf("new stream: %w", err)
		}
		defer stream.Free()
		mime, err := dom.Screenshot(stream, 0, 0)
		if err != nil {
			_ = stream.Abort()
			return fmt.Errorf("domain screenshot: %w", err)
		}
		f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			_ = stream.Abort()
			return err
		}
		defer f.Close()
		buf := make([]byte, 64*1024)
		for {
			n, recvErr := stream.Recv(buf)
			if n > 0 {
				if _, err := f.Write(buf[:n]); err != nil {
					_ = stream.Abort()
					return err
				}
			}
			if errors.Is(recvErr, io.EOF) || n == 0 {
				break
			}
			if recvErr != nil {
				_ = stream.Abort()
				return recvErr
			}
		}
		_ = stream.Finish()
		info.Mime = mime
		return nil
	})
	return info, err
}

func (c *LibvirtController) Stop(ctx context.Context, domain string) error {
	return c.withDomain(ctx, domain, func(_ *libvirt.Connect, dom *libvirt.Domain) error {
		active, err := dom.IsActive()
		if err != nil {
			return fmt.Errorf("check active: %w", err)
		}
		if !active {
			return nil
		}
		if err := dom.DestroyFlags(0); err != nil && !strings.Contains(err.Error(), "not running") {
			return fmt.Errorf("destroy domain: %w", err)
		}
		return nil
	})
}
