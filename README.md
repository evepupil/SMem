# SMem · 通用 AI 编程 Agent 统一记忆底座

**SMem** 是一个正式、极简、本地优先（Local-First）的通用 AI 编程记忆工具。它以 **Claude Code 原生记忆格式**（`MEMORY.md` 索引 + 3 字段 Frontmatter 主题 Markdown + 4 分类纪律）为开放标准，让 **Claude Code、Codex CLI、Cursor、Gemini CLI、OpenCode、Windsurf、GitHub Copilot、Cline、Antigravity、Aider** 等所有 AI 编程工具共同读写同一份记忆。

- **零数据库、零向量库、零常驻进程**：纯 Markdown 文件即唯一真相源，人改、Claude 改或 `git pull` 即刻生效。
- **单一静态 Go 二进制**：纯标准库零第三方依赖，冷启动低于 5ms，跨平台（Windows / macOS / Linux）。
- **2 层清晰作用域**：`project`（默认存本机 `~/.smem/projects/<repo-id>/`，零污染项目 Git，自动跨 Git Worktree 共享，可按需选择性同步进仓库）+ `global`（个人跨项目偏好 `~/.smem/global/`，独立私有 Git 仓库同步）。
- **Claude Code 原生零损耗兼容**：默认通过 `autoMemoryDirectory` 直连 SMem 项目目录，100% 保留 Claude Code 原生自动记忆、`extractMemories` 与 `/dream`，同时由 SMem 补齐跨项目全局记忆并向其他所有 AI 工具开放同一份文件。
- **三通道统一接入**：5 工具精简 **MCP Server** + 超轻量 **Lifecycle Hooks**（会话启动自动注入 `MEMORY.md` 索引、显式长期约定提醒写入）+ `AGENTS.md` / `SKILL.md` / CLI 通用兜底。

---

## 快速开始

### 1. 编译安装

```sh
go install ./cmd/smem
```

或从仓库根目录直接构建单文件：

```sh
go build -o bin/smem ./cmd/smem        # macOS / Linux
go build -o bin/smem.exe ./cmd/smem    # Windows
```

### 2. 一键接入本机 AI 编程工具

在你的 Git 项目根目录中先预览再应用：

```sh
smem setup --dry-run
smem setup
```

- 默认自动探测本机已安装工具，也可用 `--agents claude,codex,cursor,gemini,opencode` 显式指定。
- **Claude Code 默认使用 `native` 模式**（配置 `.claude/settings.local.json` 的 `autoMemoryDirectory` 直连 SMem 项目目录 + 注入全局记忆 Hook）；如需强制所有读写统一经由 MCP，可指定 `--claude-mode managed`。
- 修改任何工具配置文件前，SMem 会自动在 `~/.smem/backups/` 备份原文件，随时可通过 `smem setup --remove` 安全回滚。

### 3. 导入既有原生记忆（可选）

```sh
smem import --from claude --dry-run
smem import --from claude
```

同样支持 `--from codex`、`--from cline` 以及任意 Markdown 目录 `--from dir --path <目录>`。

---

## 日常使用

正常使用任何已接入的 AI 编程工具即可，无需手动敲命令。需要手动查改、运维或跨工具共享时，可使用等价 CLI（全部支持 `--json`）：

| 场景 | 命令 |
| --- | --- |
| 查看当前项目与全局记忆索引 | `smem index` |
| 按需读取某条主题记忆（含相对年龄与 `etag`） | `smem read project_release_owner.md` |
| 写入 / 更新一条记忆 | `smem write --type project --name "发版审批人" --desc "涉及生产发版审批时触发" "发版前必须由小周放行。"` |
| 安全删除一条记忆 | `smem delete project_release_owner.md --etag <etag>` |
| 关键词检索全库 | `smem search 发版 小周` |
| 检查孤儿文件、超限、过期与潜在密钥 | `smem check`（加 `--fix` 自动修复索引） |
| 选择性同步到项目 Git 仓库（团队共享） | `smem share status`、`smem share push <file>`、`smem share pull` |
| 同步个人全局记忆私有 Git 仓库 | `smem sync` |
| 获取跨工具 4 阶段 Dream 整理协议 | `smem dream` |

---

## 目录与作用域

| 作用域 | 存储位置 | 用途 | 与 Git 的关系 |
| --- | --- | --- | --- |
| **`project`**（默认） | `~/.smem/projects/<repo-id>/` | 项目决策（`project`）、外部指针（`reference`）、项目专属纠错（`feedback`） | **默认不污染项目 Git**；同仓库多个 Git Worktree 自动共享；随时可用 `smem share push/pull` 选择性同步进 `<repo>/.smem/memory/`（或 `smem setup --shared` 直接绑定仓库目录） |
| **`global`** | `~/.smem/global/` | 跨项目个人画像（`user`）与通用工作偏好（`feedback`） | 本身是个人私有 Git 仓库，可通过 `smem sync` 跨设备同步 |

---

## 正式文档

- [架构设计](docs/架构设计.md)
- [记忆格式与行为规范](docs/格式规范.md)
- [主流 AI 编程工具适配矩阵](docs/工具适配.md)
- [主流编程助手的原生记忆调研](docs/调研/主流编程助手的记忆.md)

## 开发与验收门禁

```sh
go vet ./...
go test ./...
go test -race ./...
```
