package memory

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"smem/internal/workspace"
)

type Record struct {
	Scope   string
	Path    string
	Text    string
	Ref     string
	Line    int
	Always  bool
	Managed bool
}

const alwaysMarker = "<!-- smem:always -->"

func readRecords(layout workspace.Layout, scope string) ([]Record, error) {
	if scope != "all" && scope != "project" && scope != "global" {
		return nil, fmt.Errorf("无效的记忆范围：%q", scope)
	}

	scopes := []string{scope}
	if scope == "all" {
		scopes = []string{"project", "global"}
	}

	var records []Record
	for _, current := range scopes {
		if current == "project" && (layout.Project == "" || layout.Project == layout.Home) {
			continue
		}
		found, err := readScopeRecords(layout, current)
		if err != nil {
			return nil, err
		}
		records = append(records, found...)
	}

	sort.Slice(records, func(i, j int) bool {
		if records[i].Scope != records[j].Scope {
			return records[i].Scope == "project"
		}
		if records[i].Path != records[j].Path {
			return records[i].Path < records[j].Path
		}
		if records[i].Line != records[j].Line {
			return records[i].Line < records[j].Line
		}
		return records[i].Ref < records[j].Ref
	})
	return records, nil
}

func readScopeRecords(layout workspace.Layout, scope string) ([]Record, error) {
	store, err := layout.Store(scope == "global")
	if err != nil {
		return nil, err
	}
	if err := store.Check(); err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(store.Path())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取记忆目录 %s：%w", store.Path(), err)
	}

	var records []Record
	for _, entry := range entries {
		name := entry.Name()
		if !isMarkdownName(name) {
			continue
		}

		path := filepath.Join(store.Path(), name)
		info, err := os.Lstat(path)
		if err != nil {
			return nil, fmt.Errorf("读取记忆 %s：%w", path, err)
		}
		if !info.Mode().IsRegular() {
			continue
		}

		target := workspace.At(store.Base, filepath.Join(store.Rel, name))
		if err := target.Check(); err != nil {
			return nil, fmt.Errorf("检查记忆路径 %s：%w", path, err)
		}
		data, regular, err := readRegularFile(path, info)
		if err != nil {
			return nil, fmt.Errorf("读取记忆 %s：%w", path, err)
		}
		if !regular {
			continue
		}

		found, err := parseRecords(layout, scope, path, data)
		if err != nil {
			return nil, fmt.Errorf("解析记忆 %s：%w", path, err)
		}
		records = append(records, found...)
	}
	return records, nil
}

func readRegularFile(path string, before os.FileInfo) ([]byte, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}

	opened, err := file.Stat()
	if err != nil {
		return nil, false, errors.Join(err, file.Close())
	}
	current, err := os.Lstat(path)
	if err != nil {
		return nil, false, errors.Join(err, file.Close())
	}
	if !opened.Mode().IsRegular() || !current.Mode().IsRegular() ||
		!os.SameFile(before, opened) || !os.SameFile(opened, current) {
		if err := file.Close(); err != nil {
			return nil, false, err
		}
		return nil, false, nil
	}

	data, readErr := io.ReadAll(file)
	closeErr := file.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, false, err
	}
	return data, true, nil
}

func parseRecords(layout workspace.Layout, scope, path string, data []byte) ([]Record, error) {
	if scope != "project" && scope != "global" {
		return nil, fmt.Errorf("无效的记忆范围：%q", scope)
	}
	store, err := layout.Store(scope == "global")
	if err != nil {
		return nil, err
	}
	if err := store.Check(); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(path) {
		return nil, errors.New("记忆路径必须是绝对路径")
	}
	path = filepath.Clean(path)
	if filepath.Dir(path) != filepath.Clean(store.Path()) || !isMarkdownName(filepath.Base(path)) {
		return nil, errors.New("记忆路径不在所选范围的记忆目录中")
	}
	target := workspace.At(store.Base, filepath.Join(store.Rel, filepath.Base(path)))
	if err := target.Check(); err != nil {
		return nil, err
	}

	root := filepath.Clean(store.Base)
	fileName := filepath.Base(path)
	version := fileVersion(data)
	if isManagedName(fileName) {
		return parseManagedRecord(scope, path, root, store.Path(), fileName, data, version), nil
	}
	return parseTopicRecords(scope, path, root, store.Path(), fileName, data, version), nil
}

