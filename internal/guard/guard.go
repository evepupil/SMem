package guard

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

// SecretFinding describes a detected credential pattern without leaking its value.
type SecretFinding struct {
	Rule string `json:"rule"`
	Line int    `json:"line"`
}

type secretRule struct {
	name string
	re   *regexp.Regexp
}

var secretRules = []secretRule{
	{name: "private_key", re: regexp.MustCompile(`-----BEGIN\s+(?:RSA |EC |DSA |OPENSSH |PGP )?PRIVATE KEY-----`)},
	{name: "anthropic_key", re: regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{20,}\b`)},
	{name: "openai_key", re: regexp.MustCompile(`\bsk-(?:proj-|live-|test-)?[A-Za-z0-9_-]{24,}\b`)},
	{name: "github_token", re: regexp.MustCompile(`\b(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{30,}\b`)},
	{name: "github_pat", re: regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{30,}\b`)},
	{name: "aws_access_key", re: regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`)},
	{name: "google_api_key", re: regexp.MustCompile(`\bAIza[0-9A-Za-z\-_]{35}\b`)},
	{name: "slack_token", re: regexp.MustCompile(`\bxox[baprs]-[0-9A-Za-z-]{10,}\b`)},
	{name: "stripe_key", re: regexp.MustCompile(`\b(?:sk|rk)_live_[0-9A-Za-z]{20,}\b`)},
	{name: "jwt_token", re: regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b`)},
	{name: "db_url_password", re: regexp.MustCompile(`\b(?:postgres|postgresql|mysql|mongodb(?:\+srv)?|redis|amqp)://[^/\s:@]+:[^/\s@]{6,}@[^\s]+`)},
}

var privateBlockRe = regexp.MustCompile(`(?is)<private>.*?</private>`)

// StripPrivate removes any <private>...</private> spans before persisting memory.
func StripPrivate(text string) string {
	cleaned := privateBlockRe.ReplaceAllString(text, "")
	return strings.TrimSpace(cleaned)
}

// ScanSecrets inspects text for high-confidence credentials and returns findings.
func ScanSecrets(text string) []SecretFinding {
	var findings []SecretFinding
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		for _, r := range secretRules {
			if r.re.MatchString(line) {
				findings = append(findings, SecretFinding{
					Rule: r.name,
					Line: i + 1,
				})
			}
		}
	}
	return findings
}

// CheckNoSecrets returns an actionable error if any credential pattern is found.
func CheckNoSecrets(text string) error {
	findings := ScanSecrets(text)
	if len(findings) == 0 {
		return nil
	}
	var parts []string
	seen := make(map[string]bool)
	for _, f := range findings {
		key := fmt.Sprintf("%s(第%d行)", f.Rule, f.Line)
		if !seen[key] {
			seen[key] = true
			parts = append(parts, key)
		}
	}
	return fmt.Errorf("检测到疑似密钥或敏感凭证，已拒绝写入：%s（请移除凭证后再保存）", strings.Join(parts, ", "))
}

// ValidateMemoryFilename ensures file is a plain single-segment .md filename within a memory directory.
func ValidateMemoryFilename(name string) error {
	if name == "" {
		return errors.New("文件名不能为空")
	}
	if strings.ContainsRune(name, 0) {
		return errors.New("文件名包含非法 NUL 字符")
	}
	if strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("文件名不允许包含路径分隔符：%q", name)
	}
	if name == "." || name == ".." || strings.Contains(name, "..") {
		return fmt.Errorf("非法文件名：%q", name)
	}
	if !strings.HasSuffix(strings.ToLower(name), ".md") {
		return fmt.Errorf("记忆文件必须以 .md 结尾：%q", name)
	}
	if strings.EqualFold(name, "MEMORY.md") {
		return errors.New("不能直接将 MEMORY.md 作为主题文件写入")
	}
	return nil
}

// SlugifyFilename builds a clean, readable, cross-platform safe filename like "project_release_owner.md".
func SlugifyFilename(memType, name string) string {
	var b strings.Builder
	prefix := strings.ToLower(strings.TrimSpace(memType))
	if prefix != "" && prefix != "untyped" {
		b.WriteString(prefix)
		b.WriteByte('_')
	}

	prevUnderscore := true
	runeCount := 0
	for _, r := range strings.TrimSpace(name) {
		if runeCount >= 48 {
			break
		}
		switch {
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r - 'A' + 'a')
			prevUnderscore = false
			runeCount++
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevUnderscore = false
			runeCount++
		case unicode.Is(unicode.Han, r):
			b.WriteRune(r)
			prevUnderscore = false
			runeCount++
		default:
			if !prevUnderscore {
				b.WriteByte('_')
				prevUnderscore = true
				runeCount++
			}
		}
	}

	slug := strings.Trim(b.String(), "_")
	if slug == "" || slug == prefix {
		if prefix != "" && prefix != "untyped" {
			slug = prefix + "_note"
		} else {
			slug = "note"
		}
	}
	return slug + ".md"
}

// CheckPathSafe verifies that target does not traverse symlinks outside root.
func CheckPathSafe(root, target string) error {
	cleanRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	cleanTarget, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(cleanRoot, cleanTarget)
	if err != nil {
		return err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("路径超出允许根目录：%s", target)
	}
	// Check every existing path segment from root down to target for symlinks.
	cur := cleanRoot
	if info, err := os.Lstat(cur); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("拒绝通过符号链接目录写入：%s", cur)
	}
	if rel == "." {
		return nil
	}
	for _, seg := range strings.Split(rel, string(filepath.Separator)) {
		cur = filepath.Join(cur, seg)
		info, err := os.Lstat(cur)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				break
			}
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("拒绝访问符号链接路径：%s", cur)
		}
	}
	return nil
}
