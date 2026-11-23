//go:build !windows

package memdir

import (
	"os"
	"path/filepath"
	"syscall"
)

type dirLock struct {
	file *os.File
}

func acquireDirLock(dir string) (*dirLock, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	lockPath := filepath.Join(dir, ".smem.lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return &dirLock{file: f}, nil
}

func (l *dirLock) Release() {
	if l == nil || l.file == nil {
		return
	}
	_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	_ = l.file.Close()
}