func parseManagedRecord(scope, path, root, memoryRoot, fileName string, data []byte, version [32]byte) []Record {
	content := data
	if bytes.HasPrefix(content, []byte{0xef, 0xbb, 0xbf}) {
		content = content[3:]
	}

	line := 1
	lines := splitSourceLines(content)
	always := len(lines) > 0 && bytes.Equal(lines[0].text, []byte(alwaysMarker))
	if always {
		if len(lines) == 1 {
			content = nil
		} else {
			content = content[lines[0].next:]
		}
		line = 2
	}

	text := string(content)
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil
	}
	leading := len(text) - len(strings.TrimLeftFunc(text, unicode.IsSpace))
	line += countSourceLines(text[:leading])
	return []Record{{
		Scope: scope, Path: path, Text: trimmed,
		Ref:  makeReference(scope, root, memoryRoot, fileName, line, version),
		Line: line, Always: always, Managed: true,
	}}
}

func parseTopicRecords(scope, path, root, memoryRoot, fileName string, data []byte, version [32]byte) []Record {
	source := splitSourceLines(data)
	lines := make([]string, len(source))
	for i, line := range source {
		text := line.text
		if i == 0 && bytes.HasPrefix(text, []byte{0xef, 0xbb, 0xbf}) {
			text = text[3:]
		}
		lines[i] = string(text)
	}

	var records []Record
	for i, text := range lines {
		if strings.TrimSpace(text) == "" || isMarkdownHeading(lines, i) {
			continue
		}
		line := i + 1
		records = append(records, Record{
			Scope: scope, Path: path, Text: text,
			Ref:  makeReference(scope, root, memoryRoot, fileName, line, version),
			Line: line,
		})
	}
	return records
}

type sourceLine struct {
	text []byte
	next int
}

func splitSourceLines(data []byte) []sourceLine {
	lines := make([]sourceLine, 0, bytes.Count(data, []byte{'\n'})+1)
	start := 0
	for i := 0; i < len(data); i++ {
		if data[i] != '\n' && data[i] != '\r' {
			continue
		}
		end := i
		if data[i] == '\r' && i+1 < len(data) && data[i+1] == '\n' {
			i++
		}
		lines = append(lines, sourceLine{text: data[start:end], next: i + 1})
		start = i + 1
	}
	lines = append(lines, sourceLine{text: data[start:], next: len(data)})
	return lines
}

func countSourceLines(text string) int {
	count := 0
	for i := 0; i < len(text); i++ {
		if text[i] == '\r' {
			count++
			if i+1 < len(text) && text[i+1] == '\n' {
				i++
			}
		} else if text[i] == '\n' {
			count++
		}
	}
	return count
}

func isMarkdownHeading(lines []string, index int) bool {
	text := strings.TrimSpace(lines[index])
	if isATXHeading(text) {
		return true
	}
	if isSetextUnderline(text) {
		return index > 0 && strings.TrimSpace(lines[index-1]) != ""
	}
	return index+1 < len(lines) && text != "" && isSetextUnderline(strings.TrimSpace(lines[index+1]))
}

func isATXHeading(text string) bool {
	count := 0
	for count < len(text) && text[count] == '#' {
		count++
	}
	return count > 0 && count <= 6 && (count == len(text) || text[count] == ' ' || text[count] == '\t')
}

func isSetextUnderline(text string) bool {
	if text == "" || (text[0] != '=' && text[0] != '-') {
		return false
	}
	marker := text[0]
	for i := 1; i < len(text); i++ {
		if text[i] != marker && text[i] != ' ' && text[i] != '\t' {
			return false
		}
	}
	return true
}

func isMarkdownName(name string) bool {
	return strings.EqualFold(filepath.Ext(name), ".md")
}

func isManagedName(name string) bool {
	if !isMarkdownName(name) || !strings.HasPrefix(name, "note-") {
		return false
	}
	stem := name[:len(name)-len(filepath.Ext(name))]
	tail := strings.TrimPrefix(stem, "note-")
	if len(tail) == 20 && isHexString(tail) {
		return true
	}
	separator := strings.LastIndexByte(tail, '-')
	return separator > 0 && len(tail)-separator-1 == 20 && isHexString(tail[separator+1:])
}

func isHexString(value string) bool {
	for i := 0; i < len(value); i++ {
		char := value[i]
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') || (char >= 'A' && char <= 'F')) {
			return false
		}
	}
	return true
}
