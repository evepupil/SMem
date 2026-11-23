package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"smem/internal/memdir"
)

const (
	serverName    = "smem"
	serverVersion = "2.0.0"

	serverInstructions = `SMem 是跨所有 AI 编程工具的统一共享记忆层（兼容 Claude Code MEMORY.md + 4 分类标准）。
核心规范：
1. 若上下文尚未包含记忆索引，开工前先调用 memory_index 查看目录；再按需用 memory_read 读取 1-5 个相关主题文件。
2. 涉及代码、文件路径、符号或命令行为的历史记忆，在动手前必须先核对当前仓库源码。
3. 用户明确表达长期约定、纠错偏好或项目决策时，立即调用 memory_write 保存：
   - 跨项目个人画像/习惯用 scope="global"（type="user" 或 "feedback"）；
   - 当前项目决策/外部链接/项目专属纠错用 scope="project"（type="project"、"reference" 或 "feedback"）；
   - description 写成清晰的“触发条件”，feedback/project 建议附带 **Why:** 与 **How to apply:**。
4. 不保存代码/Git 可直接查到的信息、临时调试过程、一次性要求或任何密钥凭证。
5. 更正或遗忘前必须先 memory_read 获取最新 etag；更新正文只保留现行结论，不残留旧值。`
)

// Server implements a zero-dependency JSON-RPC 2.0 MCP stdio server.
type Server struct {
	Store *memdir.Store
}

// NewServer creates an MCP server backed by Store.
func NewServer(store *memdir.Store) *Server {
	return &Server{Store: store}
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Serve runs the newline-delimited JSON-RPC 2.0 stdio loop until EOF.
func (s *Server) Serve(in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	// Support up to 10 MB single JSON-RPC line payloads.
	scanner.Buffer(make([]byte, 64*1024), 10*1024*1024)
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			_ = enc.Encode(rpcResponse{
				JSONRPC: "2.0",
				ID:      json.RawMessage("null"),
				Error:   &rpcError{Code: -32700, Message: "parse error: " + err.Error()},
			})
			continue
		}

		// JSON-RPC notifications have no id and expect no response.
		if len(req.ID) == 0 || string(req.ID) == "null" {
			continue
		}

		resp := s.handleRequest(req)
		if err := enc.Encode(resp); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func (s *Server) handleRequest(req rpcRequest) rpcResponse {
	switch req.Method {
	case "initialize":
		return s.handleInitialize(req)
	case "ping":
		return okResp(req.ID, map[string]any{})
	case "tools/list":
		return okResp(req.ID, map[string]any{"tools": toolDefinitions()})
	case "tools/call":
		return s.handleToolCall(req)
	case "resources/list":
		return okResp(req.ID, map[string]any{"resources": resourceDefinitions()})
	case "resources/read":
		return s.handleResourceRead(req)
	case "prompts/list":
		return okResp(req.ID, map[string]any{"prompts": promptDefinitions()})
	case "prompts/get":
		return s.handlePromptGet(req)
	default:
		return errResp(req.ID, -32601, "method not found: "+req.Method)
	}
}

func (s *Server) handleInitialize(req rpcRequest) rpcResponse {
	var params struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	_ = json.Unmarshal(req.Params, &params)

	version := negotiateProtocolVersion(params.ProtocolVersion)
	return okResp(req.ID, map[string]any{
		"protocolVersion": version,
		"capabilities": map[string]any{
			"tools":     map[string]any{"listChanged": false},
			"resources": map[string]any{"subscribe": false, "listChanged": false},
			"prompts":   map[string]any{"listChanged": false},
		},
		"serverInfo": map[string]any{
			"name":    serverName,
			"version": serverVersion,
		},
		"instructions": serverInstructions,
	})
}

func negotiateProtocolVersion(clientVersion string) string {
	switch clientVersion {
	case "2024-11-05", "2025-03-26", "2025-06-18":
		return clientVersion
	default:
		return "2025-03-26"
	}
}

func (s *Server) handleToolCall(req rpcRequest) rpcResponse {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return errResp(req.ID, -32602, "invalid params: "+err.Error())
	}

	var (
		text string
		err  error
	)
	switch params.Name {
	case "memory_index":
		text, err = s.callIndex(params.Arguments)
	case "memory_read":
		text, err = s.callRead(params.Arguments)
	case "memory_write":
		text, err = s.callWrite(params.Arguments)
	case "memory_delete":
		text, err = s.callDelete(params.Arguments)
	case "memory_search":
		text, err = s.callSearch(params.Arguments)
	default:
		return errResp(req.ID, -32602, "unknown tool: "+params.Name)
	}

	if err != nil {
		return okResp(req.ID, map[string]any{
			"content": []map[string]any{{"type": "text", "text": "错误：" + err.Error()}},
			"isError": true,
		})
	}
	return okResp(req.ID, map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
		"isError": false,
	})
}

