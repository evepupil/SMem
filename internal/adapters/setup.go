package adapters

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"smem/internal/workspace"
)

// AllSupportedAgents lists every built-in coding agent adapter.
var AllSupportedAgents = []string{
	"claude",
	"codex",
	"cursor",
	"gemini",
	"opencode",
	"copilot",
	"windsurf",
	"cline",
}

// SetupOptions configures smem setup.
type SetupOptions struct {
	Agents       string // "auto" or comma-separated list
	ClaudeMode   string // "native" (default, autoMemoryDirectory + global hook) | "managed" (MCP only)
	Shared       bool   // Bind project directly to <repo>/.smem/memory
	ProjectRules bool   // Also write repo-root AGENTS.md & .agents/skills/smem/SKILL.md (off by default to avoid polluting git status)
	DryRun       bool
	Remove       bool
	SmemBin      string
}

// PlannedAction represents one deterministic config/file operation.
type PlannedAction struct {
	Agent       string `json:"agent"`
	Kind        string `json:"kind"` // "mcp", "hooks", "claude-native", "rules", "skill", "marker"
	Path        string `json:"path"`
	Action      string `json:"action"` // "create", "update", "unchanged", "remove"
	Description string `json:"description"`
	BackupPath  string `json:"backup_path,omitempty"`
	after       []byte
	perm        os.FileMode
	deleteFile  bool
}

// SetupResult summarizes planned or executed setup changes.
type SetupResult struct {
	DetectedAgents []string        `json:"detected_agents"`
	SelectedAgents []string        `json:"selected_agents"`
	ClaudeMode     string          `json:"claude_mode"`
	SharedMode     bool            `json:"shared_mode"`
	ProjectDir     string          `json:"project_dir,omitempty"`
	GlobalDir      string          `json:"global_dir"`
	DryRun         bool            `json:"dry_run"`
	Remove         bool            `json:"remove"`
	Actions        []PlannedAction `json:"actions"`
}

// AgentStatus describes detection and integration state of one tool.
type AgentStatus struct {
	Agent      string `json:"agent"`
	Detected   bool   `json:"detected"`
	Configured bool   `json:"configured"`
	Details    string `json:"details"`
}

// DetectAgents identifies AI coding tools installed on the current machine/workspace.
func DetectAgents(layout workspace.Layout) []string {
	var found []string
	check := func(name string, cond bool) {
		if cond {
			found = append(found, name)
		}
	}

	codexHome := resolveCodexHome(layout.HomeDir)
	check("claude", dirOrFileExists(filepath.Join(layout.HomeDir, ".claude")) ||
		dirOrFileExists(filepath.Join(layout.HomeDir, ".claude.json")) ||
		hasBinary("claude"))
	check("codex", dirOrFileExists(codexHome) || hasBinary("codex"))
	check("cursor", dirOrFileExists(filepath.Join(layout.HomeDir, ".cursor")) ||
		(layout.InProject && dirOrFileExists(filepath.Join(layout.RepoRoot, ".cursor"))) ||
		hasBinary("cursor"))
	check("gemini", dirOrFileExists(filepath.Join(layout.HomeDir, ".gemini")) || hasBinary("gemini"))
	check("opencode", dirOrFileExists(filepath.Join(layout.HomeDir, ".config", "opencode")) || hasBinary("opencode"))
	check("copilot", dirOrFileExists(filepath.Join(layout.HomeDir, ".copilot")) ||
		(layout.InProject && dirOrFileExists(filepath.Join(layout.RepoRoot, ".github", "copilot-instructions.md"))))
	check("windsurf", dirOrFileExists(filepath.Join(layout.HomeDir, ".codeium", "windsurf")) ||
		(layout.InProject && dirOrFileExists(filepath.Join(layout.RepoRoot, ".windsurf"))))
	check("cline", (layout.InProject && (dirOrFileExists(filepath.Join(layout.RepoRoot, ".roo")) ||
		dirOrFileExists(filepath.Join(layout.RepoRoot, "memory-bank")))))

	return found
}

// Status inspects every supported agent and reports whether SMem is configured.
func Status(layout workspace.Layout) []AgentStatus {
	detectedMap := make(map[string]bool)
	for _, a := range DetectAgents(layout) {
		detectedMap[a] = true
	}

	var out []AgentStatus
	for _, agent := range AllSupportedAgents {
		configured, details := checkAgentConfigured(layout, agent)
		out = append(out, AgentStatus{
			Agent:      agent,
			Detected:   detectedMap[agent],
			Configured: configured,
			Details:    details,
		})
	}
	return out
}

