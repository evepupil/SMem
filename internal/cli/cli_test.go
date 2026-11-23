package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"smem/internal/workspace"
)

func TestCLICompleteWorkflowAndJSONOutput(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git", "info"), 0755); err != nil {
		t.Fatal(err)
	}
	layout, err := workspace.Resolve(workspace.ResolveOptions{HomeDir: home, CWD: repo})
	if err != nil {
		t.Fatal(err)
	}

	run := func(args ...string) (string, error) {
		var out, errOut bytes.Buffer
		err := Run(args, layout, strings.NewReader(""), &out, &errOut)
		return out.String(), err
	}

	// 1. write --json
	out, err := run("write", "--type", "project", "--name", "容器发布规范", "--desc", "修改配置后需重建容器", "--json", "必须执行 docker compose up --build。")
	if err != nil {
		t.Fatalf("write failed: %v", err)
	}
	var wRes struct {
		File string `json:"file"`
		ETag string `json:"etag"`
	}
	if err := json.Unmarshal([]byte(out), &wRes); err != nil || wRes.File == "" || wRes.ETag == "" {
		t.Fatalf("invalid write json: %v\n%s", err, out)
	}

	// 2. read --json
	readOut, err := run("read", "--scope", "project", "--json", wRes.File)
	if err != nil || !strings.Contains(readOut, "docker compose up --build") {
		t.Fatalf("read failed: err=%v out=%s", err, readOut)
	}

	// 3. search
	searchOut, err := run("search", "容器", "build")
	if err != nil || !strings.Contains(searchOut, "容器发布规范") {
		t.Fatalf("search failed: err=%v out=%s", err, searchOut)
	}

	// 4. check
	checkOut, err := run("check")
	if err != nil || !strings.Contains(checkOut, "检查通过") {
		t.Fatalf("check failed: err=%v out=%s", err, checkOut)
	}

	// 5. share status & push --dry-run
	shareOut, err := run("share", "push", "--dry-run", wRes.File)
	if err != nil || !strings.Contains(shareOut, wRes.File) {
		t.Fatalf("share push dry-run failed: err=%v out=%s", err, shareOut)
	}

	// 6. dream prompt output
	dreamOut, err := run("dream")
	if err != nil || !strings.Contains(dreamOut, "Orient") || !strings.Contains(dreamOut, "Prune & Index") {
		t.Fatalf("dream failed: err=%v out=%s", err, dreamOut)
	}

	// 7. delete requires etag; succeeds with valid etag
	if _, err := run("delete", wRes.File); err == nil {
		t.Fatalf("expected error deleting without etag")
	}
	if _, err := run("delete", "--etag", wRes.ETag, wRes.File); err != nil {
		t.Fatalf("delete failed: %v", err)
	}
}

func TestCLIProjectWriteOutsideGitRefused(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	outside := filepath.Join(tmp, "not-a-repo")
	if err := os.MkdirAll(outside, 0755); err != nil {
		t.Fatal(err)
	}
	layout, err := workspace.Resolve(workspace.ResolveOptions{HomeDir: home, CWD: outside})
	if err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	err = Run([]string{"write", "--type", "project", "--name", "test", "hello"}, layout, strings.NewReader(""), &out, &errOut)
	if err == nil {
		t.Fatalf("expected project write outside Git repo to be rejected")
	}

	// Global write outside Git repo must succeed.
	out.Reset()
	err = Run([]string{"write", "--scope", "global", "--type", "user", "--name", "个人偏好", "偏好 Go 语言。"}, layout, strings.NewReader(""), &out, &errOut)
	if err != nil {
		t.Fatalf("global write outside Git repo failed: %v", err)
	}
}
