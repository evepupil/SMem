package mcp

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

func TestMCPStdioFullLifecycle(t *testing.T) {
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
	store := memdir.NewStore(layout)
	srv := NewServer(store)

	send := func(req any) rpcResponse {
		b, err := json.Marshal(req)
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		if err := srv.Serve(bytes.NewReader(append(b, '\n')), &out); err != nil {
			t.Fatalf("Serve failed: %v", err)
		}
		var resp rpcResponse
		if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal response failed: %v\nraw: %s", err, out.String())
		}
		return resp
	}

	// 1. initialize
	initResp := send(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params":  map[string]any{"protocolVersion": "2025-03-26"},
	})
	if initResp.Error != nil {
		t.Fatalf("initialize error: %+v", initResp.Error)
	}

	// 2. tools/list -> 5 tools
	listResp := send(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"})
	listMap := listResp.Result.(map[string]any)
	tools := listMap["tools"].([]any)
	if len(tools) != 5 {
		t.Fatalf("expected 5 MCP tools, got %d", len(tools))
	}

	// 3. memory_write
	writeResp := send(map[string]any{
		"jsonrpc": "2.0",
		"id":      3,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "memory_write",
			"arguments": map[string]any{
				"scope":       "project",
				"type":        "project",
				"name":        "发版审批人",
				"description": "涉及生产发布审批时触发",
				"content":     "生产发布由小周放行。",
			},
		},
	})
	writeText := extractToolText(t, writeResp, false)
	if !strings.Contains(writeText, "状态：created") || !strings.Contains(writeText, "小周") {
		t.Fatalf("unexpected write text: %s", writeText)
	}

	// 4. memory_index + resources/read
	idxResp := send(map[string]any{
		"jsonrpc": "2.0",
		"id":      4,
		"method":  "tools/call",
		"params":  map[string]any{"name": "memory_index", "arguments": map[string]any{"include_manifest": true}},
	})
	idxText := extractToolText(t, idxResp, false)
	if !strings.Contains(idxText, "发版审批人") {
		t.Fatalf("expected index to contain 发版审批人, got:\n%s", idxText)
	}

	resResp := send(map[string]any{
		"jsonrpc": "2.0",
		"id":      5,
		"method":  "resources/read",
		"params":  map[string]any{"uri": "smem://index"},
	})
	if resResp.Error != nil {
		t.Fatalf("resources/read error: %+v", resResp.Error)
	}

	// 5. memory_search
	searchResp := send(map[string]any{
		"jsonrpc": "2.0",
		"id":      6,
		"method":  "tools/call",
		"params":  map[string]any{"name": "memory_search", "arguments": map[string]any{"query": "发版 小周"}},
	})
	searchText := extractToolText(t, searchResp, false)
	if !strings.Contains(searchText, "发版审批人") {
		t.Fatalf("expected search hit, got:\n%s", searchText)
	}

	// 6. memory_write secret rejected as tool isError=true
	secResp := send(map[string]any{
		"jsonrpc": "2.0",
		"id":      7,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "memory_write",
			"arguments": map[string]any{
				"scope":       "project",
				"type":        "reference",
				"name":        "Bad Secret",
				"description": "secret test",
				"content":     "ghp_1234567890abcdefghijklmnopqrstuvwxyz",
			},
		},
	})
	secText := extractToolText(t, secResp, true)
	if !strings.Contains(secText, "密钥") {
		t.Fatalf("expected secret error message, got: %s", secText)
	}

	// 7. prompts/get dream
	dreamResp := send(map[string]any{
		"jsonrpc": "2.0",
		"id":      8,
		"method":  "prompts/get",
		"params":  map[string]any{"name": "dream"},
	})
	if dreamResp.Error != nil {
		t.Fatalf("prompts/get error: %+v", dreamResp.Error)
	}
}

func TestMCPWorkspaceOverrideFromNonRepoDir(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	smemHome := filepath.Join(home, ".smem")
	nonRepo := filepath.Join(tmp, "global-dir")
	targetRepo := filepath.Join(tmp, "target-repo")
	if err := os.MkdirAll(nonRepo, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(targetRepo, ".git"), 0755); err != nil {
		t.Fatal(err)
	}

	layout, err := workspace.Resolve(workspace.ResolveOptions{HomeDir: home, SmemHome: smemHome, CWD: nonRepo})
	if err != nil {
		t.Fatal(err)
	}
	if layout.InProject {
		t.Fatalf("expected nonRepo not to be in project")
	}

	srv := NewServer(memdir.NewStore(layout))
	send := func(req any) rpcResponse {
		b, err := json.Marshal(req)
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		if err := srv.Serve(bytes.NewReader(append(b, '\n')), &out); err != nil {
			t.Fatalf("Serve failed: %v", err)
		}
		var resp rpcResponse
		if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal response failed: %v\nraw: %s", err, out.String())
		}
		return resp
	}

	// Write to targetRepo via workspace parameter even though server started in nonRepo.
	wResp := send(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "memory_write",
			"arguments": map[string]any{
				"scope":       "project",
				"type":        "project",
				"name":        "目标项目约定",
				"description": "测试跨目录指定 workspace 写入",
				"content":     "通过 workspace 参数写入 targetRepo 成功。",
				"workspace":   targetRepo,
			},
		},
	})
	wText := extractToolText(t, wResp, false)
	if !strings.Contains(wText, "状态：created") {
		t.Fatalf("unexpected write response: %s", wText)
	}

	// Index, search, read, and delete via workspace parameter.
	idxResp := send(map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "memory_index",
			"arguments": map[string]any{
				"scope":            "project",
				"include_manifest": true,
				"workspace":        targetRepo,
			},
		},
	})
	idxText := extractToolText(t, idxResp, false)
	if !strings.Contains(idxText, "目标项目约定") {
		t.Fatalf("expected index to contain 目标项目约定, got:\n%s", idxText)
	}

	searchResp := send(map[string]any{
		"jsonrpc": "2.0",
		"id":      3,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "memory_search",
			"arguments": map[string]any{
				"query":     "workspace",
				"scope":     "project",
				"workspace": targetRepo,
			},
		},
	})
	searchText := extractToolText(t, searchResp, false)
	if !strings.Contains(searchText, "目标项目约定") {
		t.Fatalf("expected search hit, got:\n%s", searchText)
	}

	// Read exact file
	readResp := send(map[string]any{
		"jsonrpc": "2.0",
		"id":      4,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "memory_read",
			"arguments": map[string]any{
				"scope":     "project",
				"files":     []string{"project_目标项目约定.md"},
				"workspace": targetRepo,
			},
		},
	})
	readText := extractToolText(t, readResp, false)
	if !strings.Contains(readText, "通过 workspace 参数写入 targetRepo 成功。") {
		t.Fatalf("expected read content, got:\n%s", readText)
	}
}

func extractToolText(t *testing.T, resp rpcResponse, wantIsError bool) string {
	t.Helper()
	if resp.Error != nil {
		t.Fatalf("unexpected JSON-RPC error: %+v", resp.Error)
	}
	resMap := resp.Result.(map[string]any)
	isErr, _ := resMap["isError"].(bool)
	if isErr != wantIsError {
		t.Fatalf("isError = %v, want %v; result = %+v", isErr, wantIsError, resMap)
	}
	content := resMap["content"].([]any)
	first := content[0].(map[string]any)
	return first["text"].(string)
}