// RunSetup plans and (unless DryRun) applies or removes SMem integrations safely.
func RunSetup(layout workspace.Layout, opts SetupOptions) (SetupResult, error) {
	if opts.ClaudeMode == "" {
		opts.ClaudeMode = "native"
	}
	if opts.ClaudeMode != "native" && opts.ClaudeMode != "managed" {
		return SetupResult{}, fmt.Errorf("无效的 --claude-mode %q（仅支持 native 或 managed）", opts.ClaudeMode)
	}
	if opts.SmemBin == "" {
		opts.SmemBin = "smem"
	}

	detected := DetectAgents(layout)
	selected, err := resolveSelectedAgents(opts.Agents, detected)
	if err != nil {
		return SetupResult{}, err
	}

	if opts.Shared && !layout.InProject {
		return SetupResult{}, errors.New("--shared 必须在 Git 项目仓库目录内使用")
	}

	// Determine effective project directory if --shared is requested.
	effectiveLayout := layout
	if opts.Shared {
		effectiveLayout.SharedMode = true
	}

	res := SetupResult{
		DetectedAgents: detected,
		SelectedAgents: selected,
		ClaudeMode:     opts.ClaudeMode,
		SharedMode:     effectiveLayout.SharedMode,
		ProjectDir:     effectiveLayout.ProjectDir(),
		GlobalDir:      effectiveLayout.GlobalDir,
		DryRun:         opts.DryRun,
		Remove:         opts.Remove,
	}

	var actions []PlannedAction
	for _, agent := range selected {
		agentActions, err := planAgentActions(effectiveLayout, agent, opts)
		if err != nil {
			return SetupResult{}, fmt.Errorf("规划 %s 接入失败：%w", agent, err)
		}
		actions = append(actions, agentActions...)
	}

	// Optional explicit project-level universal rules (AGENTS.md + .agents/skills/smem/SKILL.md).
	// Opt-in only so default setup leaves zero untracked files in team repositories!
	if effectiveLayout.InProject && opts.ProjectRules {
		pActions, err := planProjectUniversalRules(effectiveLayout, opts)
		if err != nil {
			return SetupResult{}, err
		}
		actions = append(actions, pActions...)
	}

	res.Actions = actions
	if opts.DryRun {
		return res, nil
	}

	// Ensure core memory directories exist.
	if err := os.MkdirAll(effectiveLayout.GlobalDir, 0700); err != nil {
		return SetupResult{}, err
	}
	if effectiveLayout.InProject {
		if opts.Shared && !opts.Remove {
			if err := workspace.EnableSharedMarker(effectiveLayout.RepoRoot); err != nil {
				return SetupResult{}, err
			}
		}
		if err := os.MkdirAll(effectiveLayout.ProjectDir(), 0700); err != nil {
			return SetupResult{}, err
		}
	}

	// Apply planned actions with automatic backup.
	for i := range res.Actions {
		act := &res.Actions[i]
		if act.Action == "unchanged" {
			continue
		}
		if act.deleteFile {
			if err := os.Remove(act.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return SetupResult{}, err
			}
			continue
		}
		backup, err := backupAndWriteFile(effectiveLayout.BackupsDir, act.Path, act.after, act.perm)
		if err != nil {
			return SetupResult{}, fmt.Errorf("写入 %s 失败：%w", act.Path, err)
		}
		act.BackupPath = backup
	}

	// Keep .claude/settings.local.json invisible to git status via .git/info/exclude!
	if effectiveLayout.InProject && !opts.Remove && containsString(selected, "claude") {
		_ = ensureGitExclude(effectiveLayout.RepoRoot, ".claude/settings.local.json")
	}

	return res, nil
}

