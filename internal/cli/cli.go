package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"smem/internal/adapters"
	"smem/internal/hooks"
	"smem/internal/mcp"
	"smem/internal/memdir"
	"smem/internal/workspace"
)

const Version = "2.0.0"

const usageText = `smem · 通用 AI 编程 Agent 统一记忆底座（Claude Code 原生格式兼容）

用法:
  smem setup [--dry-run] [--agents auto|claude,codex,cursor,gemini,opencode,copilot,windsurf,cline]
             [--claude-mode native|managed] [--shared] [--project-rules] [--status] [--remove]
  smem index [--scope all|project|global] [--rebuild] [--manifest] [--json]
  smem read [--scope project|global] [--json] <file> [file...]
  smem write [--scope project|global] --type user|feedback|project|reference --name "标题"
             [--desc "触发条件"] [--file 文件名 --etag 校验码] [--stdin] [--json] "正文"
  smem delete [--scope project|global] --etag <etag> [--json] <file>
  smem search [--scope all|project|global] [--type ...] [--limit 8] [--json] <关键词...>
  smem check [--scope all|project|global] [--fix] [--json]
  smem share status | push [--all] [--dry-run] [file...] | pull [--all] [--force] [--dry-run] [file...]
  smem import --from claude|codex|cline|dir [--path 目录] [--scope project|global] [--dry-run] [--json]
  smem sync [--remote <私有Git仓库URL>]
  smem dream [--scope all|project|global]
  smem mcp
  smem hook <claude|codex|cursor|gemini> <session-start|prompt-submit|post-compact|before-agent>
  smem version
`

// Run executes a SMem CLI command against layout using the provided stdio streams.
func Run(args []string, layout workspace.Layout, in io.Reader, out, errOut io.Writer) error {
	if len(args) == 0 {
		_, _ = fmt.Fprint(out, usageText)
		return nil
	}

	store := memdir.NewStore(layout)

	switch args[0] {
	case "help", "-h", "--help":
		_, _ = fmt.Fprint(out, usageText)
		return nil
	case "version", "-v", "--version":
		_, _ = fmt.Fprintf(out, "smem v%s\n", Version)
		return nil
	case "setup":
		return runSetup(args[1:], layout, out, errOut)
	case "index":
		return runIndex(args[1:], store, out, errOut)
	case "read":
		return runRead(args[1:], store, out, errOut)
	case "write":
		return runWrite(args[1:], store, in, out, errOut)
	case "delete":
		return runDelete(args[1:], store, out, errOut)
	case "search":
		return runSearch(args[1:], store, out, errOut)
	case "check":
		return runCheck(args[1:], store, out, errOut)
	case "share":
		return runShare(args[1:], layout, out, errOut)
	case "import":
		return runImport(args[1:], store, out, errOut)
	case "sync":
		return runSync(args[1:], layout, out, errOut)
	case "dream":
		return runDream(args[1:], store, out, errOut)
	case "mcp":
		return mcp.NewServer(store).Serve(in, out)
	case "hook":
		return runHook(args[1:], store, in, out, errOut)
	default:
		return fmt.Errorf("未知子命令 %q；运行 `smem --help` 查看全部用法", args[0])
	}
}

