package adapters

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"
)

var utf8BOM = []byte{0xef, 0xbb, 0xbf}

// PatchMarkdownBlock inserts, updates, or removes the managed <!-- smem:start --> block
// while preserving UTF-8 BOM, line endings, and all surrounding user content.
func PatchMarkdownBlock(original []byte, content string, remove bool) ([]byte, error) {
	hasBOM := bytes.HasPrefix(original, utf8BOM)
	raw := bytes.TrimPrefix(original, utf8BOM)
	if len(raw) > 0 && (!utf8.Valid(raw) || bytes.IndexByte(raw, 0) >= 0) {
		return nil, errors.New("说明文件不是有效的 UTF-8 文本")
	}
	newline := "\n"
	if bytes.Contains(raw, []byte("\r\n")) {
		newline = "\r\n"
	}

	body := string(raw)
	begin := strings.Index(body, markerStart)
	finish := strings.Index(body, markerEnd)

	var result string
	switch {
	case begin < 0 && finish < 0:
		if remove {
			result = body
		} else {
			block := markerStart + newline + strings.ReplaceAll(content, "\n", newline) + newline + markerEnd
			result = body
			if result != "" && !strings.HasSuffix(result, "\n") {
				result += newline
			}
			if result != "" && !strings.HasSuffix(result, newline+newline) {
				result += newline
			}
			result += block + newline
		}
	case begin >= 0 && finish > begin && strings.Count(body, markerStart) == 1 && strings.Count(body, markerEnd) == 1:
		afterEnd := finish + len(markerEnd)
		if remove {
			prefix := strings.TrimRight(body[:begin], "\r\n")
			suffix := strings.TrimLeft(body[afterEnd:], "\r\n")
			switch {
			case prefix == "" && suffix == "":
				result = ""
			case prefix == "":
				result = suffix
			case suffix == "":
				result = prefix + newline
			default:
				result = prefix + newline + newline + suffix
			}
		} else {
			block := markerStart + newline + strings.ReplaceAll(content, "\n", newline) + newline + markerEnd
			result = body[:begin] + block + body[afterEnd:]
		}
	default:
		return nil, errors.New("smem 标记不成对或重复，已停止写入以保留原文")
	}

	if hasBOM {
		return append(bytes.Clone(utf8BOM), []byte(result)...), nil
	}
	return []byte(result), nil
}

