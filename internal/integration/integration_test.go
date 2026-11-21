package integration

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"smem/internal/workspace"
)

func testOptions(agents string) Options {
	return Options{Agents: agents, Env: func(string) string { return "" }, HasCommand: func(string) bool { return false }}
}

func put(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}
func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestPreviewApplyRepeat(t *testing.T) {
	layout := workspace.Layout{Home: t.TempDir(), Project: t.TempDir()}
	opts := testOptions("claude,codex,cursor,copilot,gemini,opencode,windsurf,devin")
	claude := filepath.Join(layout.Project, "CLAUDE.md")
	codex := filepath.Join(layout.Home, ".codex", "AGENTS.override.md")
	inherited := filepath.Join(layout.Home, ".claude", "CLAUDE.md")
	put(t, claude, "\ufeff保留内容\r\n")
	put(t, codex, "override\n")
	put(t, inherited, "个人原文\n")
	put(t, filepath.Join(layout.Project, "AGENTS.md"), "手写\n")
	opts.DryRun = true
	var out bytes.Buffer
	if err := Init(layout, opts, &out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(layout.Home, ".mem")); !os.IsNotExist(err) || read(t, claude) != "\ufeff保留内容\r\n" {
		t.Fatal("preview had side effects")
	}
	opts.DryRun = false
	if err := Init(layout, opts, io.Discard); err != nil {
		t.Fatal(err)
	}
	if got := read(t, claude); got != "\ufeff@AGENTS.md\r\n保留内容\r\n" {
		t.Fatalf("CLAUDE: %q", got)
	}
	if _, err := os.Stat(filepath.Join(layout.Home, ".config", "opencode", "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatal("hid existing OpenCode fallback")
	}
	if _, err := os.Stat(filepath.Join(layout.Home, ".codex", "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatal("wrote inactive codex file")
	}
	for _, path := range []string{codex, inherited, filepath.Join(layout.Home, ".claude", "AGENTS.md"), filepath.Join(layout.Home, ".gemini", "GEMINI.md"), filepath.Join(layout.Project, "AGENTS.md"), filepath.Join(layout.Project, ".github", "copilot-instructions.md"), filepath.Join(layout.Project, ".windsurf", "rules", "smem.md")} {
		if got := read(t, path); strings.Count(got, start) != 1 || !strings.Contains(got, "mem search") {
			t.Fatalf("wrong rule: %s", path)
		}
	}
	out.Reset()
	if err := Init(layout, opts, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "更新说明：") {
		t.Fatalf("repeat not a no-op: %s", &out)
	}
}

func TestPreflightDoesNotPartiallyApply(t *testing.T) {
	layout := workspace.Layout{Home: t.TempDir(), Project: t.TempDir()}
	path := filepath.Join(layout.Home, ".gemini", "GEMINI.md")
	put(t, path, "keep\n"+start)
	if err := Init(layout, testOptions("claude,gemini"), io.Discard); err == nil {
		t.Fatal("accepted incomplete markers")
	}
	if _, err := os.Stat(filepath.Join(layout.Project, "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatal("partial update")
	}
	if read(t, path) != "keep\n"+start {
		t.Fatal("overwrote old rules")
	}
}

func TestProfileOverridesAndAutoDetection(t *testing.T) {
	layout := workspace.Layout{Home: t.TempDir()}
	opts := testOptions("auto")
	custom := filepath.Join(layout.Home, "another profile")
	put(t, filepath.Join(custom, "AGENTS.md"), "# own\n")
	opts.Env = func(key string) string {
		if key == "CODEX_HOME" {
			return custom
		}
		return ""
	}
	if err := Init(layout, opts, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(read(t, filepath.Join(custom, "AGENTS.md")), "# own\n") || !strings.Contains(read(t, filepath.Join(custom, "AGENTS.md")), rule) {
		t.Fatal("wrong profile")
	}
	if _, err := os.Stat(filepath.Join(layout.Home, ".claude")); !os.IsNotExist(err) {
		t.Fatal("touched unselected tool")
	}
}

func TestRenderRejectsAmbiguity(t *testing.T) {
	for _, raw := range [][]byte{[]byte(end + "\n" + start), []byte(start + "\n" + start + "\n" + end), []byte("inside " + start + "\n" + end), {0xff, 0x00}} {
		if _, err := render(raw, false); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
}
