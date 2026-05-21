package artifact

import (
	"path/filepath"
	"testing"
)

func TestShotPathNamingOrder(t *testing.T) {
	s := New(t.TempDir())
	if err := s.InitTask("task1"); err != nil {
		t.Fatal(err)
	}
	p1, err := s.ShotPath("task1", 1, "")
	if err != nil {
		t.Fatal(err)
	}
	p12, err := s.ShotPath("task1", 12, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	tmp, err := s.ShotTempPath("task1", 12)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(p1) != "0001.bin" || filepath.Base(p12) != "0012.png" || filepath.Base(tmp) != "0012.tmp" {
		t.Fatalf("unexpected screenshot names: %q %q", p1, p12)
	}
}

func TestArtifactPathRejectsTraversal(t *testing.T) {
	s := New(t.TempDir())
	if _, err := s.ArtifactPath("task1", "../secret"); err == nil {
		t.Fatal("expected traversal rejection")
	}
}
