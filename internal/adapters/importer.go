package adapters

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"smem/internal/guard"
	"smem/internal/memdir"
	"smem/internal/workspace"
)

// ImportCandidate describes one discovered native memory directory eligible for migration.
type ImportCandidate struct {
	Agent        string       `json:"agent"`
	Label        string       `json:"label"`
	Source       string       `json:"source"`
	Dir          string       `json:"dir"`
	DefaultScope memdir.Scope `json:"default_scope"`
	Count        int          `json:"count"`
	Selected     bool         `json:"selected"`
}

// ImportOptions configures native memory migration into SMem.
type ImportOptions struct {
	From   string       // claude, codex, cline, dir
	Path   string       // Optional explicit directory override
	Scope  memdir.Scope // Target scope (default project)
	DryRun bool
}

// ImportedItem describes one candidate file evaluated during import.
type ImportedItem struct {
	SourcePath string `json:"source_path"`
	TargetFile string `json:"target_file,omitempty"`
	Name       string `json:"name"`
	Type       string `json:"type"`
	Status     string `json:"status"` // "would_import", "imported", "skipped_secret", "skipped_duplicate", "skipped_empty"
	Reason     string `json:"reason,omitempty"`
}

// ImportResult summarizes a native memory import run.
type ImportResult struct {
	Source     string         `json:"source"`
	SourceDir  string         `json:"source_dir"`
	Scope      memdir.Scope   `json:"scope"`
	DryRun     bool           `json:"dry_run"`
	Items      []ImportedItem `json:"items"`
	Imported   int            `json:"imported"`
	Skipped    int            `json:"skipped"`
}

// RunImport scans native agent memory directories and migrates clean Markdown entries into SMem.
func RunImport(store *memdir.Store, opts ImportOptions) (ImportResult, error) {
	source := strings.ToLower(strings.TrimSpace(opts.From))
	if source == "" {
		return ImportResult{}, errors.New("请指定导入来源：--from claude|codex|cline|dir")
	}
	scope := opts.Scope
	if scope == "" {
		if source == "codex" {
			scope = memdir.ScopeGlobal
		} else {
			scope = memdir.ScopeProject
		}
	}

	sourceDir, err := resolveImportDir(store.Layout, source, opts.Path)
	if err != nil {
		return ImportResult{}, err
	}

	res := ImportResult{
		Source:    source,
		SourceDir: sourceDir,
		Scope:     scope,
		DryRun:    opts.DryRun,
	}

	mdFiles, err := collectMarkdownFiles(sourceDir, source)
	if err != nil {
		return ImportResult{}, err
	}

	for _, fullPath := range mdFiles {
		base := filepath.Base(fullPath)
		raw, err := os.ReadFile(fullPath)
		if err != nil {
			continue
		}
		fm, body, _ := memdir.ParseDocument(raw, base)
		body = guard.StripPrivate(body)
		if strings.TrimSpace(body) == "" {
			res.Items = append(res.Items, ImportedItem{
				SourcePath: fullPath,
				Name:       fm.Name,
				Status:     "skipped_empty",
				Reason:     "正文为空",
			})
			res.Skipped++
			continue
		}

		if err := guard.CheckNoSecrets(fm.Name + "\n" + fm.Description + "\n" + body); err != nil {
			res.Items = append(res.Items, ImportedItem{
				SourcePath: fullPath,
				Name:       fm.Name,
				Status:     "skipped_secret",
				Reason:     err.Error(),
			})
			res.Skipped++
			continue
		}

		mt := fm.Type
		if mt == "" || mt == memdir.TypeUntyped {
			if scope == memdir.ScopeGlobal {
				mt = memdir.TypeUser
			} else {
				mt = memdir.TypeProject
			}
		}
		desc := fm.Description
		if desc == "" {
			desc = fm.Name
		}

		if opts.DryRun {
			res.Items = append(res.Items, ImportedItem{
				SourcePath: fullPath,
				TargetFile: guard.SlugifyFilename(string(mt), fm.Name),
				Name:       fm.Name,
				Type:       string(mt),
				Status:     "would_import",
			})
			res.Imported++
			continue
		}

		wRes, err := store.Write(memdir.WriteRequest{
			Scope:       scope,
			Type:        mt,
			Name:        fm.Name,
			Description: desc,
			Content:     body,
		})
		if err != nil {
			res.Items = append(res.Items, ImportedItem{
				SourcePath: fullPath,
				Name:       fm.Name,
				Type:       string(mt),
				Status:     "skipped_duplicate",
				Reason:     err.Error(),
			})
			res.Skipped++
			continue
		}

		status := "imported"
		if wRes.Status == "unchanged" {
			status = "skipped_duplicate"
			res.Skipped++
		} else {
			res.Imported++
		}
		res.Items = append(res.Items, ImportedItem{
			SourcePath: fullPath,
			TargetFile: wRes.File,
			Name:       fm.Name,
			Type:       string(mt),
			Status:     status,
		})
	}

	return res, nil
}

