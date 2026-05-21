package guestagent

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type PathPolicy struct {
	WriteRoot    string
	AllowedRoots []string
}

func NewPathPolicy(writeRoot string, allowedRoots []string) (*PathPolicy, error) {
	if writeRoot == "" {
		writeRoot = "."
	}
	wr, err := filepath.Abs(writeRoot)
	if err != nil {
		return nil, err
	}
	roots := []string{wr}
	for _, r := range allowedRoots {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		abs, err := filepath.Abs(r)
		if err != nil {
			return nil, err
		}
		roots = append(roots, abs)
	}
	return &PathPolicy{WriteRoot: filepath.Clean(wr), AllowedRoots: cleanRoots(roots)}, nil
}

func (p *PathPolicy) ResolveWrite(path string) (string, error) {
	path = cleanInput(path)
	if path == "" {
		return "", fmt.Errorf("path is required")
	}
	if filepath.IsAbs(path) {
		return "", fmt.Errorf("writes must use a relative path")
	}
	full := filepath.Join(p.WriteRoot, filepath.Clean(path))
	if !withinRoot(full, p.WriteRoot) {
		return "", fmt.Errorf("write path escapes root")
	}
	return full, nil
}

func (p *PathPolicy) ResolveRead(path string) (string, error) {
	return p.resolveAllowed(path)
}

func (p *PathPolicy) ResolveExec(path string) (string, error) {
	return p.resolveAllowed(path)
}

func (p *PathPolicy) resolveAllowed(path string) (string, error) {
	path = cleanInput(path)
	if path == "" {
		return "", fmt.Errorf("path is required")
	}
	var full string
	if filepath.IsAbs(path) {
		full = filepath.Clean(path)
	} else {
		full = filepath.Join(p.WriteRoot, filepath.Clean(path))
	}
	for _, root := range p.AllowedRoots {
		if withinRoot(full, root) {
			return full, nil
		}
	}
	return "", fmt.Errorf("path is outside allowed roots")
}

func (p *PathPolicy) EnsureWriteRoot() error {
	return os.MkdirAll(p.WriteRoot, 0o755)
}

func cleanRoots(roots []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(roots))
	for _, r := range roots {
		r = filepath.Clean(r)
		key := norm(r)
		if !seen[key] {
			seen[key] = true
			out = append(out, r)
		}
	}
	return out
}

func withinRoot(path, root string) bool {
	path = filepath.Clean(path)
	root = filepath.Clean(root)
	if norm(path) == norm(root) {
		return true
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func cleanInput(v string) string {
	v = strings.TrimSpace(v)
	v = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, v)
	return v
}

func norm(v string) string {
	if runtime.GOOS == "windows" {
		return strings.ToLower(v)
	}
	return v
}
