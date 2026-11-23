package adapters

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"smem/internal/guard"
	"smem/internal/memdir"
	"smem/internal/workspace"
)

// ShareItem represents the sync state of a single topic file between local project memory
// (~/.smem/projects/<repo-id>/) and repo-tracked shared memory (<repo>/.smem/memory/).
type ShareItem struct {
	File   string `json:"file"`
	Status string `json:"status"` // "local_only", "repo_only", "modified_local_newer", "modified_repo_newer", "synced"
}

// ShareStatusResult summarizes differences between local project dir and repo shared dir.
type ShareStatusResult struct {
	LocalDir   string      `json:"local_dir"`
	RepoDir    string      `json:"repo_dir"`
	SharedMode bool        `json:"shared_mode"`
	Items      []ShareItem `json:"items"`
}

// ShareOpResult summarizes a push or pull operation.
type ShareOpResult struct {
	Direction string   `json:"direction"` // "push" or "pull"
	DryRun    bool     `json:"dry_run"`
	Copied    []string `json:"copied"`
	Skipped   []string `json:"skipped,omitempty"`
}

// ShareStatus compares local project memory against <repo>/.smem/memory/.
func ShareStatus(layout workspace.Layout) (ShareStatusResult, error) {
	if !layout.InProject {
		return ShareStatusResult{}, errors.New("当前目录不在 Git 仓库中")
	}
	localDir := layout.ProjectDir()
	res := ShareStatusResult{
		LocalDir:   localDir,
		RepoDir:    layout.RepoSharedDir,
		SharedMode: layout.SharedMode,
	}
	if layout.SharedMode {
		return res, nil
	}

	localFiles := listTopicFiles(localDir)
	repoFiles := listTopicFiles(layout.RepoSharedDir)

	all := make(map[string]bool)
	for f := range localFiles {
		all[f] = true
	}
	for f := range repoFiles {
		all[f] = true
	}

	var names []string
	for f := range all {
		names = append(names, f)
	}
	sort.Strings(names)

	for _, f := range names {
		li, hasL := localFiles[f]
		ri, hasR := repoFiles[f]
		switch {
		case hasL && !hasR:
			res.Items = append(res.Items, ShareItem{File: f, Status: "local_only"})
		case !hasL && hasR:
			res.Items = append(res.Items, ShareItem{File: f, Status: "repo_only"})
		case bytes.Equal(li.raw, ri.raw):
			res.Items = append(res.Items, ShareItem{File: f, Status: "synced"})
		case li.modTime.After(ri.modTime):
			res.Items = append(res.Items, ShareItem{File: f, Status: "modified_local_newer"})
		default:
			res.Items = append(res.Items, ShareItem{File: f, Status: "modified_repo_newer"})
		}
	}
	return res, nil
}

// SharePush selectively copies chosen (or all) local project memories into <repo>/.smem/memory/
// for team Git collaboration, enforcing strict secret scanning before writing to the repository.
func SharePush(layout workspace.Layout, files []string, all bool, dryRun bool) (ShareOpResult, error) {
	if !layout.InProject {
		return ShareOpResult{}, errors.New("当前目录不在 Git 仓库中")
	}
	if layout.SharedMode {
		return ShareOpResult{}, errors.New("当前仓库已开启 --shared 直连模式，项目记忆本身就在 .smem/memory/ 中，无需再 push")
	}
	localFiles := listTopicFiles(layout.ProjectDir())
	targets, err := selectTargetFiles(localFiles, files, all)
	if err != nil {
		return ShareOpResult{}, err
	}

	res := ShareOpResult{Direction: "push", DryRun: dryRun}
	for _, f := range targets {
		info := localFiles[f]
		if err := guard.CheckNoSecrets(string(info.raw)); err != nil {
			return ShareOpResult{}, fmt.Errorf("拒绝同步到项目仓库 %s：%w", f, err)
		}
		dest := filepath.Join(layout.RepoSharedDir, f)
		if existing, err := os.ReadFile(dest); err == nil && bytes.Equal(existing, info.raw) {
			res.Skipped = append(res.Skipped, f+" (已同步)")
			continue
		}
		if !dryRun {
			if err := os.MkdirAll(layout.RepoSharedDir, 0755); err != nil {
				return ShareOpResult{}, err
			}
			if _, err := backupAndWriteFile("", dest, info.raw, 0644); err != nil {
				return ShareOpResult{}, err
			}
		}
		res.Copied = append(res.Copied, f)
	}

	if !dryRun && len(res.Copied) > 0 {
		sharedLayout := layout
		sharedLayout.SharedMode = true
		sharedStore := memdir.NewStore(sharedLayout)
		if _, err := sharedStore.RebuildIndex("project"); err != nil {
			return ShareOpResult{}, err
		}
	}
	return res, nil
}

