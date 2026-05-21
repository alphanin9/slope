package artifact

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type Store struct {
	Root string
}

func New(root string) *Store {
	return &Store{Root: filepath.Clean(root)}
}

func (s *Store) InitTask(taskID string) error {
	if !safeName(taskID) {
		return fmt.Errorf("unsafe task id")
	}
	return os.MkdirAll(filepath.Join(s.Root, "tasks", taskID, "shots"), 0o755)
}

func (s *Store) TaskDir(taskID string) (string, error) {
	if !safeName(taskID) {
		return "", fmt.Errorf("unsafe task id")
	}
	return filepath.Join(s.Root, "tasks", taskID), nil
}

func (s *Store) ShotTempPath(taskID string, seq int) (string, error) {
	dir, err := s.TaskDir(taskID)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "shots", fmt.Sprintf("%04d.tmp", seq)), nil
}

func (s *Store) ShotPath(taskID string, seq int, mime string) (string, error) {
	dir, err := s.TaskDir(taskID)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "shots", fmt.Sprintf("%04d%s", seq, screenshotExt(mime))), nil
}

func screenshotExt(mime string) string {
	switch strings.ToLower(strings.TrimSpace(mime)) {
	case "image/png":
		return ".png"
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/x-portable-pixmap", "image/ppm":
		return ".ppm"
	default:
		return ".bin"
	}
}

func (s *Store) ArtifactPath(taskID, name string) (string, error) {
	dir, err := s.TaskDir(taskID)
	if err != nil {
		return "", err
	}
	clean := filepath.Clean(name)
	if clean == "." || filepath.IsAbs(clean) || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || clean == ".." {
		return "", fmt.Errorf("unsafe artifact name")
	}
	full := filepath.Join(dir, clean)
	rel, err := filepath.Rel(dir, full)
	if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", fmt.Errorf("artifact escapes task root")
	}
	return full, nil
}

func ValidateSample(path string, maxSize int64) error {
	clean := filepath.Clean(path)
	if clean == "." || strings.Contains(clean, "\x00") {
		return fmt.Errorf("invalid sample path")
	}
	st, err := os.Stat(clean)
	if err != nil {
		return err
	}
	if st.IsDir() {
		return fmt.Errorf("sample path is a directory")
	}
	if st.Size() > maxSize {
		return fmt.Errorf("sample exceeds max size")
	}
	return nil
}

func HashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func safeName(v string) bool {
	if v == "" || strings.Contains(v, "/") || strings.Contains(v, "\\") || strings.Contains(v, "..") {
		return false
	}
	return true
}

var ErrDuplicateShot = errors.New("duplicate screenshot")