// PatchCodexTOML maintains a comment-bounded [mcp_servers.smem] section in Codex config.toml
// without disturbing any user comments or other TOML tables.
func PatchCodexTOML(original []byte, smemBin string, remove bool) ([]byte, error) {
	raw := bytes.TrimPrefix(original, utf8BOM)
	if len(raw) > 0 && !utf8.Valid(raw) {
		return nil, errors.New("Codex config.toml 不是有效的 UTF-8 文本")
	}
	newline := "\n"
	if bytes.Contains(raw, []byte("\r\n")) {
		newline = "\r\n"
	}

	body := string(raw)
	begin := strings.Index(body, tomlMarkerStart)
	finish := strings.Index(body, tomlMarkerEnd)

	escapedBin := strings.ReplaceAll(smemBin, `\`, `\\`)
	escapedBin = strings.ReplaceAll(escapedBin, `"`, `\"`)
	block := strings.Join([]string{
		tomlMarkerStart,
		"[mcp_servers.smem]",
		`command = "` + escapedBin + `"`,
		`args = ["mcp"]`,
		"enabled = true",
		tomlMarkerEnd,
	}, newline)

	var result string
	switch {
	case begin < 0 && finish < 0:
		if remove {
			result = body
		} else {
			// If user already manually configured [mcp_servers.smem] without markers, do not duplicate.
			if strings.Contains(body, "[mcp_servers.smem]") {
				return original, nil
			}
			result = body
			if result != "" && !strings.HasSuffix(result, "\n") {
				result += newline
			}
			if result != "" && !strings.HasSuffix(result, newline+newline) {
				result += newline
			}
			result += block + newline
		}
	case begin >= 0 && finish > begin && strings.Count(body, tomlMarkerStart) == 1 && strings.Count(body, tomlMarkerEnd) == 1:
		afterEnd := finish + len(tomlMarkerEnd)
		if remove {
			prefix := strings.TrimRight(body[:begin], "\r\n")
			suffix := strings.TrimLeft(body[afterEnd:], "\r\n")
			switch {
			case prefix == "" && suffix == "":
				result = ""
			case prefix == "":
				result = suffix
			case suffix == "":
				result = prefix + newline
			default:
				result = prefix + newline + newline + suffix
			}
		} else {
			result = body[:begin] + block + body[afterEnd:]
		}
	default:
		return nil, errors.New("Codex config.toml 中的 smem 标记不成对，已停止写入")
	}

	return []byte(result), nil
}

// ParseJSONObject parses a JSON (or lightly commented JSONC) object into map[string]any.
func ParseJSONObject(raw []byte) (map[string]any, error) {
	cleaned := bytes.TrimSpace(bytes.TrimPrefix(raw, utf8BOM))
	if len(cleaned) == 0 {
		return map[string]any{}, nil
	}
	var m map[string]any
	if err := json.Unmarshal(cleaned, &m); err == nil {
		if m == nil {
			return map[string]any{}, nil
		}
		return m, nil
	}
	// Retry after stripping simple // comments and trailing commas for JSONC files.
	stripped := stripSimpleJSONC(string(cleaned))
	if err := json.Unmarshal([]byte(stripped), &m); err != nil {
		return nil, fmt.Errorf("JSON 解析失败：%w", err)
	}
	if m == nil {
		return map[string]any{}, nil
	}
	return m, nil
}

// MarshalJSONObject pretty-prints a JSON object with 2-space indentation and trailing newline.
func MarshalJSONObject(m map[string]any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func getOrCreateMap(parent map[string]any, key string) map[string]any {
	if existing, ok := parent[key].(map[string]any); ok && existing != nil {
		return existing
	}
	child := map[string]any{}
	parent[key] = child
	return child
}

// ensureHookEntry upserts a single smem command entry inside Claude/Codex/Gemini hooks arrays
// identified by a command containing "smem hook".
func ensureHookEntry(hooksMap map[string]any, eventName string, command string, matcher string, remove bool) {
	var rawList []any
	if existing, ok := hooksMap[eventName].([]any); ok {
		rawList = existing
	}

	var filtered []any
	for _, item := range rawList {
		if isSmemHookBlock(item) {
			continue
		}
		filtered = append(filtered, item)
	}

	if !remove {
		entry := map[string]any{
			"hooks": []any{
				map[string]any{
					"type":    "command",
					"command": command,
				},
			},
		}
		if matcher != "" {
			entry["matcher"] = matcher
		}
		filtered = append(filtered, entry)
	}

	if len(filtered) == 0 {
		delete(hooksMap, eventName)
	} else {
		hooksMap[eventName] = filtered
	}
}

// ensureCursorHookEntry upserts a smem hook inside Cursor's .cursor/hooks.json format.
func ensureCursorHookEntry(hooksMap map[string]any, eventName string, command string, remove bool) {
	var rawList []any
	if existing, ok := hooksMap[eventName].([]any); ok {
		rawList = existing
	}
	var filtered []any
	for _, item := range rawList {
		if m, ok := item.(map[string]any); ok {
			if cmd, _ := m["command"].(string); isSmemCommand(cmd) {
				continue
			}
		}
		filtered = append(filtered, item)
	}
	if !remove {
		filtered = append(filtered, map[string]any{
			"command": command,
		})
	}
	if len(filtered) == 0 {
		delete(hooksMap, eventName)
	} else {
		hooksMap[eventName] = filtered
	}
}

func isSmemHookBlock(item any) bool {
	m, ok := item.(map[string]any)
	if !ok {
		return false
	}
	if cmd, _ := m["command"].(string); isSmemCommand(cmd) {
		return true
	}
	inner, ok := m["hooks"].([]any)
	if !ok || len(inner) == 0 {
		return false
	}
	for _, h := range inner {
		hm, ok := h.(map[string]any)
		if !ok {
			continue
		}
		if cmd, _ := hm["command"].(string); isSmemCommand(cmd) {
			return true
		}
	}
	return false
}

func isSmemCommand(cmd string) bool {
	low := strings.ToLower(cmd)
	return strings.Contains(low, "smem hook ") || strings.Contains(low, "smem.exe\" hook ") || strings.Contains(low, "smem.exe hook ")
}

// quoteCommand builds a clean shell command string safe across cmd.exe, PowerShell, and Git Bash.
func quoteCommand(bin string, args ...string) string {
	normalized := filepath.ToSlash(bin)
	parts := make([]string, 0, 1+len(args))
	if strings.ContainsAny(normalized, " \t:/") {
		parts = append(parts, `"`+normalized+`"`)
	} else {
		parts = append(parts, normalized)
	}
	parts = append(parts, args...)
	return strings.Join(parts, " ")
}

// backupAndWriteFile saves a timestamped backup of an existing target file before replacing it atomically.
func backupAndWriteFile(backupRoot, targetPath string, data []byte, perm os.FileMode) (string, error) {
	backupPath := ""
	if orig, err := os.ReadFile(targetPath); err == nil {
		if bytes.Equal(orig, data) {
			return "", nil
		}
		if backupRoot != "" {
			if err := os.MkdirAll(backupRoot, 0700); err != nil {
				return "", err
			}
			stamp := time.Now().Format("20060102-150405")
			safeName := sanitizeBackupName(targetPath)
			backupPath = filepath.Join(backupRoot, stamp+"_"+safeName)
			if err := os.WriteFile(backupPath, orig, 0600); err != nil {
				return "", err
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}

	dir := filepath.Dir(targetPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return backupPath, err
	}
	tmp, err := os.CreateTemp(dir, ".smem-cfg-*")
	if err != nil {
		return backupPath, err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return backupPath, err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return backupPath, err
	}
	if err := tmp.Close(); err != nil {
		return backupPath, err
	}
	if runtime.GOOS != "windows" {
		_ = os.Chmod(tmpName, perm)
	}
	if err := os.Rename(tmpName, targetPath); err != nil {
		return backupPath, err
	}
	cleanup = false
	return backupPath, nil
}

func sanitizeBackupName(p string) string {
	base := filepath.Base(p)
	parent := filepath.Base(filepath.Dir(p))
	raw := parent + "_" + base
	var b strings.Builder
	for _, r := range raw {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

// ensureGitExclude adds a pattern to .git/info/exclude so local-only files like
// .claude/settings.local.json NEVER pollute git status or touch .gitignore.
func ensureGitExclude(repoRoot, pattern string) error {
	dotGit := filepath.Join(repoRoot, ".git")
	info, err := os.Stat(dotGit)
	if err != nil {
		return nil
	}
	gitDir := dotGit
	if !info.IsDir() {
		// Worktree: parse gitdir and follow commondir
		data, err := os.ReadFile(dotGit)
		if err != nil {
			return nil
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "gitdir:") {
				target := strings.TrimSpace(strings.TrimPrefix(line, "gitdir:"))
				if !filepath.IsAbs(target) {
					target = filepath.Join(repoRoot, target)
				}
				gitDir = filepath.Clean(target)
				if commonData, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
					rel := strings.TrimSpace(string(commonData))
					if rel != "" {
						if !filepath.IsAbs(rel) {
							gitDir = filepath.Clean(filepath.Join(gitDir, rel))
						} else {
							gitDir = filepath.Clean(rel)
						}
					}
				}
				break
			}
		}
	}

	excludePath := filepath.Join(gitDir, "info", "exclude")
	existing, err := os.ReadFile(excludePath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, line := range strings.Split(string(existing), "\n") {
		if strings.TrimSpace(line) == pattern {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(excludePath), 0755); err != nil {
		return err
	}
	var out strings.Builder
	out.Write(existing)
	if len(existing) > 0 && existing[len(existing)-1] != '\n' {
		out.WriteByte('\n')
	}
	out.WriteString("# Managed by smem (keeps local settings untracked)\n")
	out.WriteString(pattern + "\n")
	return os.WriteFile(excludePath, []byte(out.String()), 0644)
}

func stripSimpleJSONC(s string) string {
	var out strings.Builder
	inStr := false
	escaped := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inStr {
			out.WriteByte(c)
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				inStr = false
			}
			continue
		}
		if c == '"' {
			inStr = true
			out.WriteByte(c)
			continue
		}
		if c == '/' && i+1 < len(s) && s[i+1] == '/' {
			for i < len(s) && s[i] != '\n' {
				i++
			}
			if i < len(s) {
				out.WriteByte('\n')
			}
			continue
		}
		out.WriteByte(c)
	}
	return out.String()
}
