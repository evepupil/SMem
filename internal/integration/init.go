package integration

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"smem/internal/workspace"
)

func Init(layout workspace.Layout, opts Options, out io.Writer) error {
	docs, notices, err := documents(layout, opts)
	if err != nil {
		return err
	}
	var changes []edit
	seen := map[string]int{}
	for _, doc := range docs {
		path := doc.target.Path()
		if index, ok := seen[path]; ok {
			previous := &changes[index]
			previous.after, err = render(previous.after, doc.importOnly)
			if err != nil {
				return err
			}
			continue
		}
		change, err := prepare(doc)
		if err != nil {
			return err
		}
		seen[path] = len(changes)
		changes = append(changes, change)
	}
	global, err := layout.Store(true)
	if err != nil {
		return err
	}
	stores := []workspace.Target{global}
	if project, err := layout.Store(false); err == nil {
		stores = append(stores, project)
	}
	for _, store := range stores {
		if err := store.Check(); err != nil {
			return err
		}
		if entry, err := os.Stat(store.Path()); err == nil && !entry.IsDir() {
			return fmt.Errorf("记忆目录被同名文件占用：%s", store.Path())
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	gitTarget := workspace.At(layout.Home, ".mem/.git")
	if err := gitTarget.Check(); err != nil {
		return err
	}
	hasGit := exists(gitTarget.Path())
	if hasGit {
		probe := exec.Command("git", "-C", filepath.Dir(global.Path()), "rev-parse", "--show-toplevel")
		root, err := probe.Output()
		if err != nil || !sameDirectory(string(bytes.TrimSpace(root)), filepath.Dir(global.Path())) {
			return fmt.Errorf("已有的全局 Git 仓库无效，未覆盖：%s", gitTarget.Path())
		}
	} else if _, err := exec.LookPath("git"); err != nil {
		return errors.New("请先安装 Git，以便创建全局记忆仓库")
	}
	for _, change := range changes {
		if bytes.Equal(change.before, change.after) {
			continue
		}
		fmt.Fprintf(out, "更新说明：%s\n", change.target.Path())
	}
	for _, store := range stores {
		fmt.Fprintf(out, "记忆目录：%s\n", store.Path())
	}
	for _, notice := range notices {
		fmt.Fprintln(out, notice)
	}
	if !hasGit {
		fmt.Fprintf(out, "创建本地 Git 仓库：%s\n", filepath.Dir(global.Path()))
	}
	if opts.DryRun {
		fmt.Fprintln(out, "预览结束，未写入任何文件。")
		printGeneralUsage(out)
		return nil
	}
	for _, store := range stores {
		if err := os.MkdirAll(store.Path(), 0700); err != nil {
			return err
		}
	}
	if !hasGit {
		cmd := exec.Command("git", "init", "-q", "--template=", "--", filepath.Dir(global.Path()))
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("创建记忆仓库：%w：%s", err, output)
		}
	}
	for _, change := range changes {
		if bytes.Equal(change.before, change.after) {
			continue
		}
		if err := change.write(); err != nil {
			return err
		}
	}
	if len(stores) == 1 {
		fmt.Fprintln(out, "当前没有独立的 Git 项目，只接入全局记忆；需要项目说明时，请进入仓库再运行 mem init。")
	}
	fmt.Fprintln(out, "已就绪。新开 Agent 会话使用；迁移旧记忆请运行 mem migrate。")
	printGeneralUsage(out)
	return nil
}

func printGeneralUsage(out io.Writer) {
	fmt.Fprintln(out, "可复制的通用用法：")
	fmt.Fprintln(out, rule)
}

func sameDirectory(left, right string) bool {
	infoLeft, errLeft := os.Stat(left)
	infoRight, errRight := os.Stat(right)
	return errLeft == nil && errRight == nil && os.SameFile(infoLeft, infoRight)
}
