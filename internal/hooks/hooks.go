package hooks

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"smem/internal/memdir"
	"smem/internal/workspace"
)

// Options configures a hook invocation.
type Options struct {
	Agent      string // claude, codex, cursor, gemini, antigravity
	Event      string // session-start, prompt-submit, post-compact, before-agent, pre-invocation
	ClaudeMode string // native (inject global only on SessionStart) | managed (inject all)
}

type hookInput struct {
	Prompt         string   `json:"prompt"`
	UserPrompt     string   `json:"user_prompt"`
	Message        string   `json:"message"`
	Input          string   `json:"input"`
	CWD            string   `json:"cwd"`
	WorkspacePaths []string `json:"workspacePaths"`
	LastUserInput  string   `json:"lastUserInput"`
	InvocationNum  int      `json:"invocationNum"`
}

// Handle executes a deterministic, fail-open lifecycle hook (< 5ms, zero LLM).
// Strictly writes valid JSON to stdout; never pollutes stdout with logs.
func Handle(store *memdir.Store, opts Options, stdin io.Reader, stdout io.Writer) error {
	agent := strings.ToLower(strings.TrimSpace(opts.Agent))
	event := normalizeEvent(opts.Event)

	var in hookInput
	if stdin != nil {
		raw, _ := io.ReadAll(io.LimitReader(stdin, 256*1024))
		if len(strings.TrimSpace(string(raw))) > 0 {
			_ = json.Unmarshal(raw, &in)
		}
	}

	targetCWD := strings.TrimSpace(in.CWD)
	if targetCWD == "" && len(in.WorkspacePaths) > 0 {
		targetCWD = strings.TrimSpace(in.WorkspacePaths[0])
	}
	if targetCWD != "" {
		if resolved, err := workspace.Resolve(workspace.ResolveOptions{
			HomeDir:  store.Layout.HomeDir,
			SmemHome: store.Layout.SmemHome,
			CWD:      targetCWD,
		}); err == nil {
			nextStore := memdir.NewStore(resolved)
			nextStore.Now = store.Now
			store = nextStore
		}
	}

	prompt := firstNonEmpty(in.Prompt, in.UserPrompt, in.LastUserInput, in.Message, in.Input)

	var additionalContext string
	switch event {
	case "session-start", "post-compact":
		additionalContext = buildIndexInjection(store, agent, opts.ClaudeMode)
	case "prompt-submit":
		additionalContext = buildPromptSubmitContext(store, agent, opts.ClaudeMode, prompt, false)
	case "pre-invocation":
		// Skip duplicate prompt injection on subsequent tool-call iterations within the same turn.
		if in.InvocationNum > 1 {
			return writeHookJSON(stdout, agent, event, "")
		}
		additionalContext = buildPromptSubmitContext(store, agent, opts.ClaudeMode, prompt, false)
	case "before-agent":
		// Gemini CLI BeforeAgent runs before each turn; include compact index + intent hints.
		additionalContext = buildPromptSubmitContext(store, agent, opts.ClaudeMode, prompt, true)
	default:
		return writeHookJSON(stdout, agent, event, "")
	}

	return writeHookJSON(stdout, agent, event, additionalContext)
}

func normalizeEvent(ev string) string {
	switch strings.ToLower(strings.TrimSpace(ev)) {
	case "session-start", "sessionstart", "session_start":
		return "session-start"
	case "prompt-submit", "userpromptsubmit", "user_prompt_submit", "beforesubmitprompt", "before_submit_prompt":
		return "prompt-submit"
	case "pre-invocation", "preinvocation", "pre_invocation":
		return "pre-invocation"
	case "post-compact", "postcompact", "post_compact":
		return "post-compact"
	case "before-agent", "beforeagent", "before_agent":
		return "before-agent"
	default:
		return strings.ToLower(strings.TrimSpace(ev))
	}
}

