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

func TestSetupAntigravityPreservesExistingHooksAndRemove(t *testing.T) {
	layout := newAdapterTestLayout(t)
	cfgRoot := filepath.Join(layout.HomeDir, ".gemini", "config")
	if err := os.MkdirAll(cfgRoot, 0755); err != nil {
		t.Fatal(err)
	}

	// Seed existing mcp_config.json and hooks.json (orca-status)
	mcpPath := filepath.Join(cfgRoot, "mcp_config.json")
	if err := os.WriteFile(mcpPath, []byte("{\n  \"mcpServers\": {\n    \"vibex-delegation-mcp\": {\"command\": \"vibex\"}\n  }\n}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	hooksPath := filepath.Join(cfgRoot, "hooks.json")
	if err := os.WriteFile(hooksPath, []byte("{\n  \"orca-status\": {\n    \"Stop\": [{\"type\": \"command\", \"command\": \"orca-stop\"}]\n  }\n}\n"), 0600); err != nil {
		t.Fatal(err)
	}

	// 1. Setup antigravity
	if _, err := RunSetup(layout, SetupOptions{Agents: "antigravity", SmemBin: "smem"}); err != nil {
		t.Fatalf("antigravity setup failed: %v", err)
	}

	mcpAfter, err := os.ReadFile(mcpPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mcpAfter), "vibex-delegation-mcp") || !strings.Contains(string(mcpAfter), `"smem"`) {
		t.Fatalf("mcp_config.json missing expected keys:\n%s", string(mcpAfter))
	}

	hooksAfter, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(hooksAfter), "orca-status") || !strings.Contains(string(hooksAfter), "smem hook antigravity session-start") || !strings.Contains(string(hooksAfter), "smem hook antigravity pre-invocation") {
		t.Fatalf("hooks.json missing expected keys:\n%s", string(hooksAfter))
	}

	skillPath := filepath.Join(cfgRoot, "skills", "smem", "SKILL.md")
	if _, err := os.Stat(skillPath); err != nil {
		t.Fatalf("expected Antigravity SKILL.md created: %v", err)
	}

	// 2. Idempotent second run
	res2, err := RunSetup(layout, SetupOptions{Agents: "antigravity", SmemBin: "smem"})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range res2.Actions {
		if a.Action != "unchanged" {
			t.Fatalf("expected idempotent unchanged on 2nd run, got %+v", a)
		}
	}

	// 3. Remove cleanly strips smem while preserving orca-status and vibex-delegation-mcp
	if _, err := RunSetup(layout, SetupOptions{Agents: "antigravity", Remove: true, SmemBin: "smem"}); err != nil {
		t.Fatalf("antigravity remove failed: %v", err)
	}
	mcpRemoved, _ := os.ReadFile(mcpPath)
	if !strings.Contains(string(mcpRemoved), "vibex-delegation-mcp") || strings.Contains(string(mcpRemoved), `"smem"`) {
		t.Fatalf("mcp_config.json not cleanly cleaned:\n%s", string(mcpRemoved))
	}
	hooksRemoved, _ := os.ReadFile(hooksPath)
	if !strings.Contains(string(hooksRemoved), "orca-status") || strings.Contains(string(hooksRemoved), "smem hook antigravity") {
		t.Fatalf("hooks.json not cleanly cleaned:\n%s", string(hooksRemoved))
	}
	if _, err := os.Stat(skillPath); err == nil {
		t.Fatalf("expected SKILL.md removed on --remove")
	}
}

