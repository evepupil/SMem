package memdir

import (
	"fmt"
	"time"
)

// FormatAge converts a file modification time into a human/LLM-friendly relative age
// and attaches a Claude-Code-style staleness verification warning when age >= 1 day.
func FormatAge(modTime, now time.Time) (label string, days int, warning string) {
	if modTime.IsZero() {
		return "unknown", 0, ""
	}
	diff := now.Sub(modTime)
	if diff < 0 {
		diff = 0
	}
	days = int(diff.Hours() / 24)
	switch {
	case days == 0:
		return "today", 0, ""
	case days == 1:
		return "yesterday", 1, "此记忆更新于昨天（1 天前），涉及代码、路径或命令行为前请先核对当前源码。"
	default:
		label = fmt.Sprintf("%d days ago", days)
		warning = fmt.Sprintf("此记忆更新于 %d 天前，属于历史时点观察；涉及代码、文件路径、符号或命令行为前，请先核对当前仓库事实。", days)
		return label, days, warning
	}
}