func (s *Server) callIndex(raw json.RawMessage) (string, error) {
	var args struct {
		Scope           string `json:"scope"`
		IncludeManifest bool   `json:"include_manifest"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", err
		}
	}
	scopeFilter := args.Scope
	if scopeFilter == "" {
		scopeFilter = "all"
	}
	rendered, res, err := s.Store.RenderCombinedIndex(scopeFilter)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	if rendered == "" {
		b.WriteString("当前范围内尚无记忆条目。")
	} else {
		b.WriteString(rendered)
	}

	if args.IncludeManifest {
		b.WriteString("\n\n---\n### 详细清单 (Manifest)\n")
		writeManifest := func(idx *memdir.ScopeIndex) {
			if idx == nil || len(idx.Manifest) == 0 {
				return
			}
			for _, m := range idx.Manifest {
				b.WriteString(fmt.Sprintf("- [%s] `%s` (%s, age=%s, etag=%s) — %s: %s\n",
					m.Scope, m.File, m.Type, m.Age, m.ETag, m.Name, m.Description))
			}
		}
		writeManifest(res.Global)
		writeManifest(res.Project)
	}
	return strings.TrimSpace(b.String()), nil
}

func (s *Server) callRead(raw json.RawMessage) (string, error) {
	var args struct {
		Scope string   `json:"scope"`
		Files []string `json:"files"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", err
	}
	sc, err := memdir.ParseScope(args.Scope)
	if err != nil {
		return "", err
	}
	if len(args.Files) == 0 {
		return "", fmt.Errorf("files 参数不能为空")
	}
	if len(args.Files) > 5 {
		return "", fmt.Errorf("单次最多读取 5 个记忆文件以控制上下文预算，当前传入 %d 个", len(args.Files))
	}

	entries, err := s.Store.Read(sc, args.Files)
	if err != nil {
		return "", err
	}

	var parts []string
	for _, e := range entries {
		var b strings.Builder
		b.WriteString(fmt.Sprintf("### %s (%s/%s)\n", e.Name, e.Scope, e.File))
		b.WriteString(fmt.Sprintf("- type: `%s` | age: `%s` | etag: `%s`\n", e.Type, e.Age, e.ETag))
		b.WriteString(fmt.Sprintf("- description: %s\n", e.Description))
		if e.StaleWarning != "" {
			b.WriteString("> ⚠ " + e.StaleWarning + "\n")
		}
		b.WriteString("\n")
		b.WriteString(e.Body)
		parts = append(parts, strings.TrimSpace(b.String()))
	}
	return strings.Join(parts, "\n\n---\n\n"), nil
}

func (s *Server) callWrite(raw json.RawMessage) (string, error) {
	var args struct {
		Scope       string `json:"scope"`
		Type        string `json:"type"`
		Name        string `json:"name"`
		Description string `json:"description"`
		Content     string `json:"content"`
		File        string `json:"file"`
		ETag        string `json:"etag"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", err
	}
	sc, err := memdir.ParseScope(args.Scope)
	if err != nil {
		return "", err
	}
	mt, err := memdir.ParseMemoryType(args.Type)
	if err != nil {
		return "", err
	}

	res, err := s.Store.Write(memdir.WriteRequest{
		Scope:       sc,
		Type:        mt,
		Name:        args.Name,
		Description: args.Description,
		Content:     args.Content,
		File:        args.File,
		ETag:        args.ETag,
	})
	if err != nil {
		return "", err
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("状态：%s\n范围：%s\n文件：%s\netag：%s\n索引：%d 行 / %d 字节\n",
		res.Status, res.Scope, res.File, res.ETag, res.IndexLines, res.IndexBytes))
	if res.IndexWarning != "" {
		b.WriteString("警告：" + res.IndexWarning + "\n")
	}
	b.WriteString("\n现行正文回显：\n")
	b.WriteString(res.ContentEcho)
	return strings.TrimSpace(b.String()), nil
}

func (s *Server) callDelete(raw json.RawMessage) (string, error) {
	var args struct {
		Scope string `json:"scope"`
		File  string `json:"file"`
		ETag  string `json:"etag"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", err
	}
	sc, err := memdir.ParseScope(args.Scope)
	if err != nil {
		return "", err
	}
	if err := s.Store.Delete(memdir.DeleteRequest{
		Scope: sc,
		File:  args.File,
		ETag:  args.ETag,
	}); err != nil {
		return "", err
	}
	return fmt.Sprintf("已删除记忆 %s/%s 并同步更新 MEMORY.md。", sc, args.File), nil
}