func runSetup(args []string, layout workspace.Layout, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("smem setup", flag.ContinueOnError)
	fs.SetOutput(errOut)
	agentsSpec := fs.String("agents", "auto", "auto、all 或逗号分隔的工具名")
	claudeMode := fs.String("claude-mode", "native", "Claude Code 模式：native（原生 autoMemoryDirectory 直连）或 managed（全托管 MCP）")
	shared := fs.Bool("shared", false, "直接绑定项目记忆到 <repo>/.smem/memory/（默认存放 ~/.smem/projects/<repo-id>/ 不污染仓库）")
	projectRules := fs.Bool("project-rules", false, "同时在项目根写入 AGENTS.md 与 .agents/skills/smem/SKILL.md")
	dryRun := fs.Bool("dry-run", false, "仅预览拟改动的配置文件，不实际写入")
	statusOnly := fs.Bool("status", false, "查看各主流编程工具的探测与接入状态")
	remove := fs.Bool("remove", false, "安全移除 SMem 写入的接入配置（保留记忆文件）")
	smemBin := fs.String("bin", "", "写入工具配置中的 smem 可执行文件路径（默认自动解析）")
	asJSON := fs.Bool("json", false, "输出 JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *statusOnly {
		statuses := adapters.Status(layout)
		if *asJSON {
			return writeJSON(out, map[string]any{
				"in_project":  layout.InProject,
				"repo_id":     layout.RepoID,
				"project_dir": layout.ProjectDir(),
				"global_dir":  layout.GlobalDir,
				"shared_mode": layout.SharedMode,
				"agents":      statuses,
			})
		}
		_, _ = fmt.Fprintf(out, "全局记忆目录：%s\n", layout.GlobalDir)
		if layout.InProject {
			mode := "本机隔离（零污染仓库）"
			if layout.SharedMode {
				mode = "仓库直连共享 (.smem/memory)"
			}
			_, _ = fmt.Fprintf(out, "当前项目目录：%s [%s]\n\n", layout.ProjectDir(), mode)
		} else {
			_, _ = fmt.Fprint(out, "当前项目目录：（未处于 Git 仓库中）\n\n")
		}
		for _, st := range statuses {
			det := "未检测到"
			if st.Detected {
				det = "已安装"
			}
			cfg := "未接入"
			if st.Configured {
				cfg = "已接入"
			}
			_, _ = fmt.Fprintf(out, "- %-10s | %-6s | %-6s | %s\n", st.Agent, det, cfg, st.Details)
		}
		return nil
	}

	bin := strings.TrimSpace(*smemBin)
	if bin == "" {
		bin = resolveDefaultBin()
	}

	res, err := adapters.RunSetup(layout, adapters.SetupOptions{
		Agents:       *agentsSpec,
		ClaudeMode:   *claudeMode,
		Shared:       *shared,
		ProjectRules: *projectRules,
		DryRun:       *dryRun,
		Remove:       *remove,
		SmemBin:      bin,
	})
	if err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(out, res)
	}

	header := "SMem 接入完成"
	if res.DryRun {
		header = "SMem 接入预览（--dry-run，未写入任何文件）"
	} else if res.Remove {
		header = "SMem 接入已安全移除（已有记忆文件已保留）"
	}
	_, _ = fmt.Fprintf(out, "%s\n", header)
	_, _ = fmt.Fprintf(out, "- 目标工具：%s\n", strings.Join(res.SelectedAgents, ", "))
	_, _ = fmt.Fprintf(out, "- 全局记忆：%s\n", res.GlobalDir)
	if res.ProjectDir != "" {
		_, _ = fmt.Fprintf(out, "- 项目记忆：%s\n", res.ProjectDir)
	}
	_, _ = fmt.Fprintln(out, "\n配置变更清单：")
	for _, act := range res.Actions {
		_, _ = fmt.Fprintf(out, "  [%-9s] %-8s %-13s %s\n", act.Action, act.Agent, act.Kind, act.Path)
		if act.BackupPath != "" {
			_, _ = fmt.Fprintf(out, "              ↳ 已自动备份原文件：%s\n", act.BackupPath)
		}
	}
	if !res.Remove {
		_, _ = fmt.Fprintln(out, "\n"+adapters.UniversalSnippet(bin))
		if !res.DryRun {
			_, _ = fmt.Fprintln(out, "\n提示：请重新开启 AI 编程工具会话以加载最新 MCP 与 Hooks。")
		}
	}
	return nil
}

func runIndex(args []string, store *memdir.Store, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("smem index", flag.ContinueOnError)
	fs.SetOutput(errOut)
	scopeFilter := fs.String("scope", "all", "all、project 或 global")
	rebuild := fs.Bool("rebuild", false, "从目录内主题文件 Frontmatter 重建 MEMORY.md")
	manifest := fs.Bool("manifest", false, "附带每个主题文件的 etag、type 与 age 清单")
	asJSON := fs.Bool("json", false, "输出 JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *rebuild {
		stats, err := store.RebuildIndex(*scopeFilter)
		if err != nil {
			return err
		}
		if *asJSON {
			return writeJSON(out, stats)
		}
		for sc, st := range stats {
			_, _ = fmt.Fprintf(out, "已重建 %s/MEMORY.md（%d 行 / %d 字节）\n", sc, st.Lines, st.Bytes)
			if st.Warning != "" {
				_, _ = fmt.Fprintf(out, "  警告：%s\n", st.Warning)
			}
		}
		return nil
	}

	rendered, res, err := store.RenderCombinedIndex(*scopeFilter)
	if err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(out, res)
	}
	if rendered == "" {
		_, _ = fmt.Fprintln(out, "当前范围内尚无记忆条目。")
	} else {
		_, _ = fmt.Fprintln(out, rendered)
	}
	if *manifest {
		_, _ = fmt.Fprintln(out, "\n--- 详细清单 ---")
		printManifest := func(idx *memdir.ScopeIndex) {
			if idx == nil {
				return
			}
			for _, m := range idx.Manifest {
				_, _ = fmt.Fprintf(out, "[%s] %s | type=%s | age=%s | etag=%s | %s\n",
					m.Scope, m.File, m.Type, m.Age, m.ETag, m.Name)
			}
		}
		printManifest(res.Global)
		printManifest(res.Project)
	}
	return nil
}