func resolveSelectedAgents(spec string, detected []string) ([]string, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" || strings.EqualFold(spec, "auto") {
		if len(detected) > 0 {
			return detected, nil
		}
		// Fallback to claude + codex if none detected in clean environment.
		return []string{"claude", "codex"}, nil
	}
	if strings.EqualFold(spec, "all") {
		return append([]string(nil), AllSupportedAgents...), nil
	}
	valid := make(map[string]bool, len(AllSupportedAgents))
	for _, a := range AllSupportedAgents {
		valid[a] = true
	}
	var out []string
	seen := make(map[string]bool)
	for _, part := range strings.Split(spec, ",") {
		a := strings.ToLower(strings.TrimSpace(part))
		if a == "" {
			continue
		}
		if !valid[a] {
			return nil, fmt.Errorf("不支持的工具名 %q（可选：%s）", a, strings.Join(AllSupportedAgents, ", "))
		}
		if !seen[a] {
			seen[a] = true
			out = append(out, a)
		}
	}
	sort.Strings(out)
	return out, nil
}

func planAgentActions(layout workspace.Layout, agent string, opts SetupOptions) ([]PlannedAction, error) {
	switch agent {
	case "claude":
		return planClaude(layout, opts)
	case "codex":
		return planCodex(layout, opts)
	case "cursor":
		return planCursor(layout, opts)
	case "gemini":
		return planGemini(layout, opts)
	case "opencode":
		return planOpenCode(layout, opts)
	case "copilot":
		return planCopilot(layout, opts)
	case "windsurf":
		return planWindsurf(layout, opts)
	case "cline":
		return planCline(layout, opts)
	default:
		return nil, nil
	}
}

func planClaude(layout workspace.Layout, opts SetupOptions) ([]PlannedAction, error) {
	var actions []PlannedAction

	// 1. Register MCP server in ~/.claude.json
	claudeJSON := filepath.Join(layout.HomeDir, ".claude.json")
	a1, err := planJSONPatch("claude", "mcp", claudeJSON, "注册 SMem MCP 服务 (mcpServers.smem)", opts.Remove, func(root map[string]any) {
		servers := getOrCreateMap(root, "mcpServers")
		if opts.Remove {
			delete(servers, "smem")
		} else {
			servers["smem"] = map[string]any{
				"command": opts.SmemBin,
				"args":    []string{"mcp"},
			}
		}
	})
	if err != nil {
		return nil, err
	}
	actions = append(actions, a1)

	// 2. Register lightweight user-level hooks in ~/.claude/settings.json
	claudeSettings := filepath.Join(layout.HomeDir, ".claude", "settings.json")
	modeArg := opts.ClaudeMode
	a2, err := planJSONPatch("claude", "hooks", claudeSettings, fmt.Sprintf("配置 Claude Code 生命周期 Hooks（%s 模式）", modeArg), opts.Remove, func(root map[string]any) {
		hooksMap := getOrCreateMap(root, "hooks")
		ensureHookEntry(hooksMap, "SessionStart", quoteCommand(opts.SmemBin, "hook", "claude", "session-start", "--claude-mode", modeArg), "", opts.Remove)
		ensureHookEntry(hooksMap, "UserPromptSubmit", quoteCommand(opts.SmemBin, "hook", "claude", "prompt-submit", "--claude-mode", modeArg), "", opts.Remove)
		ensureHookEntry(hooksMap, "PostCompact", quoteCommand(opts.SmemBin, "hook", "claude", "post-compact", "--claude-mode", modeArg), "", opts.Remove)
		if len(hooksMap) == 0 {
			delete(root, "hooks")
		}
	})
	if err != nil {
		return nil, err
	}
	actions = append(actions, a2)

	// 3. Per-project Claude native autoMemoryDirectory bridge (or managed disable) in .claude/settings.local.json
	if layout.InProject {
		localSettings := filepath.Join(layout.RepoRoot, ".claude", "settings.local.json")
		desc := "配置 autoMemoryDirectory 原生直连 SMem 项目记忆目录（保留 Claude 原生 Auto Memory & /dream）"
		if opts.ClaudeMode == "managed" {
			desc = "关闭 Claude 原生 Auto Memory，统一经由 SMem MCP 托管读写"
		}
		a3, err := planJSONPatch("claude", "claude-native", localSettings, desc, opts.Remove, func(root map[string]any) {
			if opts.Remove {
				delete(root, "autoMemoryDirectory")
				delete(root, "autoMemoryEnabled")
				return
			}
			if opts.ClaudeMode == "native" {
				root["autoMemoryEnabled"] = true
				root["autoMemoryDirectory"] = layout.ProjectDir()
			} else {
				root["autoMemoryEnabled"] = false
				delete(root, "autoMemoryDirectory")
			}
		})
		if err != nil {
			return nil, err
		}
		actions = append(actions, a3)
	}

	// 4. User-level Claude Skill (~/.claude/skills/smem/SKILL.md)
	skillPath := filepath.Join(layout.HomeDir, ".claude", "skills", "smem", "SKILL.md")
	actions = append(actions, planStaticFile("claude", "skill", skillPath, "安装 SMem Agent Skill 规范", []byte(SkillMarkdown), 0644, opts.Remove))

	return actions, nil
}