func (s *Server) callSearch(raw json.RawMessage) (string, error) {
	var args struct {
		Query string `json:"query"`
		Scope string `json:"scope"`
		Type  string `json:"type"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", err
	}
	hits, err := s.Store.Search(memdir.SearchOptions{
		Query: args.Query,
		Scope: args.Scope,
		Type:  args.Type,
		Limit: args.Limit,
	})
	if err != nil {
		return "", err
	}
	if len(hits) == 0 {
		return "未命中相关记忆。可尝试更换关键词再查一次，或用 memory_index 查看完整目录。", nil
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("命中 %d 条相关记忆（请按需调用 memory_read 读取完整正文）：\n\n", len(hits)))
	for _, h := range hits {
		b.WriteString(fmt.Sprintf("- [%s] `%s` (%s, age=%s, etag=%s) — **%s**：%s\n",
			h.Scope, h.File, h.Type, h.Age, h.ETag, h.Name, h.Description))
		if h.Snippet != "" {
			b.WriteString(fmt.Sprintf("  > %s\n", h.Snippet))
		}
	}
	return strings.TrimSpace(b.String()), nil
}

func (s *Server) handleResourceRead(req rpcRequest) rpcResponse {
	var params struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return errResp(req.ID, -32602, "invalid params: "+err.Error())
	}
	if params.URI != "smem://index" {
		return errResp(req.ID, -32602, "unknown resource URI: "+params.URI)
	}
	rendered, _, err := s.Store.RenderCombinedIndex("all")
	if err != nil {
		return errResp(req.ID, -32603, err.Error())
	}
	if rendered == "" {
		rendered = "# SMem Memory Index\n\n（当前暂无记忆条目）\n"
	}
	return okResp(req.ID, map[string]any{
		"contents": []map[string]any{
			{
				"uri":      "smem://index",
				"mimeType": "text/markdown",
				"text":     rendered,
			},
		},
	})
}

func (s *Server) handlePromptGet(req rpcRequest) rpcResponse {
	var params struct {
		Name      string            `json:"name"`
		Arguments map[string]string `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return errResp(req.ID, -32602, "invalid params: "+err.Error())
	}
	if params.Name != "dream" {
		return errResp(req.ID, -32602, "unknown prompt: "+params.Name)
	}
	scopeFilter := params.Arguments["scope"]
	if scopeFilter == "" {
		scopeFilter = "all"
	}
	report, _ := s.Store.Check(scopeFilter, false)
	var extra strings.Builder
	extra.WriteString(DreamPromptText)
	extra.WriteString(fmt.Sprintf("\n\n当前整理目标作用域：%s（当前预检发现 %d 项提示）。", scopeFilter, len(report.Issues)))

	return okResp(req.ID, map[string]any{
		"description": "对 SMem 记忆库执行四阶段 Dream 整理与索引精简",
		"messages": []map[string]any{
			{
				"role": "user",
				"content": map[string]any{
					"type": "text",
					"text": extra.String(),
				},
			},
		},
	})
}

