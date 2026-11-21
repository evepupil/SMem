package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNearestRootAndLinkedWorktree(t *testing.T) {
	home := t.TempDir()
	outer := t.TempDir()
	inner := filepath.Join(outer, "submodule")
	cwd := filepath.Join(inner, "src")
	if err := os.MkdirAll(cwd, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(outer, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inner, ".git"), []byte("gitdir: elsewhere\n"), 0600); err != nil {
		t.Fatal(err)
	}
	layout, err := Discover(home, cwd)
	if err != nil || layout.Project != inner {
		t.Fatalf("%+v %v", layout, err)
	}
}

func TestRefuseEscapesAndLinks(t *testing.T) {
	base := t.TempDir()
	other := t.TempDir()
	if err := At(base, "../elsewhere").Check(); err == nil {
		t.Fatal("accepted traversal")
	}
	if err := os.Symlink(other, filepath.Join(base, "link")); err != nil {
		t.Skipf("directory symlinks unavailable: %v", err)
	}
	if err := At(base, "link/memory").Check(); err == nil {
		t.Fatal("followed link")
	}
}
