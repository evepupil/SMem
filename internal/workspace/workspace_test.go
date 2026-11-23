package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveGitRepoAndWorktreeShareRepoID(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	smemHome := filepath.Join(home, ".smem")
	mainRepo := filepath.Join(tmp, "my-app")
	subDir := filepath.Join(mainRepo, "cmd", "api")
	worktree := filepath.Join(tmp, "my-app-wt")

	if err := os.MkdirAll(filepath.Join(mainRepo, ".git", "worktrees", "wt1"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(worktree, 0755); err != nil {
		t.Fatal(err)
	}

	gitConfig := "[remote \"origin\"]\n\turl = git@github.com:Acme/My-App.git\n"
	if err := os.WriteFile(filepath.Join(mainRepo, ".git", "config"), []byte(gitConfig), 0644); err != nil {
		t.Fatal(err)
	}
	wtGitDir := filepath.Join(mainRepo, ".git", "worktrees", "wt1")
	if err := os.WriteFile(filepath.Join(wtGitDir, "commondir"), []byte("../..\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: "+wtGitDir+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	lMain, err := Resolve(ResolveOptions{HomeDir: home, SmemHome: smemHome, CWD: subDir})
	if err != nil {
		t.Fatal(err)
	}
	lWT, err := Resolve(ResolveOptions{HomeDir: home, SmemHome: smemHome, CWD: worktree})
	if err != nil {
		t.Fatal(err)
	}

	if !lMain.InProject || !lWT.InProject {
		t.Fatalf("expected both in project: main=%+v wt=%+v", lMain, lWT)
	}
	if lMain.RepoID != lWT.RepoID {
		t.Fatalf("worktree RepoID mismatch: main=%q wt=%q", lMain.RepoID, lWT.RepoID)
	}
	if lMain.ProjectDir() != lWT.ProjectDir() {
		t.Fatalf("worktree ProjectDir mismatch: main=%q wt=%q", lMain.ProjectDir(), lWT.ProjectDir())
	}
	if lMain.SharedMode {
		t.Fatalf("expected default SharedMode=false (zero repo pollution)")
	}
	if !strings.HasPrefix(lMain.ProjectDir(), filepath.Join(smemHome, "projects")) {
		t.Fatalf("expected project dir under smemHome/projects, got %q", lMain.ProjectDir())
	}
}

func TestNormalizeRemoteURLConsistency(t *testing.T) {
	a := normalizeRemoteURL("git@github.com:Org/SMem.git")
	b := normalizeRemoteURL("https://github.com/org/smem")
	c := normalizeRemoteURL("ssh://git@github.com/org/smem.git/")
	if a != b || b != c {
		t.Fatalf("remote URL normalization mismatch: %q %q %q", a, b, c)
	}
}

func TestResolveSharedModeOptIn(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := EnableSharedMarker(repo); err != nil {
		t.Fatal(err)
	}
	l, err := Resolve(ResolveOptions{HomeDir: home, CWD: repo})
	if err != nil {
		t.Fatal(err)
	}
	if !l.SharedMode {
		t.Fatalf("expected SharedMode=true after EnableSharedMarker")
	}
	if l.ProjectDir() != filepath.Join(repo, ".smem", "memory") {
		t.Fatalf("unexpected shared ProjectDir: %q", l.ProjectDir())
	}
}

func TestResolveOutsideGitAndHomeNotProject(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(filepath.Join(home, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	l, err := Resolve(ResolveOptions{HomeDir: home, CWD: home})
	if err != nil {
		t.Fatal(err)
	}
	if l.InProject {
		t.Fatalf("user home directory must not be treated as default project repo")
	}
}

func TestProjectBindingResolutionPriorityAndRefresh(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	smemHome := filepath.Join(home, ".smem")
	repo := filepath.Join(tmp, "repo")
	customDir := filepath.Join(tmp, "custom-memory")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(customDir, 0755); err != nil {
		t.Fatal(err)
	}

	baseLayout, err := Resolve(ResolveOptions{HomeDir: home, SmemHome: smemHome, CWD: repo})
	if err != nil {
		t.Fatal(err)
	}
	if baseLayout.BoundDir != "" || baseLayout.ProjectDir() != baseLayout.ProjectLocalDir {
		t.Fatalf("expected default ProjectLocalDir initially, got %+v", baseLayout)
	}

	boundPath, err := BindProject(smemHome, baseLayout.RepoID, customDir)
	if err != nil {
		t.Fatalf("BindProject failed: %v", err)
	}
	if boundPath != customDir {
		t.Fatalf("expected bound path %q, got %q", customDir, boundPath)
	}

	resolved, err := Resolve(ResolveOptions{HomeDir: home, SmemHome: smemHome, CWD: repo})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.BoundDir != customDir || resolved.ProjectDir() != customDir {
		t.Fatalf("expected BoundDir %q active, got %+v", customDir, resolved)
	}

	// RefreshBinding should pick up changes dynamically on an existing Layout.
	refreshed := RefreshBinding(baseLayout)
	if refreshed.ProjectDir() != customDir {
		t.Fatalf("expected RefreshBinding to pick up %q, got %q", customDir, refreshed.ProjectDir())
	}

	// SharedMode (.smem/.shared) takes precedence over BoundDir.
	if err := EnableSharedMarker(repo); err != nil {
		t.Fatal(err)
	}
	sharedLayout, err := Resolve(ResolveOptions{HomeDir: home, SmemHome: smemHome, CWD: repo})
	if err != nil {
		t.Fatal(err)
	}
	if !sharedLayout.SharedMode || sharedLayout.ProjectDir() != filepath.Join(repo, ".smem", "memory") {
		t.Fatalf("expected SharedMode to take precedence over BoundDir, got %+v", sharedLayout)
	}
	if err := DisableSharedMarker(repo); err != nil {
		t.Fatal(err)
	}

	existed, err := UnbindProject(smemHome, baseLayout.RepoID)
	if err != nil || !existed {
		t.Fatalf("expected UnbindProject existed=true, got existed=%v err=%v", existed, err)
	}
	afterUnbind := RefreshBinding(resolved)
	if afterUnbind.BoundDir != "" || afterUnbind.ProjectDir() != afterUnbind.ProjectLocalDir {
		t.Fatalf("expected ProjectLocalDir restored after unbind, got %+v", afterUnbind)
	}
}
