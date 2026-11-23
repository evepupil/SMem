package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"smem/internal/adapters"
	"smem/internal/memdir"
	"smem/internal/workspace"
)

type setupImportOptions struct {
	DryRun   bool
	DoImport bool
	NoImport bool
}

func isInteractiveInput(in io.Reader) bool {
	f, ok := in.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}

func executeSetupImportFlow(layout workspace.Layout, candidates []adapters.ImportCandidate, opts setupImportOptions, in io.Reader, out io.Writer) ([]adapters.ImportResult, error) {
	if opts.NoImport {
		return nil, nil
	}

	if opts.DryRun {
		_, _ = fmt.Fprintln(out, "\n--- 第 2 步预览：默认记忆目录扫描结果 ---")
		if len(candidates) == 0 {
			_, _ = fmt.Fprintln(out, "未在默认记忆目录中发现待导入条目。")
			renderManualBindImportHint(out)
			return nil, nil
		}
		renderImportChecklist(candidates, out, false)
		renderManualBindImportHint(out)
		return nil, nil
	}

	store := memdir.NewStore(layout)

	if opts.DoImport {
		if len(candidates) == 0 {
			_, _ = fmt.Fprintln(out, "\n--- 第 2 步：导入已有记忆 ---")
			_, _ = fmt.Fprintln(out, "未在默认记忆目录中发现待导入条目。")
			renderManualBindImportHint(out)
			return nil, nil
		}
		results, err := importSelectedCandidates(store, candidates, out)
		renderManualBindImportHint(out)
		return results, err
	}

	if !isInteractiveInput(in) {
		_, _ = fmt.Fprintln(out, "\n--- 第 2 步：默认记忆目录扫描结果 ---")
		if len(candidates) == 0 {
			_, _ = fmt.Fprintln(out, "未在默认记忆目录中发现待导入条目。")
			renderManualBindImportHint(out)
			return nil, nil
		}
		renderImportChecklist(candidates, out, false)
		_, _ = fmt.Fprintln(out, "提示：当前为非交互终端；可运行 `smem setup --import` 一键导入以上全部目录，或在交互终端运行 `smem setup` 勾选导入。")
		renderManualBindImportHint(out)
		return nil, nil
	}

	results, err := runInteractiveImportWizard(candidates, store, in, out)
	if err != nil {
		return nil, err
	}
	return results, nil
}

func runInteractiveImportWizard(candidates []adapters.ImportCandidate, store *memdir.Store, in io.Reader, out io.Writer) ([]adapters.ImportResult, error) {
	_, _ = fmt.Fprintln(out, "\n--- 第 2 步：扫描默认目录并引导导入已有记忆 ---")
	if len(candidates) == 0 {
		_, _ = fmt.Fprintln(out, "未在默认记忆目录中发现待导入条目。")
		renderManualBindImportHint(out)
		return nil, nil
	}

	// Copy slice so interactive toggles never mutate caller's snapshot unexpectedly.
	items := make([]adapters.ImportCandidate, len(candidates))
	copy(items, candidates)

	reader := bufio.NewReader(in)
	for {
		renderImportChecklist(items, out, true)
		renderManualBindImportHint(out)
		_, _ = fmt.Fprint(out, "\n操作：直接回车=一键导入已勾选项 | 输入编号切换勾选（如 2 或 1,3）| a=全选 | n=全不选 | q=跳过\n> ")

		rawLine, readErr := reader.ReadString('\n')
		line := strings.TrimSpace(rawLine)

		if readErr != nil && line == "" {
			_, _ = fmt.Fprintln(out, "已跳过导入。")
			return nil, nil
		}

		if line == "" {
			break
		}
		lower := strings.ToLower(line)
		switch lower {
		case "q", "quit", "exit", "skip":
			_, _ = fmt.Fprintln(out, "已跳过导入。")
			return nil, nil
		case "a", "all":
			for i := range items {
				items[i].Selected = true
			}
			continue
		case "n", "none":
			for i := range items {
				items[i].Selected = false
			}
			continue
		}

		indices, parseErr := parseSelectionIndices(line, len(items))
		if parseErr != nil {
			_, _ = fmt.Fprintf(out, "输入提示：%v\n\n", parseErr)
			continue
		}
		for _, idx := range indices {
			items[idx].Selected = !items[idx].Selected
		}
		_, _ = fmt.Fprintln(out)
	}

	var selected []adapters.ImportCandidate
	for _, it := range items {
		if it.Selected {
			selected = append(selected, it)
		}
	}
	if len(selected) == 0 {
		_, _ = fmt.Fprintln(out, "未勾选任何记忆来源，已跳过导入。")
		return nil, nil
	}

	return importSelectedCandidates(store, selected, out)
}