func TestSetupClaudeCustomConfigDir(t *testing.T) {
	layout := newAdapterTestLayout(t)
	customCfgDir := filepath.Join(filepath.Dir(layout.HomeDir), "rec-account-55818", "config")
	if err := os.MkdirAll(customCfgDir, 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", customCfgDir)

	if _, err := RunSetup(layout, SetupOptions{Agents: "claude", ClaudeMode: "native", SmemBin: "smem"}); err != nil {
		t.Fatalf("setup with CLAUDE_CONFIG_DIR failed: %v", err)
	}

	customJSON := filepath.Join(customCfgDir, ".claude.json")
	customSettings := filepath.Join(customCfgDir, "settings.json")
	customSkill := filepath.Join(customCfgDir, "skills", "smem", "SKILL.md")

	if !fileContains(customJSON, `"smem"`) {
		t.Fatalf("expected smem MCP in %s", customJSON)
	}
	if !fileContains(customSettings, "smem hook claude") {
		t.Fatalf("expected smem hooks in %s", customSettings)
	}
	if _, err := os.Stat(customSkill); err != nil {
		t.Fatalf("expected skill file in %s: %v", customSkill, err)
	}
	if _, err := os.Stat(filepath.Join(layout.HomeDir, ".claude.json")); err == nil {
		t.Fatalf("default ~/.claude.json should not be modified when CLAUDE_CONFIG_DIR is set")
	}
}

func TestDiscoverImportSourcesAndCodexFiltering(t *testing.T) {
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	layout := newAdapterTestLayout(t)

	// Populate Claude default project memory dir (2 topic files + MEMORY.md).
	claudeDir := workspace.ClaudeNativeMemoryDir(layout.HomeDir, layout.RepoRoot)
	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claudeDir, "MEMORY.md"), []byte("# index\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claudeDir, "proj_rule.md"), []byte("开发规矩：禁止直接 push main。\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claudeDir, "feedback_terse.md"), []byte("回复简短直接。\n"), 0600); err != nil {
		t.Fatal(err)
	}

	// Populate Codex memories dir including files that must be skipped (.git, MEMORY.md, raw_memories.md, memory_summary.md, skills).
	codexMem := filepath.Join(layout.HomeDir, ".codex", "memories")
	for _, sub := range []string{".git", "skills/my-skill", "extensions/notes", "rollout_summaries"} {
		if err := os.MkdirAll(filepath.Join(codexMem, sub), 0755); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite := func(rel, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(codexMem, rel), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("MEMORY.md", "# codex big index\n")
	mustWrite("memory_summary.md", "consolidated digest\n")
	mustWrite("raw_memories.md", "huge raw dump\n")
	mustWrite(".git/HEAD.md", "should skip hidden dir\n")
	mustWrite("skills/my-skill/SKILL.md", "should skip skills dir\n")
	mustWrite("extensions/notes/pref.md", "Codex 扩展偏好记录。\n")
	mustWrite("rollout_summaries/r1.md", "Rollout 会话总结条目。\n")

	cands := DiscoverImportSources(layout)
	if len(cands) != 2 {
		t.Fatalf("expected 2 discovered import candidates (claude + codex), got %+v", cands)
	}
	if cands[0].Source != "claude" || cands[0].Count != 2 || cands[0].DefaultScope != memdir.ScopeProject || !cands[0].Selected {
		t.Fatalf("unexpected Claude candidate: %+v", cands[0])
	}
	if cands[1].Source != "codex" || cands[1].Count != 2 || cands[1].DefaultScope != memdir.ScopeGlobal || !cands[1].Selected {
		t.Fatalf("unexpected Codex candidate (expected 2 filtered entries -> global): %+v", cands[1])
	}

	// Running RunImport with From="codex" and empty Scope defaults to global and skips summary dumps.
	store := memdir.NewStore(layout)
	codexRes, err := RunImport(store, ImportOptions{From: "codex"})
	if err != nil {
		t.Fatalf("codex import failed: %v", err)
	}
	if codexRes.Scope != memdir.ScopeGlobal || codexRes.Imported != 2 {
		t.Fatalf("expected codex import to default to global and import 2 entries, got %+v", codexRes)
	}

	// If project is bound directly to Claude's native memory dir, discovery must exclude it to prevent self-import.
	if _, err := workspace.BindProject(layout.SmemHome, layout.RepoID, claudeDir); err != nil {
		t.Fatal(err)
	}
	boundLayout, err := workspace.Resolve(workspace.ResolveOptions{HomeDir: layout.HomeDir, SmemHome: layout.SmemHome, CWD: layout.RepoRoot})
	if err != nil {
		t.Fatal(err)
	}
	candsAfterBind := DiscoverImportSources(boundLayout)
	for _, c := range candsAfterBind {
		if c.Source == "claude" {
			t.Fatalf("bound Claude directory must not be listed for self-import: %+v", c)
		}
	}
}
