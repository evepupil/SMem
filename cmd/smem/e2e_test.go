package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func buildTestBinary(t *testing.T) string {
	t.Helper()
	binName := "smem"
	if runtime.GOOS == "windows" {
		binName = "smem.exe"
	}
	binPath := filepath.Join(t.TempDir(), binName)
	cmd := exec.Command("go", "build", "-o", binPath, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\n%s", err, string(out))
	}
	return binPath
}

func runSmem(t *testing.T, bin, cwd, smemHome, home, stdin string, args ...string) (string, string, error) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = cwd
	env := append(os.Environ(),
		"SMEM_HOME="+smemHome,
		"HOME="+home,
		"USERPROFILE="+home,
	)
	cmd.Env = env
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func TestEndToEndCrossAgentWorktreeIsolationAndHooks(t *testing.T) {
	bin := buildTestBinary(t)
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	smemHome := filepath.Join(home, ".smem")

	repoA := filepath.Join(tmp, "repoA")
	repoAWT := filepath.Join(tmp, "repoA-wt")
	repoB := filepath.Join(tmp, "repoB")

	for _, d := range []string{
		filepath.Join(repoA, ".git", "worktrees", "wt1"),
		filepath.Join(repoA, ".git", "info"),
		repoAWT,
		filepath.Join(repoB, ".git", "info"),
		filepath.Join(home, ".claude"),
		filepath.Join(home, ".codex"),
	} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}
	wtGitDir := filepath.Join(repoA, ".git", "worktrees", "wt1")
	if err := os.WriteFile(filepath.Join(wtGitDir, "commondir"), []byte("../..\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoAWT, ".git"), []byte("gitdir: "+wtGitDir+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// 1. Agent A writes project + global memory via MCP stdio subprocess!
	mcpInput := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"memory_write","arguments":{"scope":"project","type":"project","name":"上线放行人","description":"准备上线交接单时触发","content":"本项目上线放行人固定为小周。"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"memory_write","arguments":{"scope":"global","type":"user","name":"回复偏好","description":"输出代码改动时触发","content":"所有项目改完代码都保持简洁回复。"}}}`,
	}, "\n") + "\n"

	out, errOut, err := runSmem(t, bin, repoA, smemHome, home, mcpInput, "mcp")
	if err != nil {
		t.Fatalf("mcp subprocess failed: %v\nstderr=%s\nstdout=%s", err, errOut, out)
	}
	if !strings.Contains(out, "小周") || !strings.Contains(out, "简洁回复") {
		t.Fatalf("expected both writes confirmed in MCP output:\n%s", out)
	}

	// 2. Agent B (Codex) starts in a Git worktree of repoA -> receives BOTH global and project MEMORY.md!
	codexHook, _, err := runSmem(t, bin, repoAWT, smemHome, home, "{}", "hook", "codex", "session-start")
	if err != nil {
		t.Fatalf("codex hook failed: %v", err)
	}
	if !strings.Contains(codexHook, "上线放行人") || !strings.Contains(codexHook, "回复偏好") {
		t.Fatalf("codex hook in worktree missing memories:\n%s", codexHook)
	}

	// 3. Claude native mode in repoA receives ONLY global (project handled by autoMemoryDirectory).
	claudeHook, _, err := runSmem(t, bin, repoA, smemHome, home, "{}", "hook", "claude", "session-start", "--claude-mode", "native")
	if err != nil {
		t.Fatalf("claude hook failed: %v", err)
	}
	if !strings.Contains(claudeHook, "回复偏好") || strings.Contains(claudeHook, "上线放行人") {
		t.Fatalf("claude native hook should only inject global:\n%s", claudeHook)
	}

	// 4. Unrelated repoB sees global preference, ZERO leakage of repoA's project memory!
	repoBIndex, _, err := runSmem(t, bin, repoB, smemHome, home, "", "index")
	if err != nil {
		t.Fatalf("repoB index failed: %v", err)
	}
	if strings.Contains(repoBIndex, "上线放行人") {
		t.Fatalf("cross-project leakage detected in repoB:\n%s", repoBIndex)
	}
	if !strings.Contains(repoBIndex, "回复偏好") {
		t.Fatalf("global memory missing in repoB:\n%s", repoBIndex)
	}
}

func TestMultiProcessConcurrentETagConflict(t *testing.T) {
	bin := buildTestBinary(t)
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	smemHome := filepath.Join(home, ".smem")
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0755); err != nil {
		t.Fatal(err)
	}

	// Seed initial memory and capture file + etag via --json.
	out, errOut, err := runSmem(t, bin, repo, smemHome, home, "",
		"write", "--type", "project", "--name", "审批人", "--desc", "发版审批", "--json", "初版审批人是阿宁。")
	if err != nil {
		t.Fatalf("seed write failed: %v\n%s", err, errOut)
	}
	var seeded struct {
		File string `json:"file"`
		ETag string `json:"etag"`
	}
	if err := json.Unmarshal([]byte(out), &seeded); err != nil {
		t.Fatalf("parse seed JSON failed: %v\n%s", err, out)
	}

	// Two OS subprocesses race to update the exact same file with the exact same stale ETag.
	var wg sync.WaitGroup
	results := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			val := "更新为小周"
			if idx == 1 {
				val = "更新为老陈"
			}
			_, _, err := runSmem(t, bin, repo, smemHome, home, "",
				"write", "--type", "project", "--name", "审批人", "--desc", "发版审批",
				"--file", seeded.File, "--etag", seeded.ETag, val)
			results[idx] = err
		}(i)
	}
	wg.Wait()

	succeeded := 0
	for _, r := range results {
		if r == nil {
			succeeded++
		}
	}
	if succeeded != 1 {
		t.Fatalf("expected exactly 1 concurrent ETag update to succeed, got %d (errs=%v)", succeeded, results)
	}
}