func planCodex(layout workspace.Layout, opts SetupOptions) ([]PlannedAction, error) {
	var actions []PlannedAction
	codexHome := resolveCodexHome(layout.HomeDir)

	// 1. Register MCP server in $CODEX_HOME/config.toml (comment-preserving!)
	cfgPath := filepath.Join(codexHome, "config.toml")
	orig, err := readOptionalFile(cfgPath)
	if err != nil {
		return nil, err
	}
	after, err := PatchCodexTOML(orig, opts.SmemBin, opts.Remove)
	if err != nil {
		return nil, err
	}
	actions = append(actions, buildAction("codex", "mcp", cfgPath, "在 Codex config.toml 注册 [mcp_servers.smem]", orig, after, 0600, false))

	// 2. Register hooks in $CODEX_HOME/hooks.json
	hooksPath := filepath.Join(codexHome, "hooks.json")
	a2, err := planJSONPatch("codex", "hooks", hooksPath, "在 Codex hooks.json 注册 SessionStart 与 UserPromptSubmit 钩子", opts.Remove, func(root map[string]any) {
		hooksMap := getOrCreateMap(root, "hooks")
		ensureHookEntry(hooksMap, "SessionStart", quoteCommand(opts.SmemBin, "hook", "codex", "session-start"), "", opts.Remove)
		ensureHookEntry(hooksMap, "UserPromptSubmit", quoteCommand(opts.SmemBin, "hook", "codex", "prompt-submit"), "", opts.Remove)
		if len(hooksMap) == 0 {
			delete(root, "hooks")
		}
	})
	if err != nil {
		return nil, err
	}
	actions = append(actions, a2)

	// 3. Global Codex AGENTS.md (respects non-empty AGENTS.override.md)
	agentsFile := filepath.Join(codexHome, "AGENTS.md")
	overrideFile := filepath.Join(codexHome, "AGENTS.override.md")
	if info, err := os.Stat(overrideFile); err == nil && info.Size() > 0 {
		agentsFile = overrideFile
	}
	a3, err := planMarkdownPatch("codex", "rules", agentsFile, "维护 Codex 全局说明中的 SMem 用法段", AgentsRuleBlock, opts.Remove)
	if err != nil {
		return nil, err
	}
	actions = append(actions, a3)

	// 4. User-level Codex Skill ($CODEX_HOME/skills/smem/SKILL.md)
	skillPath := filepath.Join(codexHome, "skills", "smem", "SKILL.md")
	actions = append(actions, planStaticFile("codex", "skill", skillPath, "安装 Codex SMem Agent Skill 规范", []byte(SkillMarkdown), 0644, opts.Remove))

	return actions, nil
}

func planCursor(layout workspace.Layout, opts SetupOptions) ([]PlannedAction, error) {
	var actions []PlannedAction

	// 1. User-level Cursor MCP config (~/.cursor/mcp.json)
	mcpPath := filepath.Join(layout.HomeDir, ".cursor", "mcp.json")
	a1, err := planJSONPatch("cursor", "mcp", mcpPath, "在 Cursor mcp.json 注册 smem MCP 服务", opts.Remove, func(root map[string]any) {
		servers := getOrCreateMap(root, "mcpServers")
		if opts.Remove {
			delete(servers, "smem")
		} else {
			servers["smem"] = map[string]any{
				"command": opts.SmemBin,
				"args":    []string{"mcp"},
			}
		}
	})
	if err != nil {
		return nil, err
	}
	actions = append(actions, a1)

	// 2. User-level Cursor hooks (~/.cursor/hooks.json) — zero repo pollution!
	hooksPath := filepath.Join(layout.HomeDir, ".cursor", "hooks.json")
	a2, err := planJSONPatch("cursor", "hooks", hooksPath, "在 Cursor hooks.json 注册 sessionStart 与 beforeSubmitPrompt 钩子", opts.Remove, func(root map[string]any) {
		if !opts.Remove {
			if _, exists := root["version"]; !exists {
				root["version"] = 1
			}
		}
		hooksMap := getOrCreateMap(root, "hooks")
		ensureCursorHookEntry(hooksMap, "sessionStart", quoteCommand(opts.SmemBin, "hook", "cursor", "session-start"), opts.Remove)
		ensureCursorHookEntry(hooksMap, "beforeSubmitPrompt", quoteCommand(opts.SmemBin, "hook", "cursor", "prompt-submit"), opts.Remove)
		if len(hooksMap) == 0 {
			delete(root, "hooks")
		}
	})
	if err != nil {
		return nil, err
	}
	actions = append(actions, a2)

	return actions, nil
}

