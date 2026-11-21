package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"unicode"

	"smem/internal/memory"
	"smem/internal/workspace"
)

func runAdd(args []string, layout workspace.Layout, out, errOut io.Writer) error {
	flags := flag.NewFlagSet("mem add", flag.ContinueOnError)
	flags.SetOutput(errOut)
	global := flags.Bool("global", false, "跨项目的个人偏好")
	always := flags.Bool("always", false, "仅用户明确要求每次开工提醒时使用")
	if err := flags.Parse(args); err != nil {
		return err
	}
	path, created, err := memory.AddNote(layout, *global, *always, strings.Join(flags.Args(), " "))
	if err != nil {
		return err
	}
	if created {
		fmt.Fprintf(out, "已记下：%s\n", path)
	} else if *always {
		fmt.Fprintf(out, "已存在（已确保开工常用）：%s\n", path)
	} else {
		fmt.Fprintf(out, "已存在：%s\n", path)
	}
	return nil
}

func runSearch(args []string, layout workspace.Layout, out io.Writer) error {
	start := len(args) > 0 && args[0] == "--start"
	if start {
		pinned, more, matches, err := memory.Start(layout, strings.Join(args[1:], " "))
		if err != nil {
			return err
		}
		query := strings.TrimSpace(strings.Join(args[1:], " "))
		if len(pinned) == 0 && len(matches) == 0 {
			fmt.Fprintln(out, "没有找到相关记忆。")
			return nil
		}
		if len(pinned) > 0 {
			fmt.Fprintln(out, "开工常用记忆：")
			for _, record := range pinned {
				writeRecord(out, record)
			}
		}
		if more {
			fmt.Fprintln(out, "常用记忆超出 3 条；用 mem list 浏览全部记忆。")
		}
		if query != "" {
			if len(matches) == 0 {
				fmt.Fprintln(out, "没有找到相关记忆。")
			} else {
				fmt.Fprintln(out, "相关记忆：")
				for _, record := range matches {
					writeRecord(out, record)
				}
				if len(matches) == 5 {
					fmt.Fprintln(out, "相关记忆最多显示 5 条；调整关键词继续搜索。")
				}
			}
		}
		return nil
	}

	records, err := memory.Related(layout, strings.Join(args, " "))
	if err != nil {
		return err
	}
	if len(records) == 0 {
		fmt.Fprintln(out, "没有找到相关记忆。")
		return nil
	}
	for _, record := range records {
		writeRecord(out, record)
	}
	return nil
}

func runList(args []string, layout workspace.Layout, out, errOut io.Writer) error {
	flags := flag.NewFlagSet("mem list", flag.ContinueOnError)
	flags.SetOutput(errOut)
	scope := flags.String("scope", "all", "记忆范围：all、project 或 global")
	offset := flags.Int("offset", 0, "从第几条开始")
	limit := flags.Int("limit", 10, "最多显示条数（1 到 100）")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("用法：mem list [--scope all|project|global] [--offset 0] [--limit 10]")
	}

	records, more, err := memory.List(layout, *scope, *offset, *limit)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "记忆范围：%s\n", scopeLabel(*scope))
	if len(records) == 0 {
		fmt.Fprintln(out, "没有找到相关记忆。")
		return nil
	}
	for _, record := range records {
		writeRecord(out, record)
	}
	if more {
		fmt.Fprintf(out, "还有更多记忆；用 --offset %d 继续。\n", *offset+len(records))
	}
	return nil
}

func runShow(args []string, layout workspace.Layout, out io.Writer) error {
	if len(args) != 1 {
		return errors.New("用法：mem show REF")
	}
	record, err := memory.Show(layout, args[0])
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "[%s] 范围：%s 路径：%s 行号：%d\n引用：%s\n原文：\n%s\n",
		scopeName(record.Scope), record.Scope, record.Path, record.Line, record.Ref, record.Text)
	return nil
}

func runUpdate(args []string, layout workspace.Layout, out io.Writer) error {
	if len(args) != 2 {
		return errors.New("用法：mem update REF \"新结论\"")
	}
	if err := memory.Update(layout, args[0], args[1]); err != nil {
		return err
	}
	fmt.Fprintf(out, "已更新，现行内容：\n%s\n继续操作请重新查询引用。\n", strings.TrimSpace(args[1]))
	return nil
}

func runForget(args []string, layout workspace.Layout, out io.Writer) error {
	if len(args) != 1 {
		return errors.New("用法：mem forget REF")
	}
	if err := memory.Forget(layout, args[0]); err != nil {
		return err
	}
	fmt.Fprintf(out, "已忘记：%s\n", args[0])
	return nil
}

func writeRecord(out io.Writer, record memory.Record) {
	fmt.Fprintf(out, "[%s] 范围：%s 路径：%s 行号：%d\n%s\n引用：%s\n",
		scopeName(record.Scope), record.Scope, record.Path, record.Line, excerpt(record.Text), record.Ref)
}

func scopeName(scope string) string {
	if scope == "global" {
		return "全局"
	}
	return "项目"
}

func scopeLabel(scope string) string {
	switch scope {
	case "project":
		return "项目（project）"
	case "global":
		return "全局（global）"
	default:
		return "全部（all）"
	}
}

func excerpt(text string) string {
	text = strings.Map(func(char rune) rune {
		if unicode.IsControl(char) || char == '\u2028' || char == '\u2029' {
			return ' '
		}
		return char
	}, strings.TrimSpace(text))
	runes := []rune(strings.TrimSpace(text))
	if len(runes) > 220 {
		return string(runes[:220]) + "…"
	}
	return string(runes)
}
