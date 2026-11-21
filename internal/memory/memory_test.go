package memory

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"smem/internal/workspace"
)

func layoutForTest(t *testing.T) workspace.Layout {
	t.Helper()
	layout := workspace.Layout{Home: t.TempDir(), Project: t.TempDir()}
	if err := os.MkdirAll(filepath.Join(layout.Home, ".mem", ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	return layout
}

func TestScopesAndManualEdits(t *testing.T) {
	layout := layoutForTest(t)
	local, _, err := Add(layout, false, "部署用 Git，重建容器")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := Add(layout, true, "部署前确认 Git 分支"); err != nil {
		t.Fatal(err)
	}
	hits, err := Search(layout, "部署 git")
	if err != nil || len(hits) != 2 || hits[0].Scope != "项目" || hits[1].Scope != "全局" {
		t.Fatalf("%v %v", hits, err)
	}
	if err := os.WriteFile(local, []byte("部署改为构建镜像\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Add(layout, false, "部署用 Git，重建容器"); err == nil {
		t.Fatal("overwrote a manual edit")
	}
	hits, err = Search(layout, "镜像")
	if err != nil || len(hits) != 1 || hits[0].Path != local {
		t.Fatalf("%v %v", hits, err)
	}
	if err := os.Remove(local); err != nil {
		t.Fatal(err)
	}
	hits, err = Search(layout, "镜像")
	if err != nil || len(hits) != 0 {
		t.Fatalf("stale result: %v %v", hits, err)
	}
	layout.Project = t.TempDir()
	hits, err = Search(layout, "部署")
	if err != nil || len(hits) != 1 || hits[0].Scope != "全局" {
		t.Fatalf("scope leak: %v %v", hits, err)
	}
}

func TestConcurrentDuplicateWrites(t *testing.T) {
	layout := layoutForTest(t)
	var group sync.WaitGroup
	created := make(chan bool, 12)
	for range cap(created) {
		group.Go(func() {
			_, fresh, err := Add(layout, false, "一条并发记忆")
			if err != nil {
				t.Error(err)
			}
			created <- fresh
		})
	}
	group.Wait()
	close(created)
	count := 0
	for fresh := range created {
		if fresh {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("created %d copies", count)
	}
	files, err := os.ReadDir(filepath.Join(layout.Project, ".mem", "memory"))
	if err != nil || len(files) != 1 {
		t.Fatalf("%v %v", files, err)
	}
}

func TestTopicSearchIsBoundedAndReadOnly(t *testing.T) {
	layout := workspace.Layout{Home: t.TempDir(), Project: t.TempDir()}
	if hits, err := Search(layout, "缓存"); err != nil || len(hits) != 0 {
		t.Fatalf("%v %v", hits, err)
	}
	if _, err := os.Stat(filepath.Join(layout.Home, ".mem")); !os.IsNotExist(err) {
		t.Fatal("search wrote files")
	}
	store, _ := layout.Store(false)
	if err := os.MkdirAll(store.Path(), 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(store.Path(), "发布.md")
	if err := os.WriteFile(file, []byte("# 发布\n"+strings.Repeat("缓存命中"+strings.Repeat("长", 300)+"\n", 7)), 0600); err != nil {
		t.Fatal(err)
	}
	hits, err := Search(layout, "缓存 命中")
	if err != nil || len(hits) != 5 || hits[0].Line != 2 || hits[4].Line != 6 || len([]rune(hits[0].Text)) > 221 {
		t.Fatalf("%v %v", hits, err)
	}
	for _, text := range []string{" ", "bad\x00text", strings.Repeat("a", 17*1024)} {
		if _, _, err := Add(layout, false, text); err == nil {
			t.Fatalf("accepted invalid note")
		}
	}
	layout.Project = ""
	if _, _, err := Add(layout, false, "only in project"); err == nil {
		t.Fatal("silently wrote global")
	}
	if _, _, err := Add(layout, true, "uninitialized global"); err == nil {
		t.Fatal("uninitialized global accepted")
	}
}
