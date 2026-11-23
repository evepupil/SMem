package adapters

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"smem/internal/memdir"
	"smem/internal/workspace"
)

func newAdapterTestLayout(t *testing.T) workspace.Layout {
	t.Helper()
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git", "info"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0755); err != nil {
		t.Fatal(err)
	}
	layout, err := workspace.Resolve(workspace.ResolveOptions{HomeDir: home, CWD: repo})
	if err != nil {
		t.Fatal(err)
	}
	return layout
}

func TestSetupClaudeNativeCodexIdempotentAndRemove(t *testing.T) {
	t.Setenv("CODEX_HOME", "")
	layout := newAdapterTestLayout(t)

	// Seed existing Codex config.toml with user comments to verify comment preservation.
	codexCfgPath := filepath.Join(layout.HomeDir, ".codex", "config.toml")
	seedTOML := "# My custom Codex settings\nmodel = \"o3\"\n"
	if err := os.WriteFile(codexCfgPath, []byte(seedTOML), 0600); err != nil {
		t.Fatal(err)
	}

	// 1. Dry-run writes nothing.
	dry, err := RunSetup(layout, SetupOptions{Agents: "claude,codex", ClaudeMode: "native", DryRun: true, SmemBin: "smem"})
	if err != nil {
		t.Fatalf("dry-run failed: %v", err)
	}
	if len(dry.Actions) == 0 {
		t.Fatalf("expected planned actions")
	}
	if _, err := os.Stat(filepath.Join(layout.RepoRoot, ".claude", "settings.local.json")); err == nil {
		t.Fatalf("dry-run must not create files")
	}

	// 2. Apply setup.
	res, err := RunSetup(layout, SetupOptions{Agents: "claude,codex", ClaudeMode: "native", SmemBin: "smem"})
	if err != nil {
		t.Fatalf("setup apply failed: %v", err)
	}
	if res.ClaudeMode != "native" {
		t.Fatalf("unexpected ClaudeMode: %s", res.ClaudeMode)
	}

	// Verify Claude autoMemoryDirectory points straight to SMem project dir.
	localCfg, err := os.ReadFile(filepath.Join(layout.RepoRoot, ".claude", "settings.local.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(localCfg), "autoMemoryDirectory") || !strings.Contains(string(localCfg), layout.RepoID) {
		t.Fatalf("expected autoMemoryDirectory pointing to %s, got:\n%s", layout.ProjectDir(), string(localCfg))
	}

	// Verify .git/info/exclude hides .claude/settings.local.json (zero git status pollution!).
	excludeBytes, err := os.ReadFile(filepath.Join(layout.RepoRoot, ".git", "info", "exclude"))
	if err != nil || !strings.Contains(string(excludeBytes), ".claude/settings.local.json") {
		t.Fatalf("expected .git/info/exclude entry, got: %s (err=%v)", string(excludeBytes), err)
	}

	// Verify Codex config.toml preserved user comment AND added [mcp_servers.smem].
	codexAfter, err := os.ReadFile(codexCfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(codexAfter), "# My custom Codex settings") || !strings.Contains(string(codexAfter), "[mcp_servers.smem]") {
		t.Fatalf("codex config.toml corrupted:\n%s", string(codexAfter))
	}

	// 3. Second setup run is 100% idempotent (all actions unchanged).
	res2, err := RunSetup(layout, SetupOptions{Agents: "claude,codex", ClaudeMode: "native", SmemBin: "smem"})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range res2.Actions {
		if a.Action != "unchanged" {
			t.Fatalf("expected idempotent unchanged on 2nd run, got %+v", a)
		}
	}

	// 4. Remove cleanly strips SMem entries while preserving user comments.
	if _, err := RunSetup(layout, SetupOptions{Agents: "claude,codex", ClaudeMode: "native", Remove: true, SmemBin: "smem"}); err != nil {
		t.Fatal(err)
	}
	codexRemoved, err := os.ReadFile(codexCfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(codexRemoved), "[mcp_servers.smem]") || !strings.Contains(string(codexRemoved), "# My custom Codex settings") {
		t.Fatalf("codex removal did not preserve original config cleanly:\n%s", string(codexRemoved))
	}
}

func TestImportFromClaudeNativeMemory(t *testing.T) {
	layout := newAdapterTestLayout(t)
	store := memdir.NewStore(layout)

	// Populate a simulated Claude Code native memory directory.
	claudeMemDir := workspace.ClaudeNativeMemoryDir(layout.HomeDir, layout.RepoRoot)
	if err := os.MkdirAll(claudeMemDir, 0755); err != nil {
		t.Fatal(err)
	}
	good := memdir.RenderDocument(memdir.Frontmatter{
		Name:        "测试不 Mock 数据库",
		Description: "编写集成测试时触发",
		Type:        memdir.TypeFeedback,
	}, "集成测试必须连真实 Postgres。\n\n**Why:** Mock 曾漏掉迁移 Bug。")
	bad := memdir.RenderDocument(memdir.Frontmatter{
		Name:        "意外粘贴的 Key",
		Description: "包含密钥",
		Type:        memdir.TypeReference,
	}, "token=sk-ant-api03-1234567890abcdefghijklmnop")

	if err := os.WriteFile(filepath.Join(claudeMemDir, "feedback_db.md"), good, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claudeMemDir, "ref_bad.md"), bad, 0600); err != nil {
		t.Fatal(err)
	}

	imp, err := RunImport(store, ImportOptions{From: "claude", Scope: memdir.ScopeProject})
	if err != nil {
		t.Fatalf("import failed: %v", err)
	}
	if imp.Imported != 1 || imp.Skipped != 1 {
		t.Fatalf("expected 1 imported & 1 secret skipped, got %+v", imp)
	}
}