func runRead(args []string, store *memdir.Store, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("smem read", flag.ContinueOnError)
	fs.SetOutput(errOut)
	scopeStr := fs.String("scope", "project", "project 或 global")
	asJSON := fs.Bool("json", false, "输出 JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return errors.New("用法：smem read [--scope project|global] <file> [file...]")
	}
	sc, err := memdir.ParseScope(*scopeStr)
	if err != nil {
		return err
	}
	entries, err := store.Read(sc, fs.Args())
	if err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(out, entries)
	}
	for i, e := range entries {
		if i > 0 {
			_, _ = fmt.Fprintln(out, "\n---")
		}
		_, _ = fmt.Fprintf(out, "# %s (%s/%s)\n", e.Name, e.Scope, e.File)
		_, _ = fmt.Fprintf(out, "type: %s | age: %s | etag: %s\n", e.Type, e.Age, e.ETag)
		_, _ = fmt.Fprintf(out, "description: %s\n", e.Description)
		if e.StaleWarning != "" {
			_, _ = fmt.Fprintf(out, "警告：%s\n", e.StaleWarning)
		}
		_, _ = fmt.Fprintf(out, "\n%s\n", e.Body)
	}
	return nil
}

func runWrite(args []string, store *memdir.Store, in io.Reader, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("smem write", flag.ContinueOnError)
	fs.SetOutput(errOut)
	scopeStr := fs.String("scope", "project", "project 或 global")
	typeStr := fs.String("type", "", "user、feedback、project 或 reference")
	name := fs.String("name", "", "记忆标题")
	desc := fs.String("desc", "", "单行触发条件说明")
	file := fs.String("file", "", "更新已有文件时的文件名")
	etag := fs.String("etag", "", "更新已有文件时的最新 etag")
	fromStdin := fs.Bool("stdin", false, "从标准输入读取正文")
	allowSecret := fs.Bool("allow-secret", false, "显式跳过密钥检测（仅限人工确认场景）")
	asJSON := fs.Bool("json", false, "输出 JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	sc, err := memdir.ParseScope(*scopeStr)
	if err != nil {
		return err
	}
	mt := memdir.MemoryType("")
	if strings.TrimSpace(*typeStr) != "" {
		mt, err = memdir.ParseMemoryType(*typeStr)
		if err != nil {
			return err
		}
	}

	var content string
	if *fromStdin {
		raw, err := io.ReadAll(in)
		if err != nil {
			return err
		}
		content = string(raw)
	} else {
		content = strings.Join(fs.Args(), " ")
	}
	if strings.TrimSpace(content) == "" {
		return errors.New("用法：smem write --type <type> --name \"标题\" [--desc \"触发说明\"] \"正文\"")
	}
	if strings.TrimSpace(*name) == "" {
		return errors.New("必须通过 --name 指定简短记忆标题")
	}

	res, err := store.Write(memdir.WriteRequest{
		Scope:       sc,
		Type:        mt,
		Name:        *name,
		Description: *desc,
		Content:     content,
		File:        *file,
		ETag:        *etag,
		AllowSecret: *allowSecret,
	})
	if err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(out, res)
	}
	_, _ = fmt.Fprintf(out, "状态：%s | %s/%s | etag=%s\n", res.Status, res.Scope, res.File, res.ETag)
	_, _ = fmt.Fprintf(out, "索引：%d 行 / %d 字节\n", res.IndexLines, res.IndexBytes)
	if res.IndexWarning != "" {
		_, _ = fmt.Fprintf(out, "警告：%s\n", res.IndexWarning)
	}
	_, _ = fmt.Fprintf(out, "\n现行正文：\n%s\n", res.ContentEcho)
	return nil
}

func runDelete(args []string, store *memdir.Store, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("smem delete", flag.ContinueOnError)
	fs.SetOutput(errOut)
	scopeStr := fs.String("scope", "project", "project 或 global")
	etag := fs.String("etag", "", "最新 etag（必填）")
	asJSON := fs.Bool("json", false, "输出 JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("用法：smem delete [--scope project|global] --etag <etag> <file>")
	}
	sc, err := memdir.ParseScope(*scopeStr)
	if err != nil {
		return err
	}
	file := fs.Arg(0)
	if err := store.Delete(memdir.DeleteRequest{Scope: sc, File: file, ETag: *etag}); err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(out, map[string]any{"deleted": true, "scope": sc, "file": file})
	}
	_, _ = fmt.Fprintf(out, "已删除 %s/%s 并同步更新 MEMORY.md。\n", sc, file)
	return nil
}

