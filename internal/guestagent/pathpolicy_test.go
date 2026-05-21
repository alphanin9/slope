package guestagent

import (
	"path/filepath"
	"testing"
)

func TestPathPolicyWriteRejectsTraversal(t *testing.T) {
	p, err := NewPathPolicy(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.ResolveWrite("../x"); err == nil {
		t.Fatal("expected traversal rejection")
	}
}

func TestPathPolicyAllowsExtraReadRoot(t *testing.T) {
	root := t.TempDir()
	extra := t.TempDir()
	p, err := NewPathPolicy(root, []string{extra})
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.ResolveRead(filepath.Join(extra, "ntoskrnl.exe"))
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(extra, "ntoskrnl.exe") {
		t.Fatalf("unexpected path: %q", got)
	}
}
