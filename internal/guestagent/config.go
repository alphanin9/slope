package guestagent

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

type Config struct {
	Addr         string   `json:"addr"`
	Root         string   `json:"root"`
	AllowedRoots []string `json:"allowed_roots"`
	MaxBytes     int64    `json:"max_bytes"`
}

func DefaultConfig() Config {
	return Config{
		Addr:         "0.0.0.0:9000",
		Root:         `C:\slope`,
		AllowedRoots: []string{`C:\Windows\System32`},
		MaxBytes:     512 << 20,
	}
}

func LoadConfig(path string) (Config, error) {
	cfg := DefaultConfig()
	if path == "" {
		path = DefaultConfigPath()
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return cfg, err
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return cfg, err
	}
	if cfg.Addr == "" {
		cfg.Addr = "0.0.0.0:9000"
	}
	if cfg.Root == "" {
		cfg.Root = `C:\slope`
	}
	if cfg.MaxBytes <= 0 {
		cfg.MaxBytes = 512 << 20
	}
	return cfg, nil
}

func DefaultConfigPath() string {
	exe, err := os.Executable()
	if err != nil {
		return "slope.conf"
	}
	return filepath.Join(filepath.Dir(exe), "slope.conf")
}
