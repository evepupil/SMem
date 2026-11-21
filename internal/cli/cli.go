package cli

import (
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"io"

	"smem/internal/integration"
	"smem/internal/workspace"
)

//go:embed migrate.txt
var migration string

const usage = `smem：所有本地编程助手共用一份记忆
  mem init [--dry-run] [--agents claude,codex,cursor,gemini,copilot,opencode,windsurf,devin]
  mem list [--scope all|project|global] [--offset 0] [--limit 10]
  mem show REF
  mem update REF "新结论"
  mem forget REF
  mem search [--start] 关键词 [关键词...]
  mem add [--always] [--global] "一条持久结论"
  mem migrate  打印可贴给旧助手的迁移提示词，不读取旧记忆
`

func Run(args []string, layout workspace.Layout, out, errOut io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(out, usage)
		return nil
	}
	switch args[0] {
	case "help", "-h", "--help":
		fmt.Fprint(out, usage)
		return nil
	case "init":
		flags := flag.NewFlagSet("mem init", flag.ContinueOnError)
		flags.SetOutput(errOut)
		dryRun := flags.Bool("dry-run", false, "仅预览，不写文件")
		agents := flags.String("agents", "auto", "默认识别本机工具；也可传逗号分隔的工具名")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 {
			return errors.New("用法：mem init [--dry-run] [--agents 工具列表]")
		}
		return integration.Init(layout, integration.Options{Agents: *agents, DryRun: *dryRun}, out)
	case "add":
		return runAdd(args[1:], layout, out, errOut)
	case "search":
		return runSearch(args[1:], layout, out)
	case "list":
		return runList(args[1:], layout, out, errOut)
	case "show":
		return runShow(args[1:], layout, out)
	case "update":
		return runUpdate(args[1:], layout, out)
	case "forget":
		return runForget(args[1:], layout, out)
	case "migrate":
		if len(args) != 1 {
			return errors.New("用法：mem migrate（只打印提示词）")
		}
		fmt.Fprint(out, migration)
		return nil
	default:
		return fmt.Errorf("未知命令 %q；运行 mem --help 查看用法", args[0])
	}
}
