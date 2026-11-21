package memory

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"smem/internal/workspace"
)

func Update(layout workspace.Layout, ref, text string) (err error) {
	text, err = normalizeNoteText(text)
	if err != nil {
		return err
	}
	record, store, path, err := resolveMutationReference(layout, ref)
	if err != nil {
		return err
	}
	if !record.Managed {
		text, err = normalizeTopicText(text)
		if err != nil {
			return err
		}
	}

	unlock, err := acquireMutationLock(store)
	if err != nil {
		return err
	}
	defer func() {
		if unlockErr := unlock(); unlockErr != nil {
			err = errors.Join(err, unlockErr)
		}
	}()

	_, path, data, info, records, err := readCurrentRecord(layout, record)
	if err != nil {
		return err
	}
	if record.Managed {
		if len(records) != 1 {
			return fmt.Errorf("管理记忆文件包含意外的多条记录，未修改：%s", path)
		}
		return updateManagedRecord(layout, record, path, data, info.Mode(), text)
	}
	updated, err := replaceTopicLine(data, record, text)
	if err != nil {
		return err
	}
	if bytes.Equal(updated, data) {
		return nil
	}
	return replaceExistingFile(path, updated, info.Mode())
}

func Forget(layout workspace.Layout, ref string) (err error) {
	record, store, _, err := resolveMutationReference(layout, ref)
	if err != nil {
		return err
	}
	unlock, err := acquireMutationLock(store)
	if err != nil {
		return err
	}
	defer func() {
		if unlockErr := unlock(); unlockErr != nil {
			err = errors.Join(err, unlockErr)
		}
	}()

	_, path, data, info, records, err := readCurrentRecord(layout, record)
	if err != nil {
		return err
	}
	if record.Managed {
		if len(records) != 1 {
			return fmt.Errorf("管理记忆文件包含意外的多条记录，未删除：%s", path)
		}
		return os.Remove(path)
	}
	updated, err := removeTopicLine(data, record)
	if err != nil {
		return err
	}
	if len(updated) == 0 {
		return os.Remove(path)
	}
	return replaceExistingFile(path, updated, info.Mode())
}

func resolveMutationReference(layout workspace.Layout, ref string) (Record, workspace.Target, string, error) {
	if ref == "" || len(ref) > 4096 || !utf8.ValidString(ref) || strings.ContainsFunc(ref, unicode.IsControl) {
		return Record{}, workspace.Target{}, "", errors.New("引用格式无效")
	}
	record, err := Show(layout, ref)
	if err != nil {
		return Record{}, workspace.Target{}, "", err
	}
	store, path, err := safeRecordPath(layout, record)
	if err != nil {
		return Record{}, workspace.Target{}, "", err
	}
	return record, store, path, nil
}

func safeRecordPath(layout workspace.Layout, record Record) (workspace.Target, string, error) {
	var global bool
	switch record.Scope {
	case "project":
	case "global":
		global = true
	default:
		return workspace.Target{}, "", fmt.Errorf("记忆范围无效：%q", record.Scope)
	}
	store, err := layout.Store(global)
	if err != nil {
		return workspace.Target{}, "", err
	}
	if err := store.Check(); err != nil {
		return workspace.Target{}, "", err
	}
	if !filepath.IsAbs(record.Path) || filepath.Clean(record.Path) != record.Path || !strings.EqualFold(filepath.Ext(record.Path), ".md") {
		return workspace.Target{}, "", errors.New("记忆引用路径无效")
	}
	rel, err := filepath.Rel(filepath.Clean(store.Path()), record.Path)
	if err != nil || rel == "." || !filepath.IsLocal(rel) || filepath.Dir(rel) != "." {
		return workspace.Target{}, "", errors.New("记忆引用超出选定范围")
	}
	file := workspace.At(store.Base, filepath.Join(store.Rel, rel))
	if err := file.Check(); err != nil {
		return workspace.Target{}, "", err
	}
	return store, filepath.Join(store.Path(), rel), nil
}

func readCurrentRecord(layout workspace.Layout, expected Record) (workspace.Target, string, []byte, fs.FileInfo, []Record, error) {
	store, path, err := safeRecordPath(layout, expected)
	if err != nil {
		return workspace.Target{}, "", nil, nil, nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return workspace.Target{}, "", nil, nil, nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return workspace.Target{}, "", nil, nil, nil, fmt.Errorf("拒绝读取链接或特殊文件：%s", path)
	}
	data, regular, err := readRegularFile(path, info)
	if err != nil {
		return workspace.Target{}, "", nil, nil, nil, err
	}
	if !regular {
		return workspace.Target{}, "", nil, nil, nil, fmt.Errorf("记忆文件已被替换：%s", path)
	}
	current, err := parseRecords(layout, expected.Scope, path, data)
	if err != nil {
		return workspace.Target{}, "", nil, nil, nil, err
	}
	matched := false
	for _, candidate := range current {
		if sameRecordVersion(expected, candidate) {
			matched = true
			break
		}
	}
	if !matched {
		return workspace.Target{}, "", nil, nil, nil, errors.New("记忆引用已过期或内容已变化")
	}
	if expected.Managed {
		if !canonicalManagedContent(data, expected.Text, expected.Always) {
			return workspace.Target{}, "", nil, nil, nil, fmt.Errorf("管理记忆已被手工修改，未覆盖：%s", path)
		}
	} else if !recordLineMatches(data, expected) {
		return workspace.Target{}, "", nil, nil, nil, errors.New("主题行已变化，记忆引用已过期")
	}
	return store, path, data, info, current, nil
}

