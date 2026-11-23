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
