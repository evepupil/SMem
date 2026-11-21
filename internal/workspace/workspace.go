package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Layout struct {
	Home    string
	Project string
}

func Discover(home, cwd string) (Layout, error) {
	if !filepath.IsAbs(home) || !filepath.IsAbs(cwd) {
		return Layout{}, errors.New("用户目录和工作目录必须是绝对路径")
	}
	layout := Layout{Home: filepath.Clean(home)}
	for dir := filepath.Clean(cwd); ; dir = filepath.Dir(dir) {
		entry, err := os.Lstat(filepath.Join(dir, ".git"))
		switch {
		case err == nil:
			if !entry.IsDir() && !entry.Mode().IsRegular() {
				return Layout{}, fmt.Errorf("无法识别 Git 工作区：%s", dir)
			}
			if dir != filepath.Join(layout.Home, ".mem") {
				layout.Project = dir
			}
			return layout, nil
		case !errors.Is(err, os.ErrNotExist):
			return Layout{}, err
		}
		if filepath.Dir(dir) == dir {
			return layout, nil
		}
	}
}

func (layout Layout) Store(global bool) (Target, error) {
	base := layout.Project
	if global {
		base = layout.Home
	} else if base == "" || base == layout.Home {
		return Target{}, errors.New("当前目录没有独立的项目仓库；进入项目再记，个人偏好请用 mem add --global")
	}
	return Target{base, filepath.Join(".mem", "memory")}, nil
}

type Target struct {
	Base string
	Rel  string
}

func (target Target) Path() string { return filepath.Join(target.Base, target.Rel) }

func (target Target) Check() error {
	if !filepath.IsAbs(target.Base) || !filepath.IsLocal(target.Rel) {
		return fmt.Errorf("路径超出选择的目录：%s", target.Path())
	}
	current := target.Base
	parts := append([]string{""}, strings.Split(filepath.Clean(target.Rel), string(filepath.Separator))...)
	for position, part := range parts {
		current = filepath.Join(current, part)
		entry, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if entry.Mode()&os.ModeSymlink != 0 || (!entry.IsDir() && (!entry.Mode().IsRegular() || position < len(parts)-1)) {
			return fmt.Errorf("路径包含链接或特殊文件，未跟随：%s", current)
		}
	}
	return nil
}

func At(base, rel string) Target { return Target{base, filepath.FromSlash(rel)} }
