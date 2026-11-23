package memdir

import (
	"fmt"
	"strings"
	"time"
)

// Scope identifies one of SMem's two memory scopes.
type Scope string

const (
	ScopeProject Scope = "project"
	ScopeGlobal  Scope = "global"
)

// ParseScope validates a scope string. Empty defaults to project.
func ParseScope(s string) (Scope, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "project":
		return ScopeProject, nil
	case "global":
		return ScopeGlobal, nil
	default:
		return "", fmt.Errorf("无效的作用域 %q（仅支持 project 或 global）", s)
	}
}

// MemoryType aligns 1:1 with Claude Code's 4 official auto-memory types.
type MemoryType string

const (
	TypeUser      MemoryType = "user"
	TypeFeedback  MemoryType = "feedback"
	TypeProject   MemoryType = "project"
	TypeReference MemoryType = "reference"
	TypeUntyped   MemoryType = "untyped" // Fallback for hand-written plain Markdown files
)

// ParseMemoryType validates one of the 4 official memory types.
func ParseMemoryType(s string) (MemoryType, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "user":
		return TypeUser, nil
	case "feedback":
		return TypeFeedback, nil
	case "project":
		return TypeProject, nil
	case "reference":
		return TypeReference, nil
	default:
		return "", fmt.Errorf("无效的记忆类型 %q（仅支持 user、feedback、project、reference）", s)
	}
}

func typeOrder(t MemoryType) int {
	switch t {
	case TypeUser:
		return 0
	case TypeFeedback:
		return 1
	case TypeProject:
		return 2
	case TypeReference:
		return 3
	default:
		return 4
	}
}

// ExtraField preserves any custom YAML frontmatter keys on round-trip edits.
type ExtraField struct {
	Key   string
	Value string
}

// Frontmatter represents Claude Code's 3-field YAML frontmatter header.
type Frontmatter struct {
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Type        MemoryType   `json:"type"`
	Extra       []ExtraField `json:"-"`
}

// Entry represents a full topic memory file read from disk.
type Entry struct {
	Scope        Scope      `json:"scope"`
	File         string     `json:"file"`
	Path         string     `json:"path"`
	Name         string     `json:"name"`
	Description  string     `json:"description"`
	Type         MemoryType `json:"type"`
	Body         string     `json:"body"`
	ETag         string     `json:"etag"`
	ModTime      time.Time  `json:"mod_time"`
	Age          string     `json:"age"`
	AgeDays      int        `json:"age_days"`
	StaleWarning string     `json:"stale_warning,omitempty"`
	Indexed      bool       `json:"indexed"`
	HasHeader    bool       `json:"has_frontmatter"`
}

// ManifestItem is the lightweight frontmatter-only representation of a memory file.
type ManifestItem struct {
	Scope       Scope      `json:"scope"`
	File        string     `json:"file"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Type        MemoryType `json:"type"`
	ETag        string     `json:"etag"`
	Age         string     `json:"age"`
	AgeDays     int        `json:"age_days"`
	Indexed     bool       `json:"indexed"`
}

// ScopeIndex holds the rendered MEMORY.md index and manifest diagnostics for one scope.
type ScopeIndex struct {
	Scope         Scope          `json:"scope"`
	Dir           string         `json:"dir"`
	Exists        bool           `json:"exists"`
	IndexContent  string         `json:"index_content"`
	TotalLines    int            `json:"total_lines"`
	TotalBytes    int            `json:"total_bytes"`
	Truncated     bool           `json:"truncated"`
	OmittedLines  int            `json:"omitted_lines,omitempty"`
	Warning       string         `json:"warning,omitempty"`
	Unindexed     []ManifestItem `json:"unindexed,omitempty"`
	BrokenLinks   []string       `json:"broken_links,omitempty"`
	Manifest      []ManifestItem `json:"manifest,omitempty"`
	TopicCount    int            `json:"topic_count"`
}

// IndexResult aggregates global and project indexes.
type IndexResult struct {
	InProject bool        `json:"in_project"`
	RepoID    string      `json:"repo_id,omitempty"`
	Shared    bool        `json:"shared_mode,omitempty"`
	Global    *ScopeIndex `json:"global,omitempty"`
	Project   *ScopeIndex `json:"project,omitempty"`
}

// WriteRequest carries parameters for creating or updating a memory file.
type WriteRequest struct {
	Scope       Scope
	Type        MemoryType
	Name        string
	Description string
	Content     string
	File        string // Optional: target filename when updating an existing topic file
	ETag        string // Required when overwriting an existing file
	AllowSecret bool   // Explicit CLI-only override; always false in MCP
}

// WriteResult confirms what was written and synced to MEMORY.md.
type WriteResult struct {
	Status       string `json:"status"` // "created", "updated", "unchanged"
	Scope        Scope  `json:"scope"`
	File         string `json:"file"`
	Path         string `json:"path"`
	ETag         string `json:"etag"`
	ContentEcho  string `json:"content_echo"`
	IndexLines   int    `json:"index_lines"`
	IndexBytes   int    `json:"index_bytes"`
	IndexWarning string `json:"index_warning,omitempty"`
}

// DeleteRequest identifies an exact memory file and version to remove.
type DeleteRequest struct {
	Scope Scope
	File  string
	ETag  string
}

// SearchOptions configures fast pure-Go filesystem search.
type SearchOptions struct {
	Query string
	Scope string // "all", "project", "global"
	Type  string // optional type filter
	Limit int
}

// SearchMatch represents a ranked search hit.
type SearchMatch struct {
	Scope        Scope      `json:"scope"`
	File         string     `json:"file"`
	Name         string     `json:"name"`
	Description  string     `json:"description"`
	Type         MemoryType `json:"type"`
	Snippet      string     `json:"snippet"`
	Score        float64    `json:"score"`
	ETag         string     `json:"etag"`
	Age          string     `json:"age"`
	StaleWarning string     `json:"stale_warning,omitempty"`
}

// CheckIssue describes a single health diagnostic finding.
type CheckIssue struct {
	Scope    Scope  `json:"scope"`
	Severity string `json:"severity"` // "error", "warning", "info"
	Code     string `json:"code"`
	File     string `json:"file,omitempty"`
	Message  string `json:"message"`
}

// CheckReport summarizes health diagnostics across scopes.
type CheckReport struct {
	Healthy bool         `json:"healthy"`
	Fixed   bool         `json:"fixed,omitempty"`
	Issues  []CheckIssue `json:"issues"`
}
