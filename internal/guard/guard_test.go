package guard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanSecrets(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		wantMatch bool
		wantRule  string
	}{
		{
			name:      "clean text",
			input:     "本项目发版前必须由小周审批放行。",
			wantMatch: false,
		},
		{
			name:      "openai key",
			input:     "token is sk-proj-abcdefghijklmnopqrstuvwxyz123456",
			wantMatch: true,
			wantRule:  "openai_key",
		},
		{
			name:      "anthropic key",
			input:     "key=sk-ant-api03-abcdefghijklmnopqrstuvwxyz1234",
			wantMatch: true,
			wantRule:  "anthropic_key",
		},
		{
			name:      "github pat",
			input:     "ghp_1234567890abcdefghijklmnopqrstuvwxyz",
			wantMatch: true,
			wantRule:  "github_token",
		},
		{
			name:      "private key header",
			input:     "-----BEGIN OPENSSH PRIVATE KEY-----\nabc",
			wantMatch: true,
			wantRule:  "private_key",
		},
		{
			name:      "db url with password",
			input:     "postgres://admin:supersecret99@db.prod.local:5432/app",
			wantMatch: true,
			wantRule:  "db_url_password",
		},
		{
			name:      "db url without password",
			input:     "postgres://localhost:5432/app",
			wantMatch: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			findings := ScanSecrets(tc.input)
			if tc.wantMatch && len(findings) == 0 {
				t.Fatalf("expected secret match for %q, got none", tc.name)
			}
			if !tc.wantMatch && len(findings) > 0 {
				t.Fatalf("unexpected secret match for %q: %+v", tc.name, findings)
			}
			if tc.wantMatch && findings[0].Rule != tc.wantRule {
				t.Fatalf("rule = %q, want %q", findings[0].Rule, tc.wantRule)
			}
		})
	}
}

func TestStripPrivate(t *testing.T) {
	in := "保留正文。<private>这里有临时密码 sk-ant-123456789012345678901234</private> 结尾。"
	got := StripPrivate(in)
	if strings.Contains(got, "临时密码") || strings.Contains(got, "sk-ant-") {
		t.Fatalf("private block was not stripped: %q", got)
	}
	if !strings.Contains(got, "保留正文。") || !strings.Contains(got, "结尾。") {
		t.Fatalf("public content lost: %q", got)
	}
}

func TestValidateMemoryFilename(t *testing.T) {
	valid := []string{"project_release.md", "user_profile.md", "发版约定.md"}
	for _, v := range valid {
		if err := ValidateMemoryFilename(v); err != nil {
			t.Fatalf("expected valid filename %q, got %v", v, err)
		}
	}
	invalid := []string{"", "MEMORY.md", "memory.md", "../escape.md", "sub/a.md", `sub\a.md`, "notes.txt", "a\x00.md"}
	for _, iv := range invalid {
		if err := ValidateMemoryFilename(iv); err == nil {
			t.Fatalf("expected error for invalid filename %q", iv)
		}
	}
}

func TestSlugifyFilename(t *testing.T) {
	got := SlugifyFilename("project", "Release Approval Owner!")
	if got != "project_release_approval_owner.md" {
		t.Fatalf("got %q", got)
	}
	gotCN := SlugifyFilename("feedback", "禁用 Mock 数据库")
	if gotCN != "feedback_禁用_mock_数据库.md" {
		t.Fatalf("got %q", gotCN)
	}
}

func TestCheckPathSafe(t *testing.T) {
	root := t.TempDir()
	okPath := filepath.Join(root, "note.md")
	if err := CheckPathSafe(root, okPath); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	escape := filepath.Join(root, "..", "outside.md")
	if err := CheckPathSafe(root, escape); err == nil {
		t.Fatalf("expected error for path escape")
	}
	if err := os.WriteFile(okPath, []byte("hi"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := CheckPathSafe(root, okPath); err != nil {
		t.Fatalf("unexpected error on existing regular file: %v", err)
	}
}
