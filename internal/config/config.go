package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	HTTP                HTTPConfig       `yaml:"http"`
	StorageDir          string           `yaml:"storage_dir"`
	DatabasePath        string           `yaml:"database_path"`
	Backend             BackendConfig    `yaml:"backend"`
	Limits              LimitsConfig     `yaml:"limits"`
	Screenshots         ScreenshotConfig `yaml:"screenshots"`
	Machines            []MachineConfig  `yaml:"machines"`
	SchedulerPollMS     int              `yaml:"scheduler_poll_ms"`
	GuestPollMS         int              `yaml:"guest_poll_ms"`
	VMReadyTimeoutMS    int              `yaml:"vm_ready_timeout_ms"`
	GuestReadyTimeoutMS int              `yaml:"guest_ready_timeout_ms"`
}

type HTTPConfig struct {
	Addr string `yaml:"addr"`
}

type BackendConfig struct {
	Type string `yaml:"type"`
	DSN  string `yaml:"dsn"`
}

type LimitsConfig struct {
	MaxTimeoutSec int   `yaml:"max_timeout_sec"`
	MaxSampleSize int64 `yaml:"max_sample_size"`
}

type ScreenshotConfig struct {
	IntervalSec int  `yaml:"interval_sec"`
	Dedupe      bool `yaml:"dedupe"`
}

type MachineConfig struct {
	Name          string `yaml:"name"`
	Platform      string `yaml:"platform"`
	Snapshot      string `yaml:"snapshot"`
	GuestEndpoint string `yaml:"guest_endpoint"`
	GuestPort     int    `yaml:"guest_port"`
}

func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return nil, err
	}
	cfg.setDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) setDefaults() {
	if c.HTTP.Addr == "" {
		c.HTTP.Addr = "127.0.0.1:8080"
	}
	if c.StorageDir == "" {
		c.StorageDir = "storage"
	}
	if c.DatabasePath == "" {
		c.DatabasePath = "storage/slope.db"
	}
	if c.SchedulerPollMS == 0 {
		c.SchedulerPollMS = 1000
	}
	if c.GuestPollMS == 0 {
		c.GuestPollMS = 1000
	}
	if c.VMReadyTimeoutMS == 0 {
		c.VMReadyTimeoutMS = int((2 * time.Minute).Milliseconds())
	}
	if c.GuestReadyTimeoutMS == 0 {
		c.GuestReadyTimeoutMS = int((10 * time.Second).Milliseconds())
	}
	if c.Screenshots.IntervalSec == 0 {
		c.Screenshots.IntervalSec = 5
	}
	if c.Limits.MaxTimeoutSec == 0 {
		c.Limits.MaxTimeoutSec = 300
	}
	if c.Limits.MaxSampleSize == 0 {
		c.Limits.MaxSampleSize = 100 << 20
	}
}

func (c *Config) Validate() error {
	var errs []error
	if strings.ToLower(c.Backend.Type) != "libvirt" {
		errs = append(errs, fmt.Errorf("backend.type must be libvirt"))
	}
	if c.Backend.DSN == "" {
		errs = append(errs, fmt.Errorf("backend.dsn is required"))
	}
	if c.Limits.MaxTimeoutSec <= 0 {
		errs = append(errs, fmt.Errorf("limits.max_timeout_sec must be positive"))
	}
	if c.Screenshots.IntervalSec <= 0 {
		errs = append(errs, fmt.Errorf("screenshots.interval_sec must be positive"))
	}
	if len(c.Machines) == 0 {
		errs = append(errs, fmt.Errorf("at least one machine is required"))
	}
	seen := map[string]bool{}
	for i := range c.Machines {
		m := &c.Machines[i]
		if m.Name == "" || m.Snapshot == "" {
			errs = append(errs, fmt.Errorf("machine %q requires name and snapshot", m.Name))
		}
		if strings.ToLower(m.Platform) != "windows" {
			errs = append(errs, fmt.Errorf("machine %q platform must be windows", m.Name))
		}
		if m.GuestPort == 0 {
			m.GuestPort = 9000
		}
		if m.GuestPort < 1 || m.GuestPort > 65535 {
			errs = append(errs, fmt.Errorf("machine %q guest_port must be 1..65535", m.Name))
		}
		if seen[m.Name] {
			errs = append(errs, fmt.Errorf("duplicate machine %q", m.Name))
		}
		seen[m.Name] = true
	}
	return errors.Join(errs...)
}