// SharePull selectively imports memories from <repo>/.smem/memory/ into ~/.smem/projects/<repo-id>/.
func SharePull(layout workspace.Layout, files []string, all, force, dryRun bool) (ShareOpResult, error) {
	if !layout.InProject {
		return ShareOpResult{}, errors.New("当前目录不在 Git 仓库中")
	}
	if layout.SharedMode {
		return ShareOpResult{}, errors.New("当前仓库已开启 --shared 直连模式，无需再 pull")
	}
	repoFiles := listTopicFiles(layout.RepoSharedDir)
	if len(repoFiles) == 0 {
		return ShareOpResult{Direction: "pull", DryRun: dryRun}, nil
	}
	if len(files) == 0 {
		all = true
	}
	targets, err := selectTargetFiles(repoFiles, files, all)
	if err != nil {
		return ShareOpResult{}, err
	}

	localDir := layout.ProjectDir()
	localFiles := listTopicFiles(localDir)
	res := ShareOpResult{Direction: "pull", DryRun: dryRun}
	for _, f := range targets {
		rInfo := repoFiles[f]
		if lInfo, exists := localFiles[f]; exists {
			if bytes.Equal(lInfo.raw, rInfo.raw) {
				res.Skipped = append(res.Skipped, f+" (已同步)")
				continue
			}
			if lInfo.modTime.After(rInfo.modTime) && !force {
				res.Skipped = append(res.Skipped, f+" (本机版本更新，加 --force 覆盖)")
				continue
			}
		}
		if !dryRun {
			if err := os.MkdirAll(localDir, 0700); err != nil {
				return ShareOpResult{}, err
			}
			dest := filepath.Join(localDir, f)
			if _, err := backupAndWriteFile(layout.BackupsDir, dest, rInfo.raw, 0600); err != nil {
				return ShareOpResult{}, err
			}
		}
		res.Copied = append(res.Copied, f)
	}

	if !dryRun && len(res.Copied) > 0 {
		localStore := memdir.NewStore(layout)
		if _, err := localStore.RebuildIndex("project"); err != nil {
			return ShareOpResult{}, err
		}
	}
	return res, nil
}

// SyncGlobal initializes ~/.smem/global as a private Git repo and optionally syncs with remote.
func SyncGlobal(layout workspace.Layout, remoteURL string) (string, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return "", errors.New("未找到 git 命令，无法执行全局记忆仓库同步")
	}
	dir := layout.GlobalDir
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}

	var logLines []string
	if !dirOrFileExists(filepath.Join(dir, ".git")) {
		if out, err := runGit(dir, "init"); err != nil {
			return "", fmt.Errorf("git init 失败：%w (%s)", err, out)
		}
		// Keep .smem.lock out of global git history.
		_ = os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(".smem.lock\n"), 0644)
		logLines = append(logLines, "已初始化个人全局记忆 Git 仓库："+dir)
	}

	if strings.TrimSpace(remoteURL) != "" {
		if _, err := runGit(dir, "remote", "get-url", "origin"); err != nil {
			if out, err := runGit(dir, "remote", "add", "origin", remoteURL); err != nil {
				return "", fmt.Errorf("设置 origin 远端失败：%w (%s)", err, out)
			}
		} else {
			if out, err := runGit(dir, "remote", "set-url", "origin", remoteURL); err != nil {
				return "", fmt.Errorf("更新 origin 远端失败：%w (%s)", err, out)
			}
		}
		logLines = append(logLines, "已配置私有远端 origin："+remoteURL)
	}

	statusOut, err := runGit(dir, "status", "--porcelain")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(statusOut) != "" {
		if out, err := runGit(dir, "add", "-A"); err != nil {
			return "", fmt.Errorf("git add 失败：%w (%s)", err, out)
		}
		msg := "smem: sync global memory " + time.Now().Format("2006-01-02 15:04")
		if out, err := runGit(dir, "commit", "-m", msg); err != nil {
			return "", fmt.Errorf("git commit 失败（请确认 git user.name 与 user.email 已配置）：%w (%s)", err, out)
		}
		logLines = append(logLines, "已提交本地全局记忆变更。")
	} else {
		logLines = append(logLines, "本地全局记忆无未提交改动。")
	}

	if remote, err := runGit(dir, "remote", "get-url", "origin"); err == nil && strings.TrimSpace(remote) != "" {
		if out, err := runGit(dir, "pull", "--rebase", "origin", "HEAD"); err != nil {
			logLines = append(logLines, "拉取远端提示（若是空远端可忽略）："+strings.TrimSpace(out))
		}
		if out, err := runGit(dir, "push", "-u", "origin", "HEAD"); err != nil {
			return strings.Join(logLines, "\n"), fmt.Errorf("推送远端失败：%w (%s)", err, out)
		}
		logLines = append(logLines, "已同步至远端 origin。")
	} else {
		logLines = append(logLines, "未配置远端仓库；如需跨设备同步请运行：smem sync --remote <你的私有仓库URL>")
	}

	return strings.Join(logLines, "\n"), nil
}

func runGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

type topicFileInfo struct {
	raw     []byte
	modTime time.Time
}

func listTopicFiles(dir string) map[string]topicFileInfo {
	out := make(map[string]topicFileInfo)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, de := range entries {
		if de.IsDir() {
			continue
		}
		name := de.Name()
		if strings.HasPrefix(name, ".") || strings.EqualFold(name, memdir.IndexFileName) || !strings.HasSuffix(strings.ToLower(name), ".md") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		info, err := de.Info()
		if err != nil {
			continue
		}
		out[name] = topicFileInfo{raw: raw, modTime: info.ModTime()}
	}
	return out
}

func selectTargetFiles(available map[string]topicFileInfo, requested []string, all bool) ([]string, error) {
	if all {
		var out []string
		for f := range available {
			out = append(out, f)
		}
		sort.Strings(out)
		return out, nil
	}
	if len(requested) == 0 {
		return nil, errors.New("请指定要同步的文件名，或传入 --all 同步全部条目")
	}
	var out []string
	for _, f := range requested {
		f = strings.TrimSpace(f)
		if err := guard.ValidateMemoryFilename(f); err != nil {
			return nil, err
		}
		if _, ok := available[f]; !ok {
			return nil, fmt.Errorf("未找到文件：%s", f)
		}
		out = append(out, f)
	}
	return out, nil
}
