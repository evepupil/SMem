package memory

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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
	terms := strings.Fields(strings.ToLower(query))
	if len(terms) == 0 {
		return nil, errors.New("请输入搜索词，例如 mem search 部署 容器")
	}
	var hits []Hit
	for _, global := range []bool{false, true} {
		store, err := layout.Store(global)
		if err != nil && !global {
			continue
		}
		if err := store.Check(); err != nil {
			return nil, err
		}
		entries, err := os.ReadDir(store.Path())
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		scope := "项目"
		if global {
			scope = "全局"
		}
		for _, entry := range entries {
			if !entry.Type().IsRegular() || !strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
				continue
			}
			path := filepath.Join(store.Path(), entry.Name())
			lines, err := searchFile(path, scope, terms, 5-len(hits))
			if err != nil {
				return nil, fmt.Errorf("读取 %s：%w", path, err)
			}
			hits = append(hits, lines...)
			if len(hits) == 5 {
				return hits, nil
			}
		}
	}
	return hits, nil
}

func searchFile(path, scope string, terms []string, limit int) ([]Hit, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var hits []Hit
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 4*1024*1024)
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		lower := strings.ToLower(text)
		if slices.ContainsFunc(terms, func(term string) bool { return !strings.Contains(lower, term) }) {
			continue
		}
		preview := []rune(strings.Map(func(char rune) rune {
			if unicode.IsControl(char) {
				return ' '
			}
			return char
		}, text))
		if len(preview) > 220 {
			preview = append(preview[:220], '…')
		}
		hits = append(hits, Hit{scope, path, line, string(preview)})
		if len(hits) == limit {
			break
		}
	}
	return hits, scanner.Err()
}
