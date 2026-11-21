package memory

import (
	"errors"
	"strings"
	"unicode"

	"smem/internal/workspace"
)

type Hit struct {
	Scope string
	Path  string
	Line  int
	Text  string
}

func Search(layout workspace.Layout, query string) ([]Hit, error) {
	records, err := Related(layout, query)
	if err != nil {
		return nil, err
	}
	hits := make([]Hit, 0, len(records))
	for _, record := range records {
		scope := "项目"
		if record.Scope == "global" {
			scope = "全局"
		}
		hits = append(hits, Hit{
			Scope: scope,
			Path:  record.Path,
			Line:  record.Line,
			Text:  preview(record.Text),
		})
	}
	return hits, nil
}

func preview(text string) string {
	text = strings.TrimSpace(text)
	text = strings.Map(func(char rune) rune {
		if unicode.IsControl(char) {
			return ' '
		}
		return char
	}, text)
	runes := []rune(text)
	if len(runes) > 220 {
		return string(runes[:220]) + "…"
	}
	return strings.TrimSpace(string(runes))
}

func emptyQueryError() error {
	return errors.New("请输入搜索词，例如 mem search 部署 容器")
}
