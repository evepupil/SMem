package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"smem/internal/adapters"
	"smem/internal/memdir"
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

func TestCLIBindAndUnbindWorkflow(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0755); err != nil {
		t.Fatal(err)
	}

	layout, err := workspace.Resolve(workspace.ResolveOptions{HomeDir: home, CWD: repo})
	if err != nil {
		t.Fatal(err)
	}
	run := func(l workspace.Layout, args ...string) (string, error) {
		var out, errOut bytes.Buffer
		err := Run(args, l, strings.NewReader(""), &out, &errOut)
		return out.String(), err
	}

	missingDir := filepath.Join(tmp, "custom-mem")
	if _, err := run(layout, "bind", missingDir); err == nil {
		t.Fatalf("expected bind to fail when directory does not exist without --create")
	}

	// Regular file target must also be rejected.
	fileTarget := filepath.Join(tmp, "not-a-dir.txt")
	if err := os.WriteFile(fileTarget, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := run(layout, "bind", fileTarget); err == nil {
		t.Fatalf("expected bind to fail when target path is a file")
	}

	if _, err := run(layout, "bind", "--create", missingDir); err != nil {
		t.Fatalf("bind --create failed: %v", err)
	}

	boundLayout, err := workspace.Resolve(workspace.ResolveOptions{HomeDir: home, CWD: repo})
	if err != nil {
		t.Fatal(err)
	}
	showOut, err := run(boundLayout, "bind", "--show", "--json")
	if err != nil {
		t.Fatalf("bind --show failed: %v", err)
	}
	var showRes struct {
		Bound    bool   `json:"bound"`
		BoundDir string `json:"bound_dir"`
	}
	if err := json.Unmarshal([]byte(showOut), &showRes); err != nil || !showRes.Bound || showRes.BoundDir != missingDir {
		t.Fatalf("unexpected bind --show json: err=%v out=%s", err, showOut)
	}

	if _, err := run(boundLayout, "write", "--type", "project", "--name", "自定义目录约定", "写入绑定目录。"); err != nil {
		t.Fatalf("write into bound dir failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(missingDir, "MEMORY.md")); err != nil {
		t.Fatalf("expected MEMORY.md in bound dir %s: %v", missingDir, err)
	}
	if _, err := os.Stat(filepath.Join(boundLayout.ProjectLocalDir, "MEMORY.md")); !os.IsNotExist(err) {
		t.Fatalf("did not expect MEMORY.md in default ProjectLocalDir when bound")
	}

	unbindOut, err := run(boundLayout, "unbind", "--json")
	if err != nil {
		t.Fatalf("unbind failed: %v", err)
	}
	var unbindRes struct {
		Unbound    bool   `json:"unbound"`
		ProjectDir string `json:"project_dir"`
	}
	if err := json.Unmarshal([]byte(unbindOut), &unbindRes); err != nil || !unbindRes.Unbound || unbindRes.ProjectDir != boundLayout.ProjectLocalDir {
		t.Fatalf("unexpected unbind json: err=%v out=%s", err, unbindOut)
	}
}

func TestSetupImportWizardSelectionAndFlags(t *testing.T) {
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")

	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git", "info"), 0755); err != nil {
		t.Fatal(err)
	}

	claudeDir := workspace.ClaudeNativeMemoryDir(home, repo)
	codexMem := filepath.Join(home, ".codex", "memories")
	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(codexMem, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claudeDir, "rule1.md"), []byte("Claude 项目规则。\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codexMem, "note1.md"), []byte("Codex 全局笔记。\n"), 0600); err != nil {
		t.Fatal(err)
	}

	layout, err := workspace.Resolve(workspace.ResolveOptions{HomeDir: home, CWD: repo})
	if err != nil {
		t.Fatal(err)
	}

	// 1. parseSelectionIndices supports commas (EN/ZH), ranges, and rejects bad inputs.
	idxs, err := parseSelectionIndices("1，3-4", 4)
	if err != nil || len(idxs) != 3 || idxs[0] != 0 || idxs[1] != 2 || idxs[2] != 3 {
		t.Fatalf("unexpected parseSelectionIndices result: idxs=%v err=%v", idxs, err)
	}
	if _, err := parseSelectionIndices("9", 2); err == nil {
		t.Fatalf("expected out-of-range error")
	}

	// 2. Interactive wizard: default all selected -> user toggles #2 off -> Enter imports ONLY #1 (Claude project)!
	candidates := adapters.DiscoverImportSources(layout)
	if len(candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(candidates))
	}
	store := memdir.NewStore(layout)
	var wizOut bytes.Buffer
	wizRes, err := runInteractiveImportWizard(candidates, store, strings.NewReader("2\n\n"), &wizOut)
	if err != nil {
		t.Fatalf("wizard failed: %v\n%s", err, wizOut.String())
	}
	if len(wizRes) != 1 || wizRes[0].Source != "claude" || wizRes[0].Imported != 1 {
		t.Fatalf("expected only Claude imported after toggling #2 off, got %+v\n%s", wizRes, wizOut.String())
	}
	if !strings.Contains(wizOut.String(), "smem bind") {
		t.Fatalf("expected manual bind/import hint in wizard output:\n%s", wizOut.String())
	}

	idxAfterWiz, err := store.Index("all")
	if err != nil {
		t.Fatal(err)
	}
	if idxAfterWiz.Project.TopicCount != 1 || idxAfterWiz.Global.TopicCount != 0 {
		t.Fatalf("expected 1 project entry and 0 global entries after deselecting Codex, got project=%d global=%d",
			idxAfterWiz.Project.TopicCount, idxAfterWiz.Global.TopicCount)
	}

	// 3. Setup with --import automatically imports remaining checked sources (Codex -> global).
	var out, errOut bytes.Buffer
	if err := Run([]string{"setup", "--agents", "claude,codex", "--import", "--bin", "smem"}, layout, strings.NewReader(""), &out, &errOut); err != nil {
		t.Fatalf("setup --import failed: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "导入完成") {
		t.Fatalf("expected import completion summary, got:\n%s", out.String())
	}
	idxAfterImport, err := store.Index("all")
	if err != nil {
		t.Fatal(err)
	}
	if idxAfterImport.Global.TopicCount != 1 {
		t.Fatalf("expected Codex global entry imported via --import, got %d", idxAfterImport.Global.TopicCount)
	}

	// 4. Dry-run in an empty repo shows manual bind/import tip cleanly.
	emptyRepo := filepath.Join(tmp, "empty-repo")
	emptyHome := filepath.Join(tmp, "empty-home")
	if err := os.MkdirAll(filepath.Join(emptyRepo, ".git", "info"), 0755); err != nil {
		t.Fatal(err)
	}
	emptyLayout, err := workspace.Resolve(workspace.ResolveOptions{HomeDir: emptyHome, CWD: emptyRepo})
	if err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := Run([]string{"setup", "--agents", "claude", "--dry-run", "--bin", "smem"}, emptyLayout, strings.NewReader(""), &out, &errOut); err != nil {
		t.Fatalf("setup --dry-run failed: %v", err)
	}
	if !strings.Contains(out.String(), "未在默认记忆目录中发现待导入条目") || !strings.Contains(out.String(), "smem bind") {
		t.Fatalf("expected empty discovery + manual bind hint on dry-run:\n%s", out.String())
	}
}
