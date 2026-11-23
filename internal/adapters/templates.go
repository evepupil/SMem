package adapters

const (
	markerStart = "<!-- smem:start -->"
	markerEnd   = "<!-- smem:end -->"

	tomlMarkerStart = "# smem:start"
	tomlMarkerEnd   = "# smem:end"
)

// AgentsRuleBlock is the minimal cross-agent behavioral contract injected into AGENTS.md / GEMINI.md / etc.
const AgentsRuleBlock = `使用 SMem 统一读写跨工具共享记忆（优先调用 MCP 工具 ` + "`memory_*`" + `，无 MCP 时使用 ` + "`smem`" + ` CLI）：
- 开工先确认上下文中的 MEMORY.md 索引（若未自动注入则先调用 ` + "`memory_index`" + `），再按需用 ` + "`memory_read`" + ` 读取 1-5 个相关主题。
- 历史记忆仅作参考，涉及代码、路径或命令前必须先核对当前源码。
- 用户明确交代长期约定、偏好或纠错时，当场调用 ` + "`memory_write`" + ` 保存并拿到成功回执再回复：跨项目个人习惯用 ` + "`scope=\"global\"`" + `，本项目决策/约束默认 ` + "`scope=\"project\"`" + `；按四分类（` + "`user`/`feedback`/`project`/`reference`" + `）填写，` + "`description`" + ` 写成触发条件。
- 不保存代码/Git 可直接查到的信息、一次性临时要求或任何密钥凭证。更正或遗忘前必须先读取最新 ` + "`etag`" + `，更新正文只保留现行结论。`

// SkillMarkdown follows the open Agent Skills (SKILL.md) standard, packaging Claude Code's
// proven memory discipline for every AI coding agent.
const SkillMarkdown = `---
name: smem
description: Cross-agent persistent memory using Claude Code's MEMORY.md + 4-type Markdown standard. Use when recalling project/user context, saving durable decisions/preferences/corrections, updating or forgetting stale memories, or running Dream consolidation.
---

# SMem · 跨工具统一记忆使用规范

SMem 让所有 AI 编程工具（Claude Code、Codex、Cursor、Gemini CLI、OpenCode、Copilot、Windsurf、Cline 等）共同读写同一套纯 Markdown 记忆（` + "`MEMORY.md`" + ` 索引 + 主题 ` + "`.md`" + ` 文件）。

## 1. 两个作用域

- **project**（默认）：当前仓库的决策、发版/部署约定、外部资源链接、项目专属纠错。
- **global**：跨项目的个人技术画像、语言风格与通用偏好。

## 2. 四种记忆类型（` + "`type`" + `）

- **user**：开发者的角色、专长、沟通偏好（通常写入 ` + "`global`" + `）。
- **feedback**：明确纠错或被确认有效的做法（跨项目写 ` + "`global`" + `，仅限单项目写 ` + "`project`" + `）。正文推荐写清 ` + "`**Why:**`" + ` 与 ` + "`**How to apply:**`" + `。
- **project**：无法从代码或 Git 历史推导出的项目约束、审批人、环境坑点、阶段目标（相对时间改写成具体日期 ` + "`YYYY-MM-DD`" + `）。
- **reference**：外部看板、监控、文档或工单系统的位置指针。

## 3. 什么不要记

- 代码或 ` + "`git log`" + ` 能直接查到的实现细节、文件路径列表。
- 一次性临时要求（如“这一次先用 npm”）或临时排障过程。
- 已经写在 ` + "`CLAUDE.md`" + ` / ` + "`AGENTS.md`" + ` 中的静态规则。
- 任何 API Key、Token、私钥、密码等敏感凭证。

## 4. 读写与更新铁律

1. **先看索引再按需读**：通过 ` + "`memory_index`" + `（或 Hook 注入）浏览 ` + "`MEMORY.md`" + `，再用 ` + "`memory_read`" + ` 批量读取 1-5 个相关文件。
2. **先核对后行动**：当 ` + "`memory_read`" + ` 提示记忆距今超过 1 天时，涉及代码或命令前必须先查证当前源码。
3. **写 ` + "`description`" + ` 写成触发条件**：明确描述“什么任务/场景下应该想起这条记忆”。
4. **现行真相更新**：纠正或删除必须先 ` + "`memory_read`" + ` 获取最新 ` + "`etag`" + `；更新正文只写现行结论，不把旧值留在正文里污染后续检索。
5. **定期 Dream 整理**：当条目变杂或索引接近 200 行时，按四阶段（Orient → Gather → Consolidate → Prune & Index）合并去重并精简 ` + "`MEMORY.md`" + `。
`

// UniversalSnippet returns copy-paste instructions for any AI coding tool without a dedicated adapter.
func UniversalSnippet(smemBin string) string {
	return `### 通用接入说明（适用于任意支持 MCP 或终端命令的 AI 编程工具）

1. **MCP 配置（推荐）**：在工具的 MCP 配置中添加 stdio 服务：
   command: "` + smemBin + `"
   args: ["mcp"]

2. **无 MCP 时的通用提示词（粘贴到系统提示或项目说明）**：
   ` + AgentsRuleBlock
}