func planGemini(layout workspace.Layout, opts SetupOptions) ([]PlannedAction, error) {
	var actions []PlannedAction

	// 1. ~/.gemini/settings.json (MCP + BeforeAgent hook)
	settingsPath := filepath.Join(layout.HomeDir, ".gemini", "settings.json")
	a1, err := planJSONPatch("gemini", "mcp", settingsPath, "在 Gemini CLI settings.json 注册 smem MCP 与 BeforeAgent 钩子", opts.Remove, func(root map[string]any) {
		servers := getOrCreateMap(root, "mcpServers")
		if opts.Remove {
			delete(servers, "smem")
		} else {
			servers["smem"] = map[string]any{
				"command": opts.SmemBin,
				"args":    []string{"mcp"},
			}
		}
		hooksMap := getOrCreateMap(root, "hooks")
		ensureHookEntry(hooksMap, "BeforeAgent", quoteCommand(opts.SmemBin, "hook", "gemini", "before-agent"), "", opts.Remove)
		if len(hooksMap) == 0 {
			delete(root, "hooks")
		}
	})
	if err != nil {
		return nil, err
	}
	actions = append(actions, a1)

	// 2. ~/.gemini/GEMINI.md
	geminiMD := filepath.Join(layout.HomeDir, ".gemini", "GEMINI.md")
	a2, err := planMarkdownPatch("gemini", "rules", geminiMD, "维护 Gemini CLI 全局说明中的 SMem 用法段", AgentsRuleBlock, opts.Remove)
	if err != nil {
		return nil, err
	}
	actions = append(actions, a2)

	return actions, nil
}

func planOpenCode(layout workspace.Layout, opts SetupOptions) ([]PlannedAction, error) {
	var actions []PlannedAction

	// 1. ~/.config/opencode/opencode.json (OpenCode uses "mcp" key with type: "local" and command array!)
	cfgPath := filepath.Join(layout.HomeDir, ".config", "opencode", "opencode.json")
	a1, err := planJSONPatch("opencode", "mcp", cfgPath, "在 OpenCode opencode.json 注册本地 MCP 服务 (mcp.smem)", opts.Remove, func(root map[string]any) {
		mcpMap := getOrCreateMap(root, "mcp")
		if opts.Remove {
			delete(mcpMap, "smem")
		} else {
			mcpMap["smem"] = map[string]any{
				"type":    "local",
				"command": []string{opts.SmemBin, "mcp"},
				"enabled": true,
			}
		}
	})
	if err != nil {
		return nil, err
	}
	actions = append(actions, a1)

	// 2. ~/.config/opencode/AGENTS.md
	agentsMD := filepath.Join(layout.HomeDir, ".config", "opencode", "AGENTS.md")
	a2, err := planMarkdownPatch("opencode", "rules", agentsMD, "维护 OpenCode 全局说明中的 SMem 用法段", AgentsRuleBlock, opts.Remove)
	if err != nil {
		return nil, err
	}
	actions = append(actions, a2)

	return actions, nil
}

func planCopilot(layout workspace.Layout, opts SetupOptions) ([]PlannedAction, error) {
	var actions []PlannedAction
	// User-level Copilot CLI / global MCP config (~/.copilot/mcp-config.json)
	cfgPath := filepath.Join(layout.HomeDir, ".copilot", "mcp-config.json")
	a1, err := planJSONPatch("copilot", "mcp", cfgPath, "在 GitHub Copilot mcp-config.json 注册 smem MCP 服务", opts.Remove, func(root map[string]any) {
		servers := getOrCreateMap(root, "mcpServers")
		if opts.Remove {
			delete(servers, "smem")
		} else {
			servers["smem"] = map[string]any{
				"command": opts.SmemBin,
				"args":    []string{"mcp"},
			}
		}
	})
	if err != nil {
		return nil, err
	}
	actions = append(actions, a1)

	if layout.InProject && opts.ProjectRules {
		instPath := filepath.Join(layout.RepoRoot, ".github", "copilot-instructions.md")
		a2, err := planMarkdownPatch("copilot", "rules", instPath, "维护仓库 Copilot 说明中的 SMem 用法段", AgentsRuleBlock, opts.Remove)
		if err != nil {
			return nil, err
		}
		actions = append(actions, a2)
	}
	return actions, nil
}