func resolveImportDir(layout workspace.Layout, source, explicitPath string) (string, error) {
	if strings.TrimSpace(explicitPath) != "" {
		abs, err := filepath.Abs(explicitPath)
		if err != nil {
			return "", err
		}
		if !dirOrFileExists(abs) {
			return "", fmt.Errorf("指定目录不存在：%s", abs)
		}
		return abs, nil
	}

	switch source {
	case "claude":
		if !layout.InProject {
			return "", errors.New("自动查找 Claude 项目记忆需在 Git 仓库内执行，或通过 --path 手动指定目录")
		}
		for _, cand := range workspace.CandidateClaudeMemoryDirs(layout.HomeDir, layout.RepoRoot, layout.MainRepoRoot) {
			if dirOrFileExists(cand) {
				return cand, nil
			}
		}
		return "", fmt.Errorf("未找到当前项目的 Claude Code 原生记忆目录（可传 --path 手动指定）")
	case "codex":
		ch := resolveCodexHome(layout.HomeDir)
		for _, sub := range []string{"memories_v2", "memories"} {
			cand := filepath.Join(ch, sub)
			if dirOrFileExists(cand) {
				return cand, nil
			}
		}
		return "", fmt.Errorf("未在 %s 中找到 Codex 原生记忆目录（memories_v2 或 memories）", ch)
	case "cline":
		if !layout.InProject {
			return "", errors.New("导入 Cline memory-bank 需在项目仓库目录内执行，或传 --path")
		}
		cand := filepath.Join(layout.RepoRoot, "memory-bank")
		if !dirOrFileExists(cand) {
			return "", fmt.Errorf("未找到 %s 目录", cand)
		}
		return cand, nil
	case "dir":
		return "", errors.New("--from dir 必须配合 --path <目录> 使用")
	default:
		return "", fmt.Errorf("不支持的导入来源 %q（支持 claude、codex、cline、dir）", source)
	}
}

func collectMarkdownFiles(root, source string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			// Skip hidden dirs (.git, .codex-baseline, etc.) everywhere.
			if p != root && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			// Skip Codex internal skills directory directly under memories root.
			if source == "codex" && filepath.Dir(p) == filepath.Clean(root) && strings.EqualFold(d.Name(), "skills") {
				return filepath.SkipDir
			}
			// For claude/dir/cline stay flat; for codex walk nested extension/notes directories.
			if p != root && source != "codex" {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, ".") || !strings.HasSuffix(strings.ToLower(name), ".md") {
			return nil
		}
		if strings.EqualFold(name, memdir.IndexFileName) {
			return nil
		}
		if source == "codex" && isCodexSummaryFile(name) {
			return nil
		}
		files = append(files, p)
		return nil
	})
	return files, err
}

func isCodexSummaryFile(name string) bool {
	return strings.EqualFold(name, "memory_summary.md") || strings.EqualFold(name, "raw_memories.md")
}

