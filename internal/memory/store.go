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
	text = strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	if text == "" || len(text) > 16*1024 || !utf8.ValidString(text) || strings.ContainsFunc(text, func(char rune) bool { return unicode.IsControl(char) && char != '\n' && char != '\t' }) {
		return "", false, errors.New("请提供一条有效的 UTF-8 文本记忆，最长 16 KiB")
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
	if err := os.MkdirAll(store.Path(), 0700); err != nil {
		return "", false, err
	}
	content := []byte(text + "\n")
	hash := sha256.Sum256(content)
	words := strings.FieldsFunc(text, func(char rune) bool { return !unicode.IsLetter(char) && !unicode.IsDigit(char) })
	label := []rune(strings.Join(words, "-"))
	if len(label) > 24 {
		label = label[:24]
	}
	name := fmt.Sprintf("note-%s-%x.md", string(label), hash[:10])
	file := workspace.At(store.Base, filepath.Join(store.Rel, name))
	if err := file.Check(); err != nil {
		return "", false, err
	}
	temp, err := os.CreateTemp(store.Path(), ".pending-*")
	if err != nil {
		return "", false, err
	}
	defer os.Remove(temp.Name())
	_, writeErr := temp.Write(content)
	if err := errors.Join(writeErr, temp.Close()); err != nil {
		return "", false, err
	}
	if err := os.Link(temp.Name(), file.Path()); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return "", false, fmt.Errorf("发布记忆失败（需要支持硬链接的本地文件系统）：%w", err)
		}
		if err := file.Check(); err != nil {
			return "", false, err
		}
		old, err := os.ReadFile(file.Path())
		if err != nil {
			return "", false, err
		}
		if !bytes.Equal(bytes.ReplaceAll(old, []byte("\r\n"), []byte("\n")), content) {
			return "", false, fmt.Errorf("原记忆已被手改，已保留，请查看：%s", file.Path())
		}
		return file.Path(), false, nil
	}
	return file.Path(), true, nil
}