func planWindsurf(layout workspace.Layout, opts SetupOptions) ([]PlannedAction, error) {
	cfgPath := filepath.Join(layout.HomeDir, ".codeium", "windsurf", "mcp_config.json")
	a1, err := planJSONPatch("windsurf", "mcp", cfgPath, "在 Windsurf mcp_config.json 注册 smem MCP 服务", opts.Remove, func(root map[string]any) {
		servers := getOrCreateMap(root, "mcpServers")
		if opts.Remove {
			delete(servers, "smem")
		} else {
			servers["smem"] = map[string]any{
				"command": opts.SmemBin,
				"args":    []string{"mcp"},
			}
		}
	})
	if err != nil {
		return nil, err
	}
	return []PlannedAction{a1}, nil
}

func planCline(layout workspace.Layout, opts SetupOptions) ([]PlannedAction, error) {
	if !layout.InProject {
		return nil, nil
	}
	cfgPath := filepath.Join(layout.RepoRoot, ".roo", "mcp.json")
	a1, err := planJSONPatch("cline", "mcp", cfgPath, "在 Roo/Cline 项目 mcp.json 注册 smem MCP 服务", opts.Remove, func(root map[string]any) {
		servers := getOrCreateMap(root, "mcpServers")
		if opts.Remove {
			delete(servers, "smem")
		} else {
			servers["smem"] = map[string]any{
				"command": opts.SmemBin,
				"args":    []string{"mcp"},
			}
		}
	})
	if err != nil {
		return nil, err
	}
	return []PlannedAction{a1}, nil
}

func planProjectUniversalRules(layout workspace.Layout, opts SetupOptions) ([]PlannedAction, error) {
	var actions []PlannedAction
	agentsMD := filepath.Join(layout.RepoRoot, "AGENTS.md")
	a1, err := planMarkdownPatch("project", "rules", agentsMD, "在项目根 AGENTS.md 维护通用 SMem 用法段", AgentsRuleBlock, opts.Remove)
	if err != nil {
		return nil, err
	}
	actions = append(actions, a1)

	skillPath := filepath.Join(layout.RepoRoot, ".agents", "skills", "smem", "SKILL.md")
	actions = append(actions, planStaticFile("project", "skill", skillPath, "在项目内安装开放标准 .agents/skills/smem/SKILL.md", []byte(SkillMarkdown), 0644, opts.Remove))
	return actions, nil
}

func planJSONPatch(agent, kind, path, desc string, remove bool, mutate func(map[string]any)) (PlannedAction, error) {
	orig, err := readOptionalFile(path)
	if err != nil {
		return PlannedAction{}, err
	}
	if len(orig) == 0 && remove {
		return PlannedAction{Agent: agent, Kind: kind, Path: path, Action: "unchanged", Description: desc}, nil
	}
	obj, err := ParseJSONObject(orig)
	if err != nil {
		return PlannedAction{}, fmt.Errorf("%s: %w", path, err)
	}
	mutate(obj)
	after, err := MarshalJSONObject(obj)
	if err != nil {
		return PlannedAction{}, err
	}
	return buildAction(agent, kind, path, desc, orig, after, 0600, false), nil
}

func planMarkdownPatch(agent, kind, path, desc, block string, remove bool) (PlannedAction, error) {
	orig, err := readOptionalFile(path)
	if err != nil {
		return PlannedAction{}, err
	}
	if len(orig) == 0 && remove {
		return PlannedAction{Agent: agent, Kind: kind, Path: path, Action: "unchanged", Description: desc}, nil
	}
	after, err := PatchMarkdownBlock(orig, block, remove)
	if err != nil {
		return PlannedAction{}, fmt.Errorf("%s: %w", path, err)
	}
	return buildAction(agent, kind, path, desc, orig, after, 0644, false), nil
}

