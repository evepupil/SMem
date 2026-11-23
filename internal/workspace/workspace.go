package workspace

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const sharedMarkerName = ".shared"

// Layout describes resolved filesystem locations for SMem's 2-scope model.
type Layout struct {
	HomeDir         string // User OS home directory (~)
	SmemHome        string // SMem home directory (SMEM_HOME or ~/.smem)
	GlobalDir       string // ~/.smem/global
	BackupsDir      string // ~/.smem/backups
	CWD             string // Current working directory
	InProject       bool   // True when inside a valid Git repository (not $HOME itself)
	RepoRoot        string // Current worktree root
	MainRepoRoot    string // Primary repository root (shared across worktrees)
	RepoID          string // Stable human-readable + hash identifier shared across worktrees
	ProjectLocalDir string // ~/.smem/projects/<repo-id> (default, zero repo pollution)
	BoundDir        string // Custom project memory dir bound via ~/.smem/config.json
	RepoSharedDir   string // <repo-root>/.smem/memory (opt-in / selective sync target)
	SharedMode      bool   // True when project directly reads/writes RepoSharedDir
}

// ProjectDir returns the active project memory directory (shared if opted in, else bound dir, else local default).
func (l Layout) ProjectDir() string {
	if !l.InProject {
		return ""
	}
	if l.SharedMode {
		return l.RepoSharedDir
	}
	if l.BoundDir != "" {
		return l.BoundDir
	}
	return l.ProjectLocalDir
}

// ResolveOptions allows tests and CLI flags to override environment paths cleanly.
type ResolveOptions struct {
	HomeDir    string
	SmemHome   string
	CWD        string
	ForceShare bool
}

// Resolve discovers the active SMem layout from the environment or explicit overrides.
func Resolve(opts ResolveOptions) (Layout, error) {
	home := opts.HomeDir
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return Layout{}, err
		}
	}
	home, err := filepath.Abs(home)
	if err != nil {
		return Layout{}, err
	}

	smemHome := opts.SmemHome
	if smemHome == "" {
		if env := strings.TrimSpace(os.Getenv("SMEM_HOME")); env != "" {
			smemHome = env
		} else {
			smemHome = filepath.Join(home, ".smem")
		}
	}
	smemHome, err = filepath.Abs(smemHome)
	if err != nil {
		return Layout{}, err
	}

	cwd := opts.CWD
	if cwd == "" {
		cwd, err = os.Getwd()
		if err != nil {
			return Layout{}, err
		}
	}
	cwd, err = filepath.Abs(cwd)
	if err != nil {
		return Layout{}, err
	}

	layout := Layout{
		HomeDir:    home,
		SmemHome:   smemHome,
		GlobalDir:  filepath.Join(smemHome, "global"),
		BackupsDir: filepath.Join(smemHome, "backups"),
		CWD:        cwd,
	}

	repoRoot, commonDir, ok := findGitRepo(cwd)
	if ok && !samePath(repoRoot, home) && !isSubpath(repoRoot, smemHome) {
		mainRoot := resolveMainRepoRoot(repoRoot, commonDir)
		repoID := computeRepoID(mainRoot, commonDir)
		repoShared := filepath.Join(repoRoot, ".smem", "memory")
		sharedMode := opts.ForceShare || hasSharedMarker(repoRoot)

		layout.InProject = true
		layout.RepoRoot = repoRoot
		layout.MainRepoRoot = mainRoot
		layout.RepoID = repoID
		layout.ProjectLocalDir = filepath.Join(smemHome, "projects", repoID)
		layout.RepoSharedDir = repoShared
		layout.SharedMode = sharedMode

		boundDir, boundOK, err := LookupProjectBinding(smemHome, repoID)
		if err != nil {
			return Layout{}, err
		}
		if boundOK {
			layout.BoundDir = boundDir
		}
	}

	return layout, nil
}

// EnableSharedMarker marks a repository to directly use <repo>/.smem/memory as its project memory dir.
func EnableSharedMarker(repoRoot string) error {
	dir := filepath.Join(repoRoot, ".smem")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	marker := filepath.Join(dir, sharedMarkerName)
	return os.WriteFile(marker, []byte("mode=shared\n"), 0644)
}

// DisableSharedMarker removes the direct shared mode marker from a repository.
func DisableSharedMarker(repoRoot string) error {
	marker := filepath.Join(repoRoot, ".smem", sharedMarkerName)
	err := os.Remove(marker)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func hasSharedMarker(repoRoot string) bool {
	marker := filepath.Join(repoRoot, ".smem", sharedMarkerName)
	info, err := os.Stat(marker)
	return err == nil && !info.IsDir()
}

// ClaudeNativeMemoryDirIn returns the Claude Code Auto Memory path inside a specific config dir.
func ClaudeNativeMemoryDirIn(configDir, repoRoot string) string {
	slug := sanitizeClaudeProjectSlug(repoRoot)
	return filepath.Join(configDir, "projects", slug, "memory")
}

// ClaudeNativeMemoryDir returns the default Claude Code Auto Memory path for a repository root.
// Claude Code sanitizes paths by replacing path separators, colons, and spaces with hyphens.
func ClaudeNativeMemoryDir(homeDir, repoRoot string) string {
	return ClaudeNativeMemoryDirIn(filepath.Join(homeDir, ".claude"), repoRoot)
}

// CandidateClaudeMemoryDirs returns possible Claude Code native memory directories for a repo.
func CandidateClaudeMemoryDirs(homeDir, repoRoot, mainRepoRoot string) []string {
	seen := make(map[string]bool)
	var out []string
	var configDirs []string
	if env := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); env != "" {
		configDirs = append(configDirs, env)
	}
	configDirs = append(configDirs, filepath.Join(homeDir, ".claude"))

	add := func(cfgDir, p string) {
		if cfgDir == "" || p == "" {
			return
		}
		dir := ClaudeNativeMemoryDirIn(cfgDir, p)
		key := normKey(dir)
		if !seen[key] {
			seen[key] = true
			out = append(out, dir)
		}
	}
	for _, cfgDir := range configDirs {
		add(cfgDir, repoRoot)
		add(cfgDir, mainRepoRoot)
		if real, err := filepath.EvalSymlinks(repoRoot); err == nil {
			add(cfgDir, real)
		}
	}
	return out
}

