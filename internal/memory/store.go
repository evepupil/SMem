package memory

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"smem/internal/workspace"
)

func Add(layout workspace.Layout, global bool, text string) (string, bool, error) {
	return AddNote(layout, global, false, text)
}

func AddNote(layout workspace.Layout, global, always bool, text string) (path string, created bool, err error) {
	text, err = normalizeNoteText(text)
	if err != nil {
		return "", false, err
	}
	store, err := layout.Store(global)
	if err != nil {
		return "", false, err
	}
	if err := store.Check(); err != nil {
		return "", false, err
	}
	if global {
		git := workspace.At(layout.Home, ".mem/.git")
		if err := git.Check(); err != nil {
			return "", false, err
		}
		if _, err := os.Stat(git.Path()); err != nil {
			return "", false, fmt.Errorf("全局记忆仓库不可用，请先运行 mem init：%w", err)
		}
	}

	unlock, err := acquireMutationLock(store)
	if err != nil {
		return "", false, err
	}
	defer func() {
		if unlockErr := unlock(); unlockErr != nil {
			err = errors.Join(err, unlockErr)
		}
	}()

	if err := store.Check(); err != nil {
		return "", false, err
	}
	if err := os.MkdirAll(store.Path(), 0700); err != nil {
		return "", false, err
	}
	if err := store.Check(); err != nil {
		return "", false, err
	}

	scope := "project"
	if global {
		scope = "global"
	}
	records, err := readRecords(layout, scope)
	if err != nil {
		return "", false, err
	}
	for _, record := range records {
		if !record.Managed || normalizeRecordText(record.Text) != text {
			continue
		}
		_, recordPath, data, info, _, err := readCurrentRecord(layout, record)
		if err != nil {
			return "", false, err
		}
		if !canonicalManagedContent(data, record.Text, record.Always) {
			return "", false, fmt.Errorf("记忆已被手工修改，未覆盖：%s", recordPath)
		}
		if always && !record.Always {
			updated := managedContent(text, true, detectLineEnding(data), hasUTF8BOM(data))
			if err := replaceExistingFile(recordPath, updated, info.Mode()); err != nil {
				return "", false, err
			}
		}
		return recordPath, false, nil
	}

	name := managedNoteName(text)
	path = filepath.Join(store.Path(), name)
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return "", false, fmt.Errorf("记忆目标不是普通文件：%s", path)
		}
		data, regular, err := readRegularFile(path, info)
		if err != nil {
			return "", false, err
		}
		if !regular {
			return "", false, fmt.Errorf("记忆目标已被替换，未读取：%s", path)
		}
		canonicalText, marked := canonicalManagedState(data)
		if canonicalText != text {
			return "", false, fmt.Errorf("原记忆已被手工修改，已保留，请查看：%s", path)
		}
		if always && !marked {
			if err := replaceExistingFile(path, managedContent(text, true, detectLineEnding(data), hasUTF8BOM(data)), info.Mode()); err != nil {
				return "", false, err
			}
		}
		return path, false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", false, err
	}

	data := managedContent(text, always, "\n", false)
	if err := createNewFile(path, data, 0600); err != nil {
		return "", false, fmt.Errorf("发布记忆失败：%w", err)
	}
	return path, true, nil
}

func normalizeNoteText(text string) (string, error) {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	if text == "" || len(text) > 16*1024 || !utf8.ValidString(text) || strings.Contains(text, alwaysMarker) || strings.ContainsFunc(text, func(char rune) bool {
		return unicode.IsControl(char) && char != '\n' && char != '\t'
	}) {
		return "", errors.New("请提供一条有效的 UTF-8 文本记忆，最长 16 KiB，且不能包含常用标记")
	}
	return text, nil
}

func normalizeRecordText(text string) string {
	return strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
}

func managedNoteName(text string) string {
	hash := sha256.Sum256([]byte(text))
	return fmt.Sprintf("note-%x.md", hash[:10])
}

func managedContent(text string, always bool, lineEnding string, bom bool) []byte {
	if lineEnding != "\r\n" {
		lineEnding = "\n"
	}
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\n", lineEnding)
	content := text + lineEnding
	if always {
		content = alwaysMarker + lineEnding + content
	}
	data := []byte(content)
	if bom {
		data = append([]byte{0xef, 0xbb, 0xbf}, data...)
	}
	return data
}

func canonicalManagedState(data []byte) (string, bool) {
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	normalized := bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	marked := []byte(alwaysMarker + "\n")
	always := bytes.HasPrefix(normalized, marked)
	if always {
		normalized = bytes.TrimPrefix(normalized, marked)
	}
	if !bytes.HasSuffix(normalized, []byte("\n")) {
		return "", false
	}
	return strings.TrimSuffix(string(normalized), "\n"), always
}

func canonicalManagedContent(data []byte, text string, always bool) bool {
	gotText, gotAlways := canonicalManagedState(data)
	return gotText == normalizeRecordText(text) && gotAlways == always
}

func hasUTF8BOM(data []byte) bool {
	return bytes.HasPrefix(data, []byte{0xef, 0xbb, 0xbf})
}

func detectLineEnding(data []byte) string {
	if bytes.Contains(data, []byte("\r\n")) {
		return "\r\n"
	}
	return "\n"
}
