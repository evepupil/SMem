package memdir

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var utf8BOM = []byte{0xef, 0xbb, 0xbf}

// ComputeETag returns a compact 12-hex-char SHA-256 revision tag for raw file bytes.
func ComputeETag(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:6])
}

// ParseDocument parses a Claude-Code-compatible Markdown memory file.
// If no YAML frontmatter is present, it synthesizes Name/Description gracefully.
func ParseDocument(raw []byte, filename string) (Frontmatter, string, bool) {
	cleaned := bytes.TrimPrefix(raw, utf8BOM)
	text := strings.ReplaceAll(string(cleaned), "\r\n", "\n")

	fm, body, hasFM := parseFrontmatterBlock(text)
	if hasFM {
		if fm.Type == "" {
			fm.Type = TypeUntyped
		}
		if fm.Name == "" {
			fm.Name = fallbackName(body, filename)
		}
		if fm.Description == "" {
			fm.Description = fallbackDescription(body)
		}
		return fm, strings.TrimSpace(body), true
	}

	// Plain human-authored Markdown without frontmatter.
	fm = Frontmatter{
		Name:        fallbackName(text, filename),
		Description: fallbackDescription(text),
		Type:        TypeUntyped,
	}
	return fm, strings.TrimSpace(text), false
}

// ScanFrontmatter reads only the beginning of a file (~4KB) to inspect metadata quickly,
// mirroring Claude Code's scanMemoryFiles().
func ScanFrontmatter(path string) (Frontmatter, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return Frontmatter{}, false, err
	}
	defer f.Close()

	buf := make([]byte, 4096)
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return Frontmatter{}, false, err
	}
	fm, _, hasFM := ParseDocument(buf[:n], filepath.Base(path))
	return fm, hasFM, nil
}

// RenderDocument serializes frontmatter and body into clean Claude-compatible Markdown.
func RenderDocument(fm Frontmatter, body string) []byte {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("name: ")
	b.WriteString(formatYAMLScalar(fm.Name))
	b.WriteString("\n")
	b.WriteString("description: ")
	b.WriteString(formatYAMLScalar(fm.Description))
	b.WriteString("\n")
	b.WriteString("type: ")
	b.WriteString(string(fm.Type))
	b.WriteString("\n")
	for _, ex := range fm.Extra {
		if ex.Key == "" {
			continue
		}
		b.WriteString(ex.Key)
		b.WriteString(": ")
		b.WriteString(formatYAMLScalar(ex.Value))
		b.WriteString("\n")
	}
	b.WriteString("---\n\n")
	b.WriteString(strings.TrimSpace(strings.ReplaceAll(body, "\r\n", "\n")))
	b.WriteString("\n")
	return []byte(b.String())
}

func parseFrontmatterBlock(text string) (Frontmatter, string, bool) {
	lines := strings.Split(text, "\n")
	if len(lines) < 3 || strings.TrimSpace(lines[0]) != "---" {
		return Frontmatter{}, text, false
	}

	endIdx := -1
	for i := 1; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "---" || trimmed == "..." {
			endIdx = i
			break
		}
	}
	if endIdx < 0 {
		return Frontmatter{}, text, false
	}

	var fm Frontmatter
	headerLines := lines[1:endIdx]
	for i := 0; i < len(headerLines); i++ {
		line := headerLines[i]
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		colon := strings.IndexByte(line, ':')
		if colon <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:colon])
		rawVal := strings.TrimSpace(line[colon+1:])

		// Handle YAML block scalars (|, |-, >, >-)
		var val string
		if rawVal == "|" || rawVal == "|-" || rawVal == ">" || rawVal == ">-" {
			folded := strings.HasPrefix(rawVal, ">")
			var block []string
			for i+1 < len(headerLines) {
				next := headerLines[i+1]
				if strings.HasPrefix(next, " ") || strings.HasPrefix(next, "\t") || strings.TrimSpace(next) == "" {
					block = append(block, strings.TrimSpace(next))
					i++
				} else {
					break
				}
			}
			if folded {
				val = strings.TrimSpace(strings.Join(block, " "))
			} else {
				val = strings.TrimSpace(strings.Join(block, "\n"))
			}
		} else {
			val = parseYAMLScalar(rawVal)
		}

		switch strings.ToLower(key) {
		case "name":
			fm.Name = singleLine(val)
		case "description":
			fm.Description = singleLine(val)
		case "type":
			if mt, err := ParseMemoryType(val); err == nil {
				fm.Type = mt
			} else {
				fm.Type = TypeUntyped
			}
		default:
			fm.Extra = append(fm.Extra, ExtraField{Key: key, Value: val})
		}
	}

	body := strings.Join(lines[endIdx+1:], "\n")
	return fm, body, true
}

func parseYAMLScalar(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		if s[0] == '"' && s[len(s)-1] == '"' {
			inner := s[1 : len(s)-1]
			inner = strings.ReplaceAll(inner, `\"`, `"`)
			inner = strings.ReplaceAll(inner, `\n`, "\n")
			inner = strings.ReplaceAll(inner, `\\`, `\`)
			return inner
		}
		if s[0] == '\'' && s[len(s)-1] == '\'' {
			inner := s[1 : len(s)-1]
			return strings.ReplaceAll(inner, "''", "'")
		}
	}
	return s
}

func formatYAMLScalar(s string) string {
	clean := singleLine(s)
	if clean == "" {
		return `""`
	}
	needsQuote := strings.ContainsAny(clean, ":#\"'{}[]&*?|>!%@`") ||
		strings.HasPrefix(clean, "-") ||
		strings.HasPrefix(clean, " ") ||
		strings.HasSuffix(clean, " ")
	if !needsQuote {
		return clean
	}
	escaped := strings.ReplaceAll(clean, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	return `"` + escaped + `"`
}

func singleLine(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

func fallbackName(body, filename string) string {
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			title := strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
			if title != "" {
				return singleLine(title)
			}
		}
	}
	base := strings.TrimSuffix(filename, filepath.Ext(filename))
	return strings.ReplaceAll(base, "_", " ")
}

func fallbackDescription(body string) string {
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || trimmed == "---" {
			continue
		}
		trimmed = strings.TrimLeft(trimmed, "-* ")
		runes := []rune(singleLine(trimmed))
		if len(runes) > 120 {
			return string(runes[:120]) + "…"
		}
		return string(runes)
	}
	return ""
}