func renderImportChecklist(items []adapters.ImportCandidate, out io.Writer, interactive bool) {
	if interactive {
		_, _ = fmt.Fprintln(out, "发现以下原有记忆可供导入（默认全选 `[x]`，直接回车即可一键导入）：")
	} else {
		_, _ = fmt.Fprintln(out, "扫描到以下默认目录包含尚未迁入的原有记忆：")
	}
	for i, it := range items {
		mark := "[ ]"
		if it.Selected {
			mark = "[x]"
		}
		_, _ = fmt.Fprintf(out, "  %s %d. %-28s %3d 条 -> %-7s (%s)\n",
			mark, i+1, it.Label, it.Count, it.DefaultScope, it.Dir)
	}
}

func renderManualBindImportHint(out io.Writer) {
	_, _ = fmt.Fprintln(out, "\n没找到你想迁入的历史记忆（例如使用多账号 CLAUDE_CONFIG_DIR / reclaude 或自定义目录）？")
	_, _ = fmt.Fprintln(out, "  · 共用原目录（直接挂载、不建副本）：smem bind \"<原有记忆目录>\"")
	_, _ = fmt.Fprintln(out, "  · 复制合并进 SMem（支持任意目录）：   smem import --from dir --path \"<原有记忆目录>\" --scope project")
}

func importSelectedCandidates(store *memdir.Store, selected []adapters.ImportCandidate, out io.Writer) ([]adapters.ImportResult, error) {
	_, _ = fmt.Fprintf(out, "\n开始导入已勾选的 %d 项来源...\n", len(selected))
	var results []adapters.ImportResult
	totalImported := 0
	totalSkipped := 0
	usedScopes := make(map[memdir.Scope]bool)

	for _, c := range selected {
		res, err := adapters.RunImport(store, adapters.ImportOptions{
			From:  c.Source,
			Path:  c.Dir,
			Scope: c.DefaultScope,
		})
		if err != nil {
			return results, fmt.Errorf("导入 %s（%s）失败：%w", c.Label, c.Dir, err)
		}
		results = append(results, res)
		totalImported += res.Imported
		totalSkipped += res.Skipped
		usedScopes[c.DefaultScope] = true

		secretCount := countSkippedStatus(res.Items, "skipped_secret")
		extra := ""
		if secretCount > 0 {
			extra = fmt.Sprintf("（含 %d 条疑似密钥已自动拦截）", secretCount)
		}
		_, _ = fmt.Fprintf(out, "  - %-28s [%s]：新导入 %d 条，跳过 %d 条%s\n",
			c.Label, c.DefaultScope, res.Imported, res.Skipped, extra)
	}

	_, _ = fmt.Fprintf(out, "导入完成：共新增 %d 条，跳过（重复/冲突/敏感/空文件）%d 条。\n", totalImported, totalSkipped)

	for sc := range usedScopes {
		idxRes, err := store.Index(string(sc))
		if err != nil {
			continue
		}
		var idx *memdir.ScopeIndex
		if sc == memdir.ScopeProject {
			idx = idxRes.Project
		} else {
			idx = idxRes.Global
		}
		if idx != nil && idx.Warning != "" {
			_, _ = fmt.Fprintf(out, "  注意 [%s]：%s\n", sc, idx.Warning)
		}
	}
	return results, nil
}

func countSkippedStatus(items []adapters.ImportedItem, status string) int {
	n := 0
	for _, it := range items {
		if it.Status == status {
			n++
		}
	}
	return n
}

func parseSelectionIndices(raw string, max int) ([]int, error) {
	normalized := strings.NewReplacer("，", " ", ",", " ", "、", " ", ";", " ", "；", " ").Replace(raw)
	fields := strings.Fields(normalized)
	if len(fields) == 0 {
		return nil, fmt.Errorf("请输入 1-%d 的编号、a、n、q 或直接回车", max)
	}

	seen := make(map[int]bool)
	var out []int
	addIdx := func(oneBased int) error {
		if oneBased < 1 || oneBased > max {
			return fmt.Errorf("编号 %d 超出范围（可选 1-%d）", oneBased, max)
		}
		zeroBased := oneBased - 1
		if !seen[zeroBased] {
			seen[zeroBased] = true
			out = append(out, zeroBased)
		}
		return nil
	}

	for _, tok := range fields {
		if startStr, endStr, hasDash := strings.Cut(tok, "-"); hasDash {
			start, err1 := strconv.Atoi(strings.TrimSpace(startStr))
			end, err2 := strconv.Atoi(strings.TrimSpace(endStr))
			if err1 != nil || err2 != nil || start > end {
				return nil, fmt.Errorf("无效区间 %q（请输入如 1-3 或单个数字 1-%d）", tok, max)
			}
			for v := start; v <= end; v++ {
				if err := addIdx(v); err != nil {
					return nil, err
				}
			}
			continue
		}
		num, err := strconv.Atoi(tok)
		if err != nil {
			return nil, fmt.Errorf("无效输入 %q（请输入 1-%d 的编号、a、n、q 或直接回车）", tok, max)
		}
		if err := addIdx(num); err != nil {
			return nil, err
		}
	}
	return out, nil
}