func buildIndexInjection(store *memdir.Store, agent, claudeMode string) string {
	scopeFilter := "all"
	// In Claude Code native mode, Claude already loads project MEMORY.md via autoMemoryDirectory.
	// SMem injects only global MEMORY.md so there is zero duplication!
	if agent == "claude" && strings.ToLower(strings.TrimSpace(claudeMode)) == "native" {
		scopeFilter = "global"
	}

	rendered, idxRes, err := store.RenderCombinedIndex(scopeFilter)
	if err != nil {
		return ""
	}
	hasIndex := strings.TrimSpace(rendered) != ""
	isAntigravityProject := agent == "antigravity" && store.Layout.InProject
	if !hasIndex && !isAntigravityProject {
		return ""
	}

	var b strings.Builder
	b.WriteString("<smem-memory-index>\n")
	if isAntigravityProject {
		b.WriteString(fmt.Sprintf("当前工作区已绑定 SMem 共享记忆：%s（调用 `smem` MCP `memory_*` 工具读写项目记忆时请传入 `workspace: %q`）。\n",
			store.Layout.RepoRoot, store.Layout.RepoRoot))
	}
	if hasIndex {
		b.WriteString("以下是 SMem 跨工具共享记忆目录（项目记忆请按触发说明主动调用 MCP `memory_read` 读取正文后再作答；涉及代码/命令前请先核对当前源码）：\n\n")
		b.WriteString(rendered)
	} else {
		b.WriteString("当前项目与全局尚无已保存记忆条目；遇到持久约定、项目决策或个人偏好时，可调用 `memory_write` 保存。")
	}

	// Inline compact global preferences (up to 2KB total) so cross-project user preferences
	// take effect on Turn 1 without requiring an extra MCP round-trip in every session.
	if idxRes.Global != nil && len(idxRes.Global.Manifest) > 0 && len(idxRes.Global.Manifest) <= 10 {
		var files []string
		for _, m := range idxRes.Global.Manifest {
			files = append(files, m.File)
		}
		if entries, err := store.Read(memdir.ScopeGlobal, files); err == nil {
			var gb strings.Builder
			totalBytes := 0
			for _, e := range entries {
				chunk := fmt.Sprintf("- **%s** (%s): %s\n", e.Name, e.File, strings.TrimSpace(e.Body))
				if totalBytes+len(chunk) > 2048 {
					break
				}
				gb.WriteString(chunk)
				totalBytes += len(chunk)
			}
			if gb.Len() > 0 {
				b.WriteString("\n\n### 全局个人偏好生效内容 (global)\n")
				b.WriteString(strings.TrimSpace(gb.String()))
			}
		}
	}

	b.WriteString("\n</smem-memory-index>")
	return b.String()
}

func buildPromptSubmitContext(store *memdir.Store, agent, claudeMode, prompt string, includeIndex bool) string {
	var blocks []string

	if includeIndex {
		if idx := buildIndexInjection(store, agent, claudeMode); idx != "" {
			blocks = append(blocks, idx)
		}
	}

	intent := DetectMemoryIntent(prompt)
	switch intent {
	case IntentWrite:
		if agent == "claude" && strings.ToLower(strings.TrimSpace(claudeMode)) == "native" {
			blocks = append(blocks, "<smem-hint>检测到用户明确给出了长期约定/偏好：跨项目偏好请立即调用 `memory_write(scope=\"global\")` 保存；本项目长期约定请立即写入项目记忆，并在确认保存成功后再回复。</smem-hint>")
		} else {
			blocks = append(blocks, "<smem-hint>检测到用户明确给出了长期约定/偏好/纠错：请在回复前立即调用 SMem `memory_write`（或 `smem write`）持久化（仅跨项目习惯用 scope=\"global\"，其余默认 scope=\"project\"），拿到成功回执后再答复。一次性临时要求不要保存。</smem-hint>")
		}
	case IntentUpdateOrForget:
		blocks = append(blocks, "<smem-hint>检测到用户要求更正或忘掉某条已有记忆：请先调用 `memory_index` / `memory_search` 找到确切文件，再用 `memory_read` 获取最新 etag，然后调用 `memory_write`（仅保留现行结论、不留旧值）或 `memory_delete`。</smem-hint>")
	}

	// If MEMORY.md is truncated (>200 lines) and the user prompt matches entries beyond the index,
	// rescue them with compact pointers.
	if strings.TrimSpace(prompt) != "" {
		_, idxRes, err := store.RenderCombinedIndex("all")
		if err == nil {
			truncated := (idxRes.Project != nil && idxRes.Project.Truncated) ||
				(idxRes.Global != nil && idxRes.Global.Truncated)
			if truncated {
				hits, _ := store.Search(memdir.SearchOptions{Query: prompt, Limit: 3})
				if len(hits) > 0 {
					var hb strings.Builder
					hb.WriteString("<smem-overflow-hits>索引超限补召回（可调用 `memory_read` 查看）：\n")
					for _, h := range hits {
						hb.WriteString(fmt.Sprintf("- [%s] %s — %s (%s)\n", h.Scope, h.File, h.Name, h.Description))
					}
					hb.WriteString("</smem-overflow-hits>")
					blocks = append(blocks, hb.String())
				}
			}
		}
	}

	return strings.Join(blocks, "\n\n")
}

