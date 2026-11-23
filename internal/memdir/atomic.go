package memdir

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// atomicWriteFile writes data to a temporary file in the same directory, syncs it,
// and atomically renames it into place (with brief retry on Windows).
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".smem-tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		_ = os.Chmod(tmpName, perm)
	}

	var lastErr error
	attempts := 1
	if runtime.GOOS == "windows" {
		attempts = 8
	}
	for i := 0; i < attempts; i++ {
		if err := os.Rename(tmpName, path); err == nil {
			cleanup = false
			return nil
		} else {
			lastErr = err
			if runtime.GOOS == "windows" && i < attempts-1 {
				time.Sleep(time.Duration(10*(i+1)) * time.Millisecond)
				continue
			}
		}
	}
	return lastErr
}

// isRegularMarkdown checks if a directory entry is a normal user/topic .md file.
func isRegularMarkdown(entry os.DirEntry) bool {
	if entry.IsDir() {
		return false
	}
	name := entry.Name()
	if stringsHasPrefixDot(name) {
		return false
	}
	if equalFold(name, IndexFileName) {
		return false
	}
	if len(name) < 4 || !equalFold(name[len(name)-3:], ".md") {
		return false
	}
	if entry.Type()&os.ModeSymlink != 0 {
		return false
	}
	return true
}

func stringsHasPrefixDot(s string) bool {
	return len(s) > 0 && s[0] == '.'
}

func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if ca >= 'A' && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func isNotExist(err error) bool {
	return errors.Is(err, os.ErrNotExist)
}