func toolDefinitions() []map[string]any {
	return []map[string]any{
		{
			"name":        "memory_index",
			"description": "查看全局（global）和当前项目（project）的 MEMORY.md 记忆目录索引。开工时若上下文未注入索引，应先调用此工具。",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"scope": map[string]any{
						"type":        "string",
						"enum":        []string{"all", "project", "global"},
						"description": "要查看的范围，默认 all",
					},
					"include_manifest": map[string]any{
						"type":        "boolean",
						"description": "是否附带所有主题文件的详细清单（含 type、age、etag），用于整理或核对",
					},
				},
			},
		},
		{
			"name":        "memory_read",
			"description": "按文件名批量读取 1-5 个主题记忆的完整正文、etag（用于后续更新/删除）以及相对年龄与保鲜核对提醒。",
			"inputSchema": map[string]any{
				"type":     "object",
				"required": []string{"scope", "files"},
				"properties": map[string]any{
					"scope": map[string]any{
						"type": "string",
						"enum": []string{"project", "global"},
					},
					"files": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"minItems":    1,
						"maxItems":    5,
						"description": "来自 MEMORY.md 或 memory_search 的主题文件名列表（如 project_release_owner.md）",
					},
				},
			},
		},
		{
			"name":        "memory_write",
			"description": "新增或原位更新一条主题记忆，并自动同步 MEMORY.md 索引。自动拦截密钥凭证。更新已有文件必须先 memory_read 获取最新 file 和 etag，正文只保留现行结论。",
			"inputSchema": map[string]any{
				"type":     "object",
				"required": []string{"type", "name", "description", "content"},
				"properties": map[string]any{
					"scope": map[string]any{
						"type":        "string",
						"enum":        []string{"project", "global"},
						"description": "默认 project；仅跨项目个人画像与通用习惯用 global",
					},
					"type": map[string]any{
						"type":        "string",
						"enum":        []string{"user", "feedback", "project", "reference"},
						"description": "Claude Code 四分类：user（用户画像）、feedback（纠错/规则）、project（项目决策/约束）、reference（外部资源指针）",
					},
					"name": map[string]any{
						"type":        "string",
						"description": "简短清晰的主题标题",
					},
					"description": map[string]any{
						"type":        "string",
						"description": "单行触发条件说明（描述何时应想起该记忆，供未来检索判断）",
					},
					"content": map[string]any{
						"type":        "string",
						"description": "Markdown 正文；feedback 与 project 类型建议包含 **Why:** 与 **How to apply:**",
					},
					"file": map[string]any{
						"type":        "string",
						"description": "更新已有记忆时必填目标文件名；新建时留空自动生成",
					},
					"etag": map[string]any{
						"type":        "string",
						"description": "更新已有记忆时必填最新 etag（来自 memory_read）",
					},
				},
			},
		},
		{
			"name":        "memory_delete",
			"description": "安全删除单条记忆文件并从 MEMORY.md 移除索引行。删除前必须先 memory_read 核对内容并传入最新 etag。",
			"inputSchema": map[string]any{
				"type":     "object",
				"required": []string{"scope", "file", "etag"},
				"properties": map[string]any{
					"scope": map[string]any{"type": "string", "enum": []string{"project", "global"}},
					"file":  map[string]any{"type": "string"},
					"etag":  map[string]any{"type": "string"},
				},
			},
		},
		{
			"name":        "memory_search",
			"description": "按关键词跨全局与项目记忆进行快速全文检索（支持中英文与代码标识符分词），返回摘要、文件名与 etag。",
			"inputSchema": map[string]any{
				"type":     "object",
				"required": []string{"query"},
				"properties": map[string]any{
					"query": map[string]any{"type": "string", "description": "搜索关键词（推荐 2-3 个实词）"},
					"scope": map[string]any{"type": "string", "enum": []string{"all", "project", "global"}},
					"type":  map[string]any{"type": "string", "enum": []string{"user", "feedback", "project", "reference"}},
					"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 20},
				},
			},
		},
	}
}

func resourceDefinitions() []map[string]any {
	return []map[string]any{
		{
			"uri":         "smem://index",
			"name":        "SMem Combined Memory Index",
			"description": "当前全局与项目 MEMORY.md 索引的实时合并视图",
			"mimeType":    "text/markdown",
		},
	}
}

func promptDefinitions() []map[string]any {
	return []map[string]any{
		{
			"name":        "dream",
			"description": "执行四阶段 Dream 整理：摸底、识别冲突与重复、合并规范化并重建 200 行以内的 MEMORY.md",
			"arguments": []map[string]any{
				{
					"name":        "scope",
					"description": "整理范围：all、project 或 global（默认 all）",
					"required":    false,
				},
			},
		},
	}
}

func okResp(id json.RawMessage, result any) rpcResponse {
	return rpcResponse{JSONRPC: "2.0", ID: id, Result: result}
}

func errResp(id json.RawMessage, code int, msg string) rpcResponse {
	return rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}}
}