func sanitizeClaudeProjectSlug(p string) string {
	clean := filepath.Clean(p)
	var b strings.Builder
	for _, r := range clean {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

func findGitRepo(start string) (repoRoot, commonDir string, ok bool) {
	dir := filepath.Clean(start)
	for {
		dotGit := filepath.Join(dir, ".git")
		info, err := os.Stat(dotGit)
		if err == nil {
			if info.IsDir() {
				return dir, dotGit, true
			}
			// Worktree or submodule: .git is a file containing "gitdir: <path>"
			if gitDir, ok := parseGitDirFile(dotGit, dir); ok {
				return dir, resolveGitCommonDir(gitDir), true
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", "", false
		}
		dir = parent
	}
}

func parseGitDirFile(dotGitFile, repoRoot string) (string, bool) {
	data, err := os.ReadFile(dotGitFile)
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "gitdir:") {
			target := strings.TrimSpace(strings.TrimPrefix(line, "gitdir:"))
			if target == "" {
				return "", false
			}
			if !filepath.IsAbs(target) {
				target = filepath.Join(repoRoot, target)
			}
			return filepath.Clean(target), true
		}
	}
	return "", false
}

func resolveGitCommonDir(gitDir string) string {
	commonFile := filepath.Join(gitDir, "commondir")
	data, err := os.ReadFile(commonFile)
	if err != nil {
		return gitDir
	}
	rel := strings.TrimSpace(string(data))
	if rel == "" {
		return gitDir
	}
	if !filepath.IsAbs(rel) {
		return filepath.Clean(filepath.Join(gitDir, rel))
	}
	return filepath.Clean(rel)
}

func resolveMainRepoRoot(repoRoot, commonDir string) string {
	if filepath.Base(commonDir) == ".git" {
		return filepath.Dir(commonDir)
	}
	return repoRoot
}

func computeRepoID(mainRoot, commonDir string) string {
	baseName := sanitizeIDSegment(filepath.Base(mainRoot))
	if baseName == "" {
		baseName = "repo"
	}

	identity := ""
	if origin := readGitOriginURL(commonDir); origin != "" {
		identity = "origin:" + normalizeRemoteURL(origin)
	} else {
		canonical := mainRoot
		if resolved, err := filepath.EvalSymlinks(mainRoot); err == nil {
			canonical = resolved
		}
		if runtime.GOOS == "windows" {
			canonical = strings.ToLower(canonical)
		}
		identity = "path:" + filepath.ToSlash(filepath.Clean(canonical))
	}

	sum := sha256.Sum256([]byte(identity))
	return baseName + "-" + hex.EncodeToString(sum[:4])
}

func readGitOriginURL(commonDir string) string {
	cfgPath := filepath.Join(commonDir, "config")
	f, err := os.Open(cfgPath)
	if err != nil {
		return ""
	}
	defer f.Close()

	inOrigin := false
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section := strings.TrimSpace(line[1 : len(line)-1])
			inOrigin = strings.EqualFold(section, `remote "origin"`)
			continue
		}
		if inOrigin {
			k, v, ok := strings.Cut(line, "=")
			if ok && strings.EqualFold(strings.TrimSpace(k), "url") {
				return strings.TrimSpace(v)
			}
		}
	}
	return ""
}

// normalizeRemoteURL canonicalizes SSH and HTTPS Git URLs to host/path so the same repo
// gets the same ID across machines and clone styles.
func normalizeRemoteURL(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.TrimSuffix(s, "/")
	s = strings.TrimSuffix(s, ".git")

	// Handle scp-like syntax: git@github.com:org/repo
	if !strings.Contains(s, "://") {
		if at := strings.IndexByte(s, '@'); at >= 0 {
			s = s[at+1:]
		}
		if colon := strings.IndexByte(s, ':'); colon >= 0 {
			s = s[:colon] + "/" + s[colon+1:]
		}
		return strings.ToLower(s)
	}

	// Strip scheme and optional userinfo
	if idx := strings.Index(s, "://"); idx >= 0 {
		s = s[idx+3:]
	}
	if at := strings.IndexByte(s, '@'); at >= 0 {
		if slash := strings.IndexByte(s, '/'); slash < 0 || at < slash {
			s = s[at+1:]
		}
	}
	return strings.ToLower(s)
}

func sanitizeIDSegment(s string) string {
	var b strings.Builder
	prevDash := true
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevDash = false
		} else if !prevDash {
			b.WriteByte('-')
			prevDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func samePath(a, b string) bool {
	return normKey(a) == normKey(b)
}

func isSubpath(child, parent string) bool {
	c := normKey(child)
	p := normKey(parent)
	if c == p {
		return true
	}
	return strings.HasPrefix(c, p+string(filepath.Separator))
}

func normKey(p string) string {
	clean := filepath.Clean(p)
	if runtime.GOOS == "windows" {
		return strings.ToLower(clean)
	}
	return clean
}
