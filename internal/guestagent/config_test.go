package guestagent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigMissingUsesDefaults(t *testing.T) {
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "missing.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != "0.0.0.0:9000" || cfg.Root == "" || cfg.MaxBytes == 0 {
		t.Fatalf("unexpected defaults: %#v", cfg)
	}
}

func TestLoadConfigJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "slope.conf")
	if err := os.WriteFile(path, []byte(`{"addr":"127.0.0.1:1","root":"C:\\x","allowed_roots":["C:\\y"],"max_bytes":42}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != "127.0.0.1:1" || cfg.Root != `C:\x` || cfg.MaxBytes != 42 || len(cfg.AllowedRoots) != 1 {
		t.Fatalf("unexpected config: %#v", cfg)
	}
}