func sameRecordVersion(a, b Record) bool {
	return a.Scope == b.Scope && a.Path == b.Path && a.Text == b.Text && a.Ref == b.Ref && a.Line == b.Line && a.Always == b.Always && a.Managed == b.Managed
}

func recordLineMatches(data []byte, record Record) bool {
	lines := splitFileLines(data)
	if record.Line < 1 || record.Line > len(lines) {
		return false
	}
	line := data[lines[record.Line-1].start:lines[record.Line-1].contentEnd]
	if record.Line == 1 && hasUTF8BOM(line) {
		line = line[3:]
	}
	return normalizeRecordText(string(line)) == normalizeRecordText(record.Text)
}

func normalizeTopicText(text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" || len(text) > 16*1024 || !utf8.ValidString(text) || strings.ContainsAny(text, "\r\n\u2028\u2029") || strings.Contains(text, alwaysMarker) || strings.ContainsFunc(text, func(char rune) bool {
		return unicode.IsControl(char) && char != '\t'
	}) {
		return "", errors.New("主题更新必须是有效的单行 UTF-8 文本，最长 16 KiB，且不能包含常用标记")
	}
	candidate := strings.TrimLeft(text, " \t")
	for strings.HasPrefix(candidate, ">") {
		candidate = strings.TrimLeft(candidate[1:], " \t")
	}
	if strings.HasPrefix(candidate, "#") || strings.HasPrefix(candidate, `\#`) || strings.HasPrefix(candidate, "&#35;") || strings.HasPrefix(candidate, "&#x23;") {
		return "", errors.New("主题更新不能变成 Markdown 标题")
	}
	return text, nil
}

type fileLine struct {
	start      int
	contentEnd int
	end        int
}

func splitFileLines(data []byte) []fileLine {
	var lines []fileLine
	for start := 0; start < len(data); {
		end := bytes.IndexByte(data[start:], '\n')
		if end < 0 {
			lines = append(lines, fileLine{start: start, contentEnd: len(data), end: len(data)})
			break
		}
		end += start
		contentEnd := end
		if contentEnd > start && data[contentEnd-1] == '\r' {
			contentEnd--
		}
		lines = append(lines, fileLine{start: start, contentEnd: contentEnd, end: end + 1})
		start = end + 1
	}
	return lines
}

func replaceTopicLine(data []byte, record Record, text string) ([]byte, error) {
	lines := splitFileLines(data)
	if record.Line < 1 || record.Line > len(lines) || !recordLineMatches(data, record) {
		return nil, errors.New("主题行已变化，记忆引用已过期")
	}
	line := lines[record.Line-1]
	prefix := data[line.start:line.start]
	if record.Line == 1 && hasUTF8BOM(data) {
		prefix = []byte{0xef, 0xbb, 0xbf}
	}
	updated := make([]byte, 0, len(data)-(line.contentEnd-line.start)+len(prefix)+len(text))
	updated = append(updated, data[:line.start]...)
	updated = append(updated, prefix...)
	updated = append(updated, text...)
	updated = append(updated, data[line.contentEnd:]...)
	return updated, nil
}

func removeTopicLine(data []byte, record Record) ([]byte, error) {
	lines := splitFileLines(data)
	if record.Line < 1 || record.Line > len(lines) || !recordLineMatches(data, record) {
		return nil, errors.New("主题行已变化，记忆引用已过期")
	}
	line := lines[record.Line-1]
	updated := make([]byte, 0, len(data)-(line.end-line.start)+3)
	updated = append(updated, data[:line.start]...)
	updated = append(updated, data[line.end:]...)
	if record.Line == 1 && hasUTF8BOM(data) && len(updated) > 0 {
		updated = append([]byte{0xef, 0xbb, 0xbf}, updated...)
	}
	return updated, nil
}

func updateManagedRecord(layout workspace.Layout, record Record, path string, data []byte, mode fs.FileMode, text string) error {
	if text == normalizeRecordText(record.Text) {
		return nil
	}
	others, err := readRecords(layout, record.Scope)
	if err != nil {
		return err
	}
	for _, other := range others {
		if other.Path != path && normalizeRecordText(other.Text) == text {
			return fmt.Errorf("同一结论已经存在于 %s；请核对后只忘掉多余的一条", other.Path)
		}
	}
	return replaceExistingFile(path, managedContent(text, record.Always, detectLineEnding(data), hasUTF8BOM(data)), mode)
}