// DiscoverImportSources scans only standard default tool memory directories and counts importable entries,
// skipping directories that already match SMem's active target directories (including smem bind targets).
func DiscoverImportSources(layout workspace.Layout) []ImportCandidate {
	excluded := make(map[string]bool)
	markExcluded := func(p string) {
		if strings.TrimSpace(p) != "" {
			excluded[cleanPathKey(p)] = true
		}
	}
	markExcluded(layout.GlobalDir)
	if layout.InProject {
		markExcluded(layout.ProjectDir())
		markExcluded(layout.ProjectLocalDir)
		markExcluded(layout.RepoSharedDir)
	}

	seen := make(map[string]bool)
	var out []ImportCandidate

	tryAdd := func(agent, label, source, dir string, scope memdir.Scope) bool {
		if strings.TrimSpace(dir) == "" || !dirOrFileExists(dir) {
			return false
		}
		key := cleanPathKey(dir)
		if excluded[key] || seen[key] {
			return false
		}
		count := countImportableMarkdown(dir, source)
		if count == 0 {
			return false
		}
		seen[key] = true
		out = append(out, ImportCandidate{
			Agent:        agent,
			Label:        label,
			Source:       source,
			Dir:          filepath.Clean(dir),
			DefaultScope: scope,
			Count:        count,
			Selected:     true,
		})
		return true
	}

	// 1. Claude Code default (~/.claude) & optional current CLAUDE_CONFIG_DIR for this project.
	if layout.InProject {
		repoVariants := collectRepoPathVariants(layout)
		defaultClaudeDir := filepath.Join(layout.HomeDir, ".claude")
		if envDir := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); envDir != "" && cleanPathKey(envDir) != cleanPathKey(defaultClaudeDir) {
			for _, p := range repoVariants {
				if tryAdd("claude", "Claude Code（CLAUDE_CONFIG_DIR）", "claude", workspace.ClaudeNativeMemoryDirIn(envDir, p), memdir.ScopeProject) {
					break
				}
			}
		}
		for _, p := range repoVariants {
			if tryAdd("claude", "Claude Code（默认目录）", "claude", workspace.ClaudeNativeMemoryDirIn(defaultClaudeDir, p), memdir.ScopeProject) {
				break
			}
		}
	}

	// 2. Codex CLI cross-project memory directory ($CODEX_HOME/memories_v2 or memories).
	codexHome := resolveCodexHome(layout.HomeDir)
	for _, sub := range []string{"memories_v2", "memories"} {
		cand := filepath.Join(codexHome, sub)
		if tryAdd("codex", "Codex CLI（跨项目记忆）", "codex", cand, memdir.ScopeGlobal) {
			break
		}
	}

	// 3. Cline / Roo Code per-repo memory-bank.
	if layout.InProject {
		tryAdd("cline", "Cline / Roo (memory-bank)", "cline", filepath.Join(layout.RepoRoot, "memory-bank"), memdir.ScopeProject)
	}

	return out
}

func collectRepoPathVariants(layout workspace.Layout) []string {
	var candidates []string
	seen := make(map[string]bool)
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" {
			return
		}
		key := cleanPathKey(p)
		if !seen[key] {
			seen[key] = true
			candidates = append(candidates, p)
		}
	}
	add(layout.RepoRoot)
	add(layout.MainRepoRoot)
	if real, err := filepath.EvalSymlinks(layout.RepoRoot); err == nil {
		add(real)
	}
	return candidates
}

func countImportableMarkdown(dir, source string) int {
	files, err := collectMarkdownFiles(dir, source)
	if err != nil {
		return 0
	}
	count := 0
	for _, fullPath := range files {
		raw, err := os.ReadFile(fullPath)
		if err != nil {
			continue
		}
		_, body, _ := memdir.ParseDocument(raw, filepath.Base(fullPath))
		if strings.TrimSpace(guard.StripPrivate(body)) != "" {
			count++
		}
	}
	return count
}

func cleanPathKey(p string) string {
	clean := filepath.Clean(p)
	if abs, err := filepath.Abs(clean); err == nil {
		clean = filepath.Clean(abs)
	}
	if runtime.GOOS == "windows" {
		return strings.ToLower(clean)
	}
	return clean
}
