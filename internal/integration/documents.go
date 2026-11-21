package integration

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"smem/internal/workspace"
)

const start = "<!-- smem:start -->"
const end = "<!-- smem:end -->"
const rule = "开工先实际运行 `mem search --start 关键词`，选两三个实词；未命中再换个说法查一次。用户明确说“以后”“每次”或交代长期约定，就已授权保存：当场运行 `mem add \"结论\"`，成功再回复，无需改项目配置或再问确认。仅跨项目偏好加 `--global`，明确要求开工提醒才加 `--always`。要翻看用 `mem list`、`mem show 引用`。纠正先查原文，再用 `mem update 引用 \"仅新的现行结论\"`；核对回显，不把旧值或更换经过留在正文，历史交给 Git。忘掉用 `mem forget 引用`。没有成功回执就说明未完成；不要记凭证和聊天原文。"

type document struct {
	target     workspace.Target
	importOnly bool
}

type edit struct {
	document
	before, after []byte
	exists        bool
	mode          os.FileMode
}

func render(original []byte, importOnly bool) ([]byte, error) {
	bom := []byte{0xef, 0xbb, 0xbf}
	hasBOM := bytes.HasPrefix(original, bom)
	raw := bytes.TrimPrefix(original, bom)
	if !utf8.Valid(raw) || bytes.IndexByte(raw, 0) >= 0 {
		return nil, errors.New("说明文件不是 UTF-8 文本；请手动接入")
	}
	newline := "\n"
	if bytes.Contains(raw, []byte("\r\n")) {
		newline = "\r\n"
	}
	body := string(raw)
	result := body
	if importOnly {
		first, _, _ := strings.Cut(body, "\n")
		if strings.TrimSpace(first) != "@AGENTS.md" {
			result = "@AGENTS.md" + newline + body
		}
	} else {
		block := start + newline + rule + newline + end
		begin, finish := strings.Index(body, start), strings.Index(body, end)
		switch {
		case begin < 0 && finish < 0:
			if result != "" && !strings.HasSuffix(result, "\n") {
				result += newline
			}
			if result != "" {
				result += newline
			}
			result += block + newline
		case begin >= 0 && finish > begin && strings.Count(body, start) == 1 && strings.Count(body, end) == 1:
			for _, marker := range []struct {
				at    int
				value string
			}{{begin, start}, {finish, end}} {
				rest := body[marker.at+len(marker.value):]
				if (marker.at > 0 && body[marker.at-1] != '\n') || (rest != "" && !strings.HasPrefix(rest, "\n") && !strings.HasPrefix(rest, "\r\n")) {
					return nil, errors.New("smem 标记未独占一行，保留原文")
				}
			}
			result = body[:begin] + block + body[finish+len(end):]
		default:
			return nil, errors.New("smem 标记不成对或重复，保留原文")
		}
	}
	if hasBOM {
		return append(bytes.Clone(bom), result...), nil
	}
	return []byte(result), nil
}

func prepare(doc document) (edit, error) {
	change := edit{document: doc, mode: 0600}
	if err := doc.target.Check(); err != nil {
		return change, err
	}
	path := doc.target.Path()
	info, err := os.Stat(path)
	if err == nil {
		if !info.Mode().IsRegular() {
			return change, fmt.Errorf("不是普通文件：%s", path)
		}
		change.exists, change.mode = true, info.Mode().Perm()
		change.before, err = os.ReadFile(path)
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return change, err
	}
	seed := change.before
	if filepath.ToSlash(doc.target.Rel) == ".windsurf/rules/smem.md" {
		if change.exists && !bytes.Contains(seed, []byte(start)) {
			return change, fmt.Errorf("同名规则由用户管理，未改动：%s", path)
		}
		if !change.exists {
			seed = []byte("---\ntrigger: always_on\n---\n")
		}
	}
	change.after, err = render(seed, doc.importOnly)
	if err != nil {
		return change, fmt.Errorf("%s：%w", path, err)
	}
	return change, nil
}

func (change edit) write() error {
	if err := change.target.Check(); err != nil {
		return err
	}
	path := change.target.Path()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".smem-instruction-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err := file.Chmod(change.mode); err != nil {
		file.Close()
		return err
	}
	_, writeErr := file.Write(change.after)
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return err
	}
	if !change.exists {
		return os.Link(file.Name(), path)
	}
	current, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(current, change.before) {
		return fmt.Errorf("文件已在预览后变动，停止以保留你的修改：%s", path)
	}
	return os.Rename(file.Name(), path)
}
