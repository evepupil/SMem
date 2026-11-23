package hooks

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"smem/internal/memdir"
	"smem/internal/workspace"
)

func newHookTestStore(t *testing.T) *memdir.Store {
	t.Helper()
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
	s := memdir.NewStore(layout)
	if _, err := s.Write(memdir.WriteRequest{
		Scope:       memdir.ScopeGlobal,
		Type:        memdir.TypeUser,
		Name:        "简洁回复偏好",
		Description: "输出代码后不需要总结",
		Content:     "不要在结尾复述改动摘要。",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Write(memdir.WriteRequest{
		Scope:       memdir.ScopeProject,
		Type:        memdir.TypeProject,
		Name:        "生产发版审批人",
		Description: "发版交接单审批人",
		Content:     "本项目发版由小周审批放行。",
	}); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestClaudeNativeVsManagedSessionStart(t *testing.T) {
	store := newHookTestStore(t)

	// Claude native mode: injects ONLY global (project handled natively via autoMemoryDirectory).
	var nativeOut bytes.Buffer
	if err := Handle(store, Options{Agent: "claude", Event: "session-start", ClaudeMode: "native"}, strings.NewReader("{}"), &nativeOut); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(nativeOut.String(), "简洁回复偏好") {
		t.Fatalf("native mode should inject global memory, got: %s", nativeOut.String())
	}
	if strings.Contains(nativeOut.String(), "生产发版审批人") {
		t.Fatalf("native mode should NOT duplicate project memory, got: %s", nativeOut.String())
	}

	// Codex / Claude managed mode: injects BOTH global and project!
	var codexOut bytes.Buffer
	if err := Handle(store, Options{Agent: "codex", Event: "session-start"}, strings.NewReader("{}"), &codexOut); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(codexOut.String(), "简洁回复偏好") || !strings.Contains(codexOut.String(), "生产发版审批人") {
		t.Fatalf("codex should receive both global and project memory indexes, got: %s", codexOut.String())
	}
}

func TestCursorAndGeminiSchemasAndIntentDetection(t *testing.T) {
	store := newHookTestStore(t)

	// Cursor prompt-submit with explicit write cue.
	var cursorOut bytes.Buffer
	in := `{"prompt":"以后这个项目发版都找阿宁放行"}`
	if err := Handle(store, Options{Agent: "cursor", Event: "beforeSubmitPrompt"}, strings.NewReader(in), &cursorOut); err != nil {
		t.Fatal(err)
	}
	var curMap map[string]any
	if err := json.Unmarshal(cursorOut.Bytes(), &curMap); err != nil {
		t.Fatalf("invalid cursor JSON: %v\n%s", err, cursorOut.String())
	}
	if ctx, _ := curMap["additional_context"].(string); !strings.Contains(ctx, "memory_write") {
		t.Fatalf("expected memory_write hint in cursor output, got: %s", cursorOut.String())
	}

	// Gemini BeforeAgent includes index + update cue.
	var gemOut bytes.Buffer
	gemIn := `{"prompt":"现在改由小周放行，更新一下"}`
	if err := Handle(store, Options{Agent: "gemini", Event: "BeforeAgent"}, strings.NewReader(gemIn), &gemOut); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gemOut.String(), "BeforeAgent") || !strings.Contains(gemOut.String(), "etag") {
		t.Fatalf("unexpected gemini hook output: %s", gemOut.String())
	}

	// One-off instruction should NOT trigger write hint.
	if got := DetectMemoryIntent("这一次先用 npm 跑一下测试"); got != IntentNone {
		t.Fatalf("one-off instruction falsely triggered intent %v", got)
	}
}

func TestAntigravitySessionStartAndPreInvocation(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	smemHome := filepath.Join(home, ".smem")
	hookProcDir := filepath.Join(home, ".gemini", "config")
	targetRepo := filepath.Join(tmp, "golfjoy-play")
	for _, d := range []string{hookProcDir, filepath.Join(targetRepo, ".git")} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}

	// Simulate Antigravity launching the global hook with process CWD in ~/.gemini/config
	// while passing targetRepo in stdin JSON "cwd" / "workspacePaths".
	procLayout, err := workspace.Resolve(workspace.ResolveOptions{HomeDir: home, SmemHome: smemHome, CWD: hookProcDir})
	if err != nil {
		t.Fatal(err)
	}
	procStore := memdir.NewStore(procLayout)

	// 1. SessionStart even on an empty repo emits injectSteps binding workspace path.
	var startEmptyOut bytes.Buffer
	startPayload, _ := json.Marshal(map[string]any{
		"cwd":            targetRepo,
		"workspacePaths": []string{targetRepo},
	})
	if err := Handle(procStore, Options{Agent: "antigravity", Event: "session-start"}, bytes.NewReader(startPayload), &startEmptyOut); err != nil {
		t.Fatal(err)
	}
	var startParsed struct {
		InjectSteps []struct {
			SystemMessage struct {
				SystemMessage string `json:"systemMessage"`
			} `json:"systemMessage"`
		} `json:"injectSteps"`
	}
	if err := json.Unmarshal(startEmptyOut.Bytes(), &startParsed); err != nil {
		t.Fatalf("invalid Antigravity SessionStart JSON: %v\n%s", err, startEmptyOut.String())
	}
	if len(startParsed.InjectSteps) != 1 || !strings.Contains(startParsed.InjectSteps[0].SystemMessage.SystemMessage, targetRepo) {
		t.Fatalf("expected Antigravity injectSteps containing %s, got: %s", targetRepo, startEmptyOut.String())
	}

	// Seed a project memory in targetRepo and verify SessionStart resolves it via stdin cwd.
	targetLayout, err := workspace.Resolve(workspace.ResolveOptions{HomeDir: home, SmemHome: smemHome, CWD: targetRepo})
	if err != nil {
		t.Fatal(err)
	}
	targetStore := memdir.NewStore(targetLayout)
	if _, err := targetStore.Write(memdir.WriteRequest{
		Scope:       memdir.ScopeProject,
		Type:        memdir.TypeProject,
		Name:        "门禁远端主机",
		Description: "跑构建或测试门禁时触发",
		Content:     "重活统一在 cicd mac 192.168.0.68 上执行。",
	}); err != nil {
		t.Fatal(err)
	}

	var startFilledOut bytes.Buffer
	if err := Handle(procStore, Options{Agent: "antigravity", Event: "session-start"}, bytes.NewReader(startPayload), &startFilledOut); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(startFilledOut.String(), "门禁远端主机") {
		t.Fatalf("expected Antigravity SessionStart to include targetRepo memory, got: %s", startFilledOut.String())
	}

	// 2. PreInvocation invocationNum=1 triggers memory_write hint; invocationNum=2 returns {}.
	pre1Payload, _ := json.Marshal(map[string]any{
		"cwd":           targetRepo,
		"lastUserInput": "记住：以后这个项目都统一用 pnpm",
		"invocationNum": 1,
	})
	var pre1Out bytes.Buffer
	if err := Handle(procStore, Options{Agent: "antigravity", Event: "PreInvocation"}, bytes.NewReader(pre1Payload), &pre1Out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pre1Out.String(), "injectSteps") || !strings.Contains(pre1Out.String(), "memory_write") {
		t.Fatalf("expected PreInvocation invocationNum=1 to emit memory_write hint, got: %s", pre1Out.String())
	}

	pre2Payload, _ := json.Marshal(map[string]any{
		"cwd":           targetRepo,
		"lastUserInput": "记住：以后这个项目都统一用 pnpm",
		"invocationNum": 2,
	})
	var pre2Out bytes.Buffer
	if err := Handle(procStore, Options{Agent: "antigravity", Event: "PreInvocation"}, bytes.NewReader(pre2Payload), &pre2Out); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(pre2Out.String()) != "{}" {
		t.Fatalf("expected PreInvocation invocationNum=2 to return {}, got: %s", pre2Out.String())
	}
}
