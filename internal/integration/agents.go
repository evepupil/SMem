package integration

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"smem/internal/workspace"
)

var names = []string{"claude", "codex", "cursor", "gemini", "copilot", "opencode", "windsurf", "devin"}

type Options struct {
	Agents     string
	DryRun     bool
	Env        func(string) string
	HasCommand func(string) bool
}

func profile(layout workspace.Layout, env func(string) string, key, fallback string) (workspace.Target, error) {
	path := env(key)
	if path == "" {
		return workspace.At(layout.Home, fallback), nil
	}
	if !filepath.IsAbs(path) {
		return workspace.Target{}, fmt.Errorf("%s 必须为绝对路径", key)
	}
	rel, err := filepath.Rel(layout.Home, path)
	if err == nil && filepath.IsLocal(rel) {
		return workspace.At(layout.Home, rel), nil
	}
	return workspace.At(filepath.Clean(path), "."), nil
}

func child(base workspace.Target, rel string) workspace.Target {
	return workspace.At(base.Base, filepath.Join(base.Rel, rel))
}

func exists(path string) bool { _, err := os.Lstat(path); return err == nil }

func documents(layout workspace.Layout, opts Options) ([]document, []string, error) {
	if opts.Env == nil {
		opts.Env = os.Getenv
	}
	if opts.HasCommand == nil {
		opts.HasCommand = func(name string) bool { _, err := exec.LookPath(name); return err == nil }
	}
	selected := map[string]bool{}
	auto := opts.Agents == "auto"
	if !auto {
		for _, name := range strings.Split(opts.Agents, ",") {
			name = strings.ToLower(strings.TrimSpace(name))
			if !slices.Contains(names, name) {
				return nil, nil, fmt.Errorf("未知工具 %q；可选：%s", name, strings.Join(names, ","))
			}
			selected[name] = true
		}
	}
	var docs []document
	var notices []string
	hasProject := layout.Project != "" && layout.Project != layout.Home
	if hasProject {
		docs = append(docs, document{workspace.At(layout.Project, "AGENTS.md"), false})
		if !exists(filepath.Join(layout.Project, "AGENTS.md")) && exists(filepath.Join(layout.Project, "CLAUDE.md")) {
			notices = append(notices, "新增项目 AGENTS.md 后，OpenCode 会优先读它；原 CLAUDE.md 仍保留。请把其他工具也需要的旧约定放入 AGENTS.md。")
		}
	}
	for _, name := range names {
		if !auto && !selected[name] {
			continue
		}
		var base workspace.Target
		var err error
		switch name {
		case "claude":
			base, err = profile(layout, opts.Env, "CLAUDE_CONFIG_DIR", ".claude")
		case "codex":
			base, err = profile(layout, opts.Env, "CODEX_HOME", ".codex")
		case "gemini":
			base = workspace.At(layout.Home, ".gemini")
		case "opencode":
			base, err = profile(layout, opts.Env, "XDG_CONFIG_HOME", ".config")
			base = child(base, "opencode")
		case "copilot":
			base = workspace.At(layout.Home, ".copilot")
		case "devin":
			base = workspace.At(layout.Home, ".config/devin")
		default:
			base = workspace.At(layout.Home, "."+name)
		}
		if err != nil {
			return nil, nil, err
		}
		if auto && !exists(base.Path()) && !opts.HasCommand(name) {
			continue
		}
		switch name {
		case "claude":
			docs = append(docs, document{child(base, "AGENTS.md"), false}, document{child(base, "CLAUDE.md"), true})
			if hasProject && (exists(filepath.Join(layout.Project, "CLAUDE.md")) || exists(filepath.Join(layout.Project, ".claude", "CLAUDE.md")) || exists(filepath.Join(layout.Project, "CLAUDE.local.md"))) {
				docs = append(docs, document{workspace.At(layout.Project, "CLAUDE.md"), true})
			}
		case "codex":
			scopes := []workspace.Target{base}
			if hasProject {
				scopes = append(scopes, workspace.At(layout.Project, "."))
			}
			for _, scope := range scopes {
				target := child(scope, "AGENTS.md")
				override := child(scope, "AGENTS.override.md")
				if info, err := os.Stat(override.Path()); err == nil && info.Size() > 0 {
					target = override
				} else if err != nil && !errors.Is(err, os.ErrNotExist) {
					return nil, nil, err
				}
				docs = append(docs, document{target, false})
			}
		case "gemini":
			docs = append(docs, document{child(base, "GEMINI.md"), false})
		case "opencode":
			target := child(base, "AGENTS.md")
			if !exists(target.Path()) && exists(filepath.Join(layout.Home, ".claude", "CLAUDE.md")) {
				inherited := workspace.At(layout.Home, ".claude/CLAUDE.md")
				notices = append(notices, "OpenCode：沿用现有 Claude 全局说明，在这份说明内补入用法；不会新建文件遮住旧规则。")
				docs = append(docs, document{inherited, false})
			} else {
				docs = append(docs, document{target, false})
			}
		case "copilot":
			if hasProject {
				docs = append(docs, document{workspace.At(layout.Project, ".github/copilot-instructions.md"), false})
			}
		case "windsurf":
			if hasProject {
				docs = append(docs, document{workspace.At(layout.Project, ".windsurf/rules/smem.md"), false})
			}
		}
	}
	return docs, notices, nil
}