func TestSharePushPullSelectiveRepoSync(t *testing.T) {
	layout := newAdapterTestLayout(t)
	store := memdir.NewStore(layout)

	// Write 2 local project memories (zero pollution in repo initially).
	w1, err := store.Write(memdir.WriteRequest{
		Scope:       memdir.ScopeProject,
		Type:        memdir.TypeProject,
		Name:        "团队发版规则",
		Description: "生产发布审批",
		Content:     "生产发布前必须由小周放行。",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Write(memdir.WriteRequest{
		Scope:       memdir.ScopeProject,
		Type:        memdir.TypeFeedback,
		Name:        "个人本地端口备注",
		Description: "本地调试端口",
		Content:     "本机 8080 被占，调试用 8099。",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Verify repo .smem/memory does not exist yet!
	if _, err := os.Stat(layout.RepoSharedDir); err == nil {
		t.Fatalf("repo .smem/memory should not exist by default")
	}

	// Selectively push ONLY the team rule into repo .smem/memory/.
	pushRes, err := SharePush(layout, []string{w1.File}, false, false)
	if err != nil {
		t.Fatalf("SharePush failed: %v", err)
	}
	if len(pushRes.Copied) != 1 || pushRes.Copied[0] != w1.File {
		t.Fatalf("unexpected push result: %+v", pushRes)
	}
	if _, err := os.Stat(filepath.Join(layout.RepoSharedDir, memdir.IndexFileName)); err != nil {
		t.Fatalf("expected repo MEMORY.md rebuilt after push: %v", err)
	}

	// Simulate teammate adding another file to repo .smem/memory/ and pulling it in.
	teammateDoc := memdir.RenderDocument(memdir.Frontmatter{
		Name:        "数据库迁移规范",
		Description: "修改 schema 时触发",
		Type:        memdir.TypeProject,
	}, "禁止在生产迁移中使用 LOCK TABLE。")
	if err := os.WriteFile(filepath.Join(layout.RepoSharedDir, "project_db_migration.md"), teammateDoc, 0644); err != nil {
		t.Fatal(err)
	}

	status, err := ShareStatus(layout)
	if err != nil {
		t.Fatal(err)
	}
	foundRepoOnly := false
	for _, it := range status.Items {
		if it.File == "project_db_migration.md" && it.Status == "repo_only" {
			foundRepoOnly = true
		}
	}
	if !foundRepoOnly {
		t.Fatalf("expected project_db_migration.md as repo_only, got %+v", status.Items)
	}

	pullRes, err := SharePull(layout, nil, true, false, false)
	if err != nil {
		t.Fatalf("SharePull failed: %v", err)
	}
	if len(pullRes.Copied) != 1 || pullRes.Copied[0] != "project_db_migration.md" {
		t.Fatalf("unexpected pull result: %+v", pullRes)
	}

	// Pulled memory is immediately visible in local project MEMORY.md!
	idx, err := store.Index("project")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(idx.Project.IndexContent, "project_db_migration.md") {
		t.Fatalf("expected pulled file in local MEMORY.md, got:\n%s", idx.Project.IndexContent)
	}
}
