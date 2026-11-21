package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestHandoff(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "user")
	project := filepath.Join(root, "项目 A")
	other := filepath.Join(root, "project B")
	subdir := filepath.Join(project, "src")
	for _, path := range []string{home, subdir, other} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	binary := filepath.Join(root, "mem")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	if output, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %s %v", output, err)
	}
	for key, value := range map[string]string{"HOME": home, "USERPROFILE": home, "CODEX_HOME": filepath.Join(home, ".codex"), "CLAUDE_CONFIG_DIR": filepath.Join(home, ".claude"), "XDG_CONFIG_HOME": filepath.Join(home, ".config"), "GIT_CONFIG_GLOBAL": filepath.Join(root, "empty.gitconfig"), "GIT_CONFIG_NOSYSTEM": "1"} {
		t.Setenv(key, value)
	}
	run := func(dir, program string, args ...string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, program, args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s %v: %v\n%s", program, args, err, out)
		}
		return string(out)
	}
	for _, dir := range []string{project, other} {
		run(dir, "git", "init", "-q", "--template=")
	}
	old := filepath.Join(home, ".claude", "projects", "old", "memory", "MEMORY.md")
	if err := os.MkdirAll(filepath.Dir(old), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(old, []byte("private-unmigrated-canary"), 0600); err != nil {
		t.Fatal(err)
	}
	run(subdir, binary, "init", "--agents", "claude,codex,cursor,gemini,copilot,opencode,windsurf,devin")
	run(project, binary, "add", "验收代号：青柚；部署要重建容器")
	run(subdir, binary, "add", "--global", "验收习惯：先看差异")
	handoff := run(subdir, binary, "search", "验收")
	if !strings.Contains(handoff, "青柚") || !strings.Contains(handoff, "先看差异") {
		t.Fatalf("handoff: %s", handoff)
	}
	isolated := run(other, binary, "search", "验收")
	if strings.Contains(isolated, "青柚") || !strings.Contains(isolated, "先看差异") {
		t.Fatalf("scope leak: %s", isolated)
	}
	if result := run(project, binary, "search", "private-unmigrated-canary"); !strings.Contains(result, "没有找到") {
		t.Fatalf("old memory imported: %s", result)
	}
	if prompt := run(project, binary, "migrate"); !strings.Contains(prompt, "等我确认") || strings.Contains(prompt, "private-unmigrated-canary") {
		t.Fatal("unsafe migration output")
	}
	if data, err := os.ReadFile(old); err != nil || string(data) != "private-unmigrated-canary" {
		t.Fatalf("old memory changed: %v", err)
	}
	if os.Getenv("SMEM_REAL_CODEX") != "1" {
		return
	}
	program := "codex"
	if runtime.GOOS == "windows" {
		program = "codex.cmd"
	}
	t.Setenv("OPENAI_BASE_URL", "http://127.0.0.1:1/v1")
	t.Setenv("CODEX_DISABLE_UPDATE", "1")
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, program, "debug", "prompt-input", "synthetic fixture")
	cmd.Dir = subdir
	data, err := cmd.Output()
	if err != nil {
		t.Fatalf("real Codex prompt-input: %v", err)
	}
	if !json.Valid(data) || !strings.Contains(string(data), "mem search") || !strings.Contains(string(data), "mem add") || strings.Contains(string(data), "青柚") || strings.Contains(string(data), "private-unmigrated-canary") {
		t.Fatal("expected only lookup instructions in real Codex prompt")
	}
	t.Log("real Codex sees search/add instructions, and neither fixture memory is in the starting prompt")
}