func runSearch(args []string, store *memdir.Store, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("smem search", flag.ContinueOnError)
	fs.SetOutput(errOut)
	scopeFilter := fs.String("scope", "all", "all、project 或 global")
	typeFilter := fs.String("type", "", "按四分类过滤")
	limit := fs.Int("limit", 8, "最大返回条数")
	asJSON := fs.Bool("json", false, "输出 JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	query := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if query == "" {
		return errors.New("用法：smem search [--scope all|project|global] <关键词...>")
	}
	hits, err := store.Search(memdir.SearchOptions{
		Query: query,
		Scope: *scopeFilter,
		Type:  *typeFilter,
		Limit: *limit,
	})
	if err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(out, hits)
	}
	if len(hits) == 0 {
		_, _ = fmt.Fprintln(out, "未命中相关记忆。")
		return nil
	}
	for _, h := range hits {
		_, _ = fmt.Fprintf(out, "- [%s] %s (%s, age=%s, etag=%s) — %s：%s\n",
			h.Scope, h.File, h.Type, h.Age, h.ETag, h.Name, h.Description)
		if h.Snippet != "" {
			_, _ = fmt.Fprintf(out, "  %s\n", h.Snippet)
		}
	}
	return nil
}

func runCheck(args []string, store *memdir.Store, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("smem check", flag.ContinueOnError)
	fs.SetOutput(errOut)
	scopeFilter := fs.String("scope", "all", "all、project 或 global")
	fix := fs.Bool("fix", false, "自动重建修复不同步的 MEMORY.md 索引")
	asJSON := fs.Bool("json", false, "输出 JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	report, err := store.Check(*scopeFilter, *fix)
	if err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(out, report)
	}
	if len(report.Issues) == 0 {
		_, _ = fmt.Fprintln(out, "检查通过：记忆目录与 MEMORY.md 索引状态健康。")
		return nil
	}
	for _, iss := range report.Issues {
		_, _ = fmt.Fprintf(out, "[%-7s] (%s) %s\n", iss.Severity, iss.Scope, iss.Message)
	}
	if report.Fixed {
		_, _ = fmt.Fprintln(out, "\n已自动重建修复 MEMORY.md 索引。")
	}
	return nil
}

func runShare(args []string, layout workspace.Layout, out, errOut io.Writer) error {
	if len(args) == 0 {
		return errors.New("用法：smem share status | push [--all] [--dry-run] [file...] | pull [--all] [--force] [--dry-run] [file...]")
	}
	sub := args[0]
	fs := flag.NewFlagSet("smem share "+sub, flag.ContinueOnError)
	fs.SetOutput(errOut)
	all := fs.Bool("all", false, "同步全部条目")
	force := fs.Bool("force", false, "拉取时覆盖较新的本机条目")
	dryRun := fs.Bool("dry-run", false, "仅预览，不写入")
	asJSON := fs.Bool("json", false, "输出 JSON")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	switch sub {
	case "status":
		st, err := adapters.ShareStatus(layout)
		if err != nil {
			return err
		}
		if *asJSON {
			return writeJSON(out, st)
		}
		_, _ = fmt.Fprintf(out, "本机项目记忆：%s\n仓库共享目录：%s\n", st.LocalDir, st.RepoDir)
		if st.SharedMode {
			_, _ = fmt.Fprintln(out, "当前仓库处于 --shared 直连模式，无需手动 push/pull。")
			return nil
		}
		if len(st.Items) == 0 {
			_, _ = fmt.Fprintln(out, "（两侧均无项目记忆条目）")
			return nil
		}
		for _, it := range st.Items {
			_, _ = fmt.Fprintf(out, "- %-22s %s\n", it.Status, it.File)
		}
		return nil
	case "push":
		res, err := adapters.SharePush(layout, fs.Args(), *all, *dryRun)
		if err != nil {
			return err
		}
		if *asJSON {
			return writeJSON(out, res)
		}
		label := "已同步到项目仓库 .smem/memory/"
		if *dryRun {
			label = "预览拟同步到项目仓库 .smem/memory/"
		}
		_, _ = fmt.Fprintf(out, "%s（%d 项）：%s\n", label, len(res.Copied), strings.Join(res.Copied, ", "))
		if len(res.Skipped) > 0 {
			_, _ = fmt.Fprintf(out, "跳过：%s\n", strings.Join(res.Skipped, ", "))
		}
		return nil
	case "pull":
		res, err := adapters.SharePull(layout, fs.Args(), *all, *force, *dryRun)
		if err != nil {
			return err
		}
		if *asJSON {
			return writeJSON(out, res)
		}
		label := "已从项目仓库同步至本机项目记忆"
		if *dryRun {
			label = "预览拟从项目仓库拉取至本机"
		}
		_, _ = fmt.Fprintf(out, "%s（%d 项）：%s\n", label, len(res.Copied), strings.Join(res.Copied, ", "))
		if len(res.Skipped) > 0 {
			_, _ = fmt.Fprintf(out, "跳过：%s\n", strings.Join(res.Skipped, ", "))
		}
		return nil
	default:
		return fmt.Errorf("未知 share 子命令 %q（支持 status、push、pull）", sub)
	}
}

