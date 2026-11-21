package memory

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"smem/internal/workspace"
)

const mutationLockName = ".mutation.lock"

func acquireMutationLock(store workspace.Target) (func() error, error) {
	mem := workspace.At(store.Base, ".mem")
	if err := mem.Check(); err != nil {
		return nil, err
	}
	if err := os.Mkdir(mem.Path(), 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	info, err := os.Lstat(mem.Path())
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, fmt.Errorf("記忆目录不是安全目录：%s", mem.Path())
	}
	if err := store.Check(); err != nil {
		return nil, err
	}

	lockPath := filepath.Join(mem.Path(), mutationLockName)
	deadline := time.Now().Add(10 * time.Second)
	for {
		err := os.Mkdir(lockPath, 0700)
		if err == nil {
			return func() error {
				lockInfo, err := os.Lstat(lockPath)
				if err != nil {
					return err
				}
				if lockInfo.Mode()&os.ModeSymlink != 0 || !lockInfo.IsDir() {
					return fmt.Errorf("记忆锁目录已被替换，未清理：%s", lockPath)
				}
				return os.Remove(lockPath)
			}, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		lockInfo, statErr := os.Lstat(lockPath)
		if statErr != nil {
			if !errors.Is(statErr, os.ErrNotExist) {
				return nil, statErr
			}
			continue
		}
		if lockInfo.Mode()&os.ModeSymlink != 0 || !lockInfo.IsDir() {
			return nil, fmt.Errorf("记忆锁路径不是普通目录：%s", lockPath)
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("记忆锁等待超时：%s；确认没有记忆操作仍在运行后，手动删除该锁目录", lockPath)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func createNewFile(path string, data []byte, mode fs.FileMode) error {
	temp, err := writeTemporaryFile(filepath.Dir(path), data, mode)
	if err != nil {
		return err
	}
	defer os.Remove(temp)
	if err := os.Link(temp, path); err != nil {
		return err
	}
	return nil
}

func replaceExistingFile(path string, data []byte, mode fs.FileMode) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("拒绝替换链接或特殊文件：%s", path)
	}
	temp, err := writeTemporaryFile(filepath.Dir(path), data, mode)
	if err != nil {
		return err
	}
	defer os.Remove(temp)
	return replaceFile(temp, path)
}

func writeTemporaryFile(dir string, data []byte, mode fs.FileMode) (string, error) {
	file, err := os.CreateTemp(dir, ".pending-*")
	if err != nil {
		return "", err
	}
	path := file.Name()
	_, writeErr := file.Write(data)
	syncErr := file.Sync()
	chmodErr := file.Chmod(mode & (os.ModePerm | os.ModeSetuid | os.ModeSetgid | os.ModeSticky))
	closeErr := file.Close()
	if err := errors.Join(writeErr, syncErr, chmodErr, closeErr); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}