func planStaticFile(agent, kind, path, desc string, content []byte, perm os.FileMode, remove bool) PlannedAction {
	orig, _ := readOptionalFile(path)
	if remove {
		if len(orig) == 0 {
			return PlannedAction{Agent: agent, Kind: kind, Path: path, Action: "unchanged", Description: desc}
		}
		return PlannedAction{Agent: agent, Kind: kind, Path: path, Action: "remove", Description: desc, deleteFile: true}
	}
	return buildAction(agent, kind, path, desc, orig, content, perm, false)
}

func buildAction(agent, kind, path, desc string, orig, after []byte, perm os.FileMode, del bool) PlannedAction {
	act := "update"
	switch {
	case del:
		act = "remove"
	case len(orig) == 0 && len(after) == 0:
		act = "unchanged"
	case len(orig) == 0:
		act = "create"
	case bytes.Equal(orig, after):
		act = "unchanged"
	}
	return PlannedAction{
		Agent:       agent,
		Kind:        kind,
		Path:        path,
		Action:      act,
		Description: desc,
		after:       after,
		perm:        perm,
		deleteFile:  del,
	}
}

func checkAgentConfigured(layout workspace.Layout, agent string) (bool, string) {
	switch agent {
	case "claude":
		hasMCP := fileContains(filepath.Join(layout.HomeDir, ".claude.json"), `"smem"`)
		hasLocal := layout.InProject && fileContains(filepath.Join(layout.RepoRoot, ".claude", "settings.local.json"), "autoMemoryDirectory")
		if hasMCP && hasLocal {
			return true, "MCP + autoMemoryDirectory 原生直连已就绪"
		}
		if hasMCP {
			return true, "全局 MCP 已配置（当前项目未配置 autoMemoryDirectory）"
		}
		return false, "未接入"
	case "codex":
		ch := resolveCodexHome(layout.HomeDir)
		hasMCP := fileContains(filepath.Join(ch, "config.toml"), "[mcp_servers.smem]")
		hasHooks := fileContains(filepath.Join(ch, "hooks.json"), "smem hook codex")
		if hasMCP && hasHooks {
			return true, "MCP + Hooks 已就绪"
		}
		if hasMCP {
			return true, "MCP 已配置"
		}
		return false, "未接入"
	case "cursor":
		hasMCP := fileContains(filepath.Join(layout.HomeDir, ".cursor", "mcp.json"), `"smem"`)
		hasHooks := fileContains(filepath.Join(layout.HomeDir, ".cursor", "hooks.json"), "smem hook cursor")
		if hasMCP && hasHooks {
			return true, "MCP + Hooks 已就绪"
		}
		if hasMCP {
			return true, "MCP 已配置"
		}
		return false, "未接入"
	case "gemini":
		p := filepath.Join(layout.HomeDir, ".gemini", "settings.json")
		if fileContains(p, `"smem"`) {
			return true, "MCP + BeforeAgent Hook 已就绪"
		}
		return false, "未接入"
	case "opencode":
		p := filepath.Join(layout.HomeDir, ".config", "opencode", "opencode.json")
		if fileContains(p, `"smem"`) {
			return true, "本地 MCP 已就绪"
		}
		return false, "未接入"
	case "copilot":
		p := filepath.Join(layout.HomeDir, ".copilot", "mcp-config.json")
		if fileContains(p, `"smem"`) {
			return true, "MCP 已就绪"
		}
		return false, "未接入"
	case "windsurf":
		p := filepath.Join(layout.HomeDir, ".codeium", "windsurf", "mcp_config.json")
		if fileContains(p, `"smem"`) {
			return true, "MCP 已就绪"
		}
		return false, "未接入"
	case "cline":
		if layout.InProject && fileContains(filepath.Join(layout.RepoRoot, ".roo", "mcp.json"), `"smem"`) {
			return true, "项目 MCP 已就绪"
		}
		return false, "未接入"
	default:
		return false, "未接入"
	}
}

func resolveCodexHome(homeDir string) string {
	if env := strings.TrimSpace(os.Getenv("CODEX_HOME")); env != "" {
		return env
	}
	return filepath.Join(homeDir, ".codex")
}

func readOptionalFile(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return b, nil
}

func dirOrFileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func fileContains(path, sub string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return strings.Contains(string(b), sub)
}

func hasBinary(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func containsString(list []string, target string) bool {
	for _, s := range list {
		if s == target {
			return true
		}
	}
	return false
}