func runImport(args []string, store *memdir.Store, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("smem import", flag.ContinueOnError)
	fs.SetOutput(errOut)
	from := fs.String("from", "", "来源：claude、codex、cline 或 dir")
	path := fs.String("path", "", "可选显式目录路径")
	scopeStr := fs.String("scope", "project", "导入目标作用域：project 或 global")
	dryRun := fs.Bool("dry-run", false, "仅预览候选导入文件，不写入")
	asJSON := fs.Bool("json", false, "输出 JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	sc, err := memdir.ParseScope(*scopeStr)
	if err != nil {
		return err
	}
	res, err := adapters.RunImport(store, adapters.ImportOptions{
		From:   *from,
		Path:   *path,
		Scope:  sc,
		DryRun: *dryRun,
	})
	if err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(out, res)
	}
	_, _ = fmt.Fprintf(out, "导入来源：%s (%s) -> %s\n", res.Source, res.SourceDir, res.Scope)
	_, _ = fmt.Fprintf(out, "结果：成功/候选 %d 项，跳过 %d 项\n", res.Imported, res.Skipped)
	for _, it := range res.Items {
		line := fmt.Sprintf("- [%-17s] %s", it.Status, it.Name)
		if it.TargetFile != "" {
			line += " -> " + it.TargetFile
		}
		if it.Reason != "" {
			line += " (" + it.Reason + ")"
		}
		_, _ = fmt.Fprintln(out, line)
	}
	return nil
}

func runSync(args []string, layout workspace.Layout, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("smem sync", flag.ContinueOnError)
	fs.SetOutput(errOut)
	remote := fs.String("remote", "", "配置或更新 ~/.smem/global 的私有远端 Git URL")
	if err := fs.Parse(args); err != nil {
		return err
	}
	msg, err := adapters.SyncGlobal(layout, *remote)
	if msg != "" {
		_, _ = fmt.Fprintln(out, msg)
	}
	return err
}

func runDream(args []string, store *memdir.Store, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("smem dream", flag.ContinueOnError)
	fs.SetOutput(errOut)
	scopeFilter := fs.String("scope", "all", "all、project 或 global")
	if err := fs.Parse(args); err != nil {
		return err
	}
	report, _ := store.Check(*scopeFilter, false)
	_, _ = fmt.Fprintln(out, mcp.DreamPromptText)
	_, _ = fmt.Fprintf(out, "\n---\n[当前体检摘要] 范围=%s，发现 %d 项可关注问题（可先运行 `smem check --fix` 修复基础索引问题）。\n", *scopeFilter, len(report.Issues))
	return nil
}

func runHook(args []string, store *memdir.Store, in io.Reader, out, errOut io.Writer) error {
	if len(args) < 2 {
		// Fail-open for hook callers.
		_, _ = fmt.Fprintln(out, "{}")
		return nil
	}
	agent := args[0]
	event := args[1]
	fs := flag.NewFlagSet("smem hook", flag.ContinueOnError)
	fs.SetOutput(errOut)
	claudeMode := fs.String("claude-mode", "native", "native 或 managed")
	_ = fs.Parse(args[2:])

	if err := hooks.Handle(store, hooks.Options{
		Agent:      agent,
		Event:      event,
		ClaudeMode: *claudeMode,
	}, in, out); err != nil {
		_, _ = fmt.Fprintf(errOut, "smem hook warning: %v\n", err)
		_, _ = fmt.Fprintln(out, "{}")
	}
	return nil
}

func resolveDefaultBin() string {
	if _, err := exec.LookPath("smem"); err == nil {
		return "smem"
	}
	if exe, err := os.Executable(); err == nil && exe != "" {
		return exe
	}
	return "smem"
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
