//go:build windows

package memdir

import (
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

var (
	kernel32        = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx  = kernel32.NewProc("LockFileEx")
	procUnlockFile  = kernel32.NewProc("UnlockFileEx")
)

const lockfileExclusiveLock = 0x00000002

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
	var ol syscall.Overlapped
	r1, _, callErr := procLockFileEx.Call(
		f.Fd(),
		uintptr(lockfileExclusiveLock),
		0,
		1,
		0,
		uintptr(unsafe.Pointer(&ol)),
	)
	if r1 == 0 {
		f.Close()
		return nil, callErr
	}
	return &dirLock{file: f}, nil
}

func (l *dirLock) Release() {
	if l == nil || l.file == nil {
		return
	}
	var ol syscall.Overlapped
	_, _, _ = procUnlockFile.Call(
		l.file.Fd(),
		0,
		1,
		0,
		uintptr(unsafe.Pointer(&ol)),
	)
	_ = l.file.Close()
}