// MemoryIntent classifies explicit durable memory cues in a user prompt.
type MemoryIntent int

const (
	IntentNone MemoryIntent = iota
	IntentWrite
	IntentUpdateOrForget
)

var (
	oneOffCues = []string{
		"这一次", "这次先", "仅本次", "临时用", "先临时",
		"just this once", "for this time only", "temporarily",
	}
	updateForgetCues = []string{
		"忘掉", "忘记", "删掉这条记忆", "删除这条记忆", "不再由", "改由", "改成", "换成了", "更新约定", "纠正一下",
		"forget that", "forget the", "remove memory", "delete memory", "update memory", "instead of",
	}
	writeCues = []string{
		"记住", "记一下", "以后都", "以后这个项目", "以后所有项目", "以后每次", "每次都", "每次开工", "不要再", "别再", "永远", "长期约定", "统一用", "所有项目都",
		"remember that", "remember to", "from now on", "always use", "never use", "for all projects", "save this to memory", "keep in mind",
	}
)

// DetectMemoryIntent detects explicit user signals for persisting, correcting, or forgetting memory
// while avoiding false positives on explicit one-off instructions.
func DetectMemoryIntent(prompt string) MemoryIntent {
	p := strings.TrimSpace(prompt)
	if p == "" {
		return IntentNone
	}
	lower := strings.ToLower(p)

	// Explicit one-off instruction overrides write cues unless user also says "记住/以后".
	isOneOff := containsAny(lower, oneOffCues)
	if containsAny(lower, updateForgetCues) {
		return IntentUpdateOrForget
	}
	if containsAny(lower, writeCues) {
		if isOneOff && !strings.Contains(lower, "记住") && !strings.Contains(lower, "以后") && !strings.Contains(lower, "remember") {
			return IntentNone
		}
		return IntentWrite
	}
	return IntentNone
}

func containsAny(text string, phrases []string) bool {
	for _, p := range phrases {
		if strings.Contains(text, p) {
			return true
		}
	}
	return false
}

func writeHookJSON(w io.Writer, agent, event, additionalContext string) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)

	if strings.TrimSpace(additionalContext) == "" {
		return enc.Encode(map[string]any{})
	}

	switch agent {
	case "antigravity":
		return enc.Encode(map[string]any{
			"injectSteps": []map[string]any{
				{
					"systemMessage": map[string]any{
						"systemMessage": additionalContext,
					},
				},
			},
		})
	case "cursor":
		return enc.Encode(map[string]any{
			"additional_context": additionalContext,
		})
	case "gemini":
		return enc.Encode(map[string]any{
			"hookSpecificOutput": map[string]any{
				"hookEventName":     "BeforeAgent",
				"additionalContext": additionalContext,
			},
		})
	default:
		// Claude Code & Codex CLI standard hookSpecificOutput schema.
		eventName := "SessionStart"
		switch event {
		case "prompt-submit":
			eventName = "UserPromptSubmit"
		case "post-compact":
			eventName = "PostCompact"
		}
		return enc.Encode(map[string]any{
			"hookSpecificOutput": map[string]any{
				"hookEventName":     eventName,
				"additionalContext": additionalContext,
			},
		})
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}
