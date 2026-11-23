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
