package memdir

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"smem/internal/guard"
	"smem/internal/workspace"
)

// Store manages Claude-Code-compatible pure Markdown memory directories across 2 scopes.
type Store struct {
	Layout workspace.Layout
	Now    func() time.Time
}

// NewStore constructs a Store bound to the given workspace layout.
func NewStore(layout workspace.Layout) *Store {
	return &Store{Layout: layout}
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Store) dirForScope(scope Scope) string {
	switch scope {
	case ScopeGlobal:
		return s.Layout.GlobalDir
	case ScopeProject:
		return s.Layout.ProjectDir()
	default:
		return ""
	}
}

func (s *Store) requireDir(scope Scope) (string, error) {
	if scope == ScopeProject && !s.Layout.InProject {
		return "", errors.New("当前目录不在 Git 仓库中，无法操作 project 记忆；请进入项目目录，或显式指定 global 作用域")
	}
	dir := s.dirForScope(scope)
	if dir == "" {
		return "", fmt.Errorf("无法解析作用域 %q 的存储目录", scope)
	}
	return dir, nil
}

func (s *Store) resolveScopeList(filter string) ([]Scope, error) {
	switch strings.ToLower(strings.TrimSpace(filter)) {
	case "", "all":
		if s.Layout.InProject {
			return []Scope{ScopeProject, ScopeGlobal}, nil
		}
		return []Scope{ScopeGlobal}, nil
	case "project":
		if !s.Layout.InProject {
			return nil, errors.New("当前目录不在 Git 仓库中，没有可用的 project 记忆范围")
		}
		return []Scope{ScopeProject}, nil
	case "global":
		return []Scope{ScopeGlobal}, nil
	default:
		return nil, fmt.Errorf("无效的 scope 过滤值 %q（支持 all、project、global）", filter)
	}
}

// Index returns MEMORY.md index content and manifest diagnostics across requested scopes.
func (s *Store) Index(scopeFilter string) (IndexResult, error) {
	scopes, err := s.resolveScopeList(scopeFilter)
	if err != nil {
		return IndexResult{}, err
	}
	now := s.now()
	res := IndexResult{
		InProject: s.Layout.InProject,
		RepoID:    s.Layout.RepoID,
		Shared:    s.Layout.SharedMode,
	}
	for _, sc := range scopes {
		idx, err := loadScopeIndex(sc, s.dirForScope(sc), now)
		if err != nil {
			return IndexResult{}, err
		}
		if sc == ScopeGlobal {
			res.Global = &idx
		} else {
			res.Project = &idx
		}
	}
	return res, nil
}

// RenderCombinedIndex renders a concise, LLM-ready Markdown view of MEMORY.md index(es),
// including any self-healed unindexed topic files.
func (s *Store) RenderCombinedIndex(scopeFilter string) (string, IndexResult, error) {
	res, err := s.Index(scopeFilter)
	if err != nil {
		return "", res, err
	}

	var sections []string
	renderOne := func(title string, idx *ScopeIndex) {
		if idx == nil {
			return
		}
		if !idx.Exists || (strings.TrimSpace(idx.IndexContent) == "" && len(idx.Unindexed) == 0) {
			return
		}
		var b strings.Builder
		b.WriteString(fmt.Sprintf("## %s (%s)\n", title, idx.Scope))
		if strings.TrimSpace(idx.IndexContent) != "" {
			b.WriteString(strings.TrimSpace(idx.IndexContent))
			b.WriteString("\n")
		}
		if len(idx.Unindexed) > 0 {
			b.WriteString("\n### 未登记到 MEMORY.md 的主题文件\n")
			for _, u := range idx.Unindexed {
				b.WriteString(FormatIndexLine(u.Name, u.File, u.Description))
				b.WriteString("\n")
			}
		}
		if idx.Warning != "" && !idx.Truncated {
			b.WriteString("\n> " + idx.Warning + "\n")
		}
		sections = append(sections, strings.TrimSpace(b.String()))
	}

	renderOne("全局记忆", res.Global)
	renderOne("当前项目记忆", res.Project)

	if len(sections) == 0 {
		return "", res, nil
	}
	return strings.Join(sections, "\n\n"), res, nil
}

// Read loads 1..N topic files in a scope, attaching ETag, human-readable relative Age,
// and Claude-style staleness verification caveats.
func (s *Store) Read(scope Scope, files []string) ([]Entry, error) {
	dir, err := s.requireDir(scope)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, errors.New("请至少指定一个要读取的记忆文件名")
	}

	// Build indexed set quickly for Entry.Indexed metadata.
	indexedSet := make(map[string]bool)
	if idxRaw, err := os.ReadFile(filepath.Join(dir, IndexFileName)); err == nil {
		for _, f := range extractLinkedFiles(string(idxRaw)) {
			indexedSet[f] = true
		}
	}

	now := s.now()
	entries := make([]Entry, 0, len(files))
	for _, file := range files {
		file = strings.TrimSpace(file)
		if err := guard.ValidateMemoryFilename(file); err != nil {
			return nil, err
		}
		fullPath := filepath.Join(dir, file)
		if err := guard.CheckPathSafe(dir, fullPath); err != nil {
			return nil, err
		}
		raw, err := os.ReadFile(fullPath)
		if err != nil {
			if isNotExist(err) {
				return nil, fmt.Errorf("记忆文件不存在：%s/%s", scope, file)
			}
			return nil, err
		}
		info, err := os.Stat(fullPath)
		if err != nil {
			return nil, err
		}
		fm, body, hasFM := ParseDocument(raw, file)
		ageLabel, ageDays, staleWarn := FormatAge(info.ModTime(), now)
		entries = append(entries, Entry{
			Scope:        scope,
			File:         file,
			Path:         fullPath,
			Name:         fm.Name,
			Description:  fm.Description,
			Type:         fm.Type,
			Body:         body,
			ETag:         ComputeETag(raw),
			ModTime:      info.ModTime(),
			Age:          ageLabel,
			AgeDays:      ageDays,
			StaleWarning: staleWarn,
			Indexed:      indexedSet[file],
			HasHeader:    hasFM,
		})
	}
	return entries, nil
}

// Write atomically creates or updates a topic Markdown file and synchronizes MEMORY.md
// under an exclusive OS file lock.
func (s *Store) Write(req WriteRequest) (WriteResult, error) {
	dir, err := s.requireDir(req.Scope)
	if err != nil {
		return WriteResult{}, err
	}
	if req.Type == "" {
		if req.Scope == ScopeGlobal {
			req.Type = TypeUser
		} else {
			req.Type = TypeProject
		}
	}
	if _, err := ParseMemoryType(string(req.Type)); err != nil {
		return WriteResult{}, err
	}

	name := singleLine(req.Name)
	if name == "" {
		return WriteResult{}, errors.New("记忆标题 name 不能为空")
	}
	desc := singleLine(req.Description)
	if desc == "" {
		desc = fallbackDescription(req.Content)
	}
	if desc == "" {
		return WriteResult{}, errors.New("记忆触发说明 description 不能为空")
	}

	body := guard.StripPrivate(req.Content)
	if body == "" {
		return WriteResult{}, errors.New("记忆正文 content 不能为空（或仅包含被剥离的 <private> 内容）")
	}

	if !req.AllowSecret {
		combined := name + "\n" + desc + "\n" + body
		if err := guard.CheckNoSecrets(combined); err != nil {
			return WriteResult{}, err
		}
	}

	if err := os.MkdirAll(dir, 0700); err != nil {
		return WriteResult{}, err
	}
	lock, err := acquireDirLock(dir)
	if err != nil {
		return WriteResult{}, fmt.Errorf("获取写锁失败：%w", err)
	}
	defer lock.Release()

	targetFile := strings.TrimSpace(req.File)
	oldFileToRemove := ""
	status := "created"
	var extraFields []ExtraField

	if targetFile != "" {
		if err := guard.ValidateMemoryFilename(targetFile); err != nil {
			return WriteResult{}, err
		}
		fullPath := filepath.Join(dir, targetFile)
		if err := guard.CheckPathSafe(dir, fullPath); err != nil {
			return WriteResult{}, err
		}
		if existingRaw, err := os.ReadFile(fullPath); err == nil {
			currentETag := ComputeETag(existingRaw)
			if strings.TrimSpace(req.ETag) == "" {
				return WriteResult{}, fmt.Errorf("更新已有记忆 %s 必须提供最新 etag（当前 etag=%s）；请先读取该文件再更新", targetFile, currentETag)
			}
			if strings.TrimSpace(req.ETag) != currentETag {
				return WriteResult{}, fmt.Errorf("版本冲突：%s 已被其他工具或人工修改（传入 etag=%s，当前 etag=%s）；请重新读取后再更新", targetFile, req.ETag, currentETag)
			}
			oldFM, _, _ := ParseDocument(existingRaw, targetFile)
			extraFields = oldFM.Extra
			status = "updated"

			// If the title changed and the old filename was auto-derived from the old title,
			// migrate to a fresh slug filename so obsolete values do not linger in the filename.
			oldAutoSlug := guard.SlugifyFilename(string(oldFM.Type), oldFM.Name)
			newAutoSlug := guard.SlugifyFilename(string(req.Type), name)
			if targetFile == oldAutoSlug && newAutoSlug != targetFile {
				oldFileToRemove = targetFile
				targetFile = allocateUniqueFilename(dir, string(req.Type), name)
			}
		} else if !isNotExist(err) {
			return WriteResult{}, err
		}
	} else {
		// Creating a new memory: detect exact duplicates or same-name existing topic files.
		existing, dupErr := s.findExistingMatchLocked(dir, name, body)
		if dupErr != nil {
			return WriteResult{}, dupErr
		}
		if existing != nil {
			if existing.exactBody {
				// Idempotent reuse: ensure it is indexed in MEMORY.md.
				stats, err := upsertIndexEntryLocked(dir, req.Scope, existing.file, existing.name, existing.desc)
				if err != nil {
					return WriteResult{}, err
				}
				return WriteResult{
					Status:       "unchanged",
					Scope:        req.Scope,
					File:         existing.file,
					Path:         filepath.Join(dir, existing.file),
					ETag:         existing.etag,
					ContentEcho:  body,
					IndexLines:   stats.Lines,
					IndexBytes:   stats.Bytes,
					IndexWarning: stats.Warning,
				}, nil
			}
			return WriteResult{}, fmt.Errorf("同作用域已存在同名记忆 %s（etag=%s）；若要更正请先 memory_read 后携带 file 与 etag 更新，避免产生重复冲突条目", existing.file, existing.etag)
		}

		targetFile = allocateUniqueFilename(dir, string(req.Type), name)
	}

	fullPath := filepath.Join(dir, targetFile)
	if err := guard.CheckPathSafe(dir, fullPath); err != nil {
		return WriteResult{}, err
	}

	fm := Frontmatter{
		Name:        name,
		Description: desc,
		Type:        req.Type,
		Extra:       extraFields,
	}
	rendered := RenderDocument(fm, body)
	if err := atomicWriteFile(fullPath, rendered, 0600); err != nil {
		return WriteResult{}, err
	}
	if oldFileToRemove != "" && oldFileToRemove != targetFile {
		_ = os.Remove(filepath.Join(dir, oldFileToRemove))
		_ = removeIndexEntryLocked(dir, oldFileToRemove)
	}

	stats, err := upsertIndexEntryLocked(dir, req.Scope, targetFile, name, desc)
	if err != nil {
		return WriteResult{}, err
	}

	return WriteResult{
		Status:       status,
		Scope:        req.Scope,
		File:         targetFile,
		Path:         fullPath,
		ETag:         ComputeETag(rendered),
		ContentEcho:  body,
		IndexLines:   stats.Lines,
		IndexBytes:   stats.Bytes,
		IndexWarning: stats.Warning,
	}, nil
}

// Delete removes a topic memory file after verifying ETag and atomically updates MEMORY.md.
func (s *Store) Delete(req DeleteRequest) error {
	dir, err := s.requireDir(req.Scope)
	if err != nil {
		return err
	}
	file := strings.TrimSpace(req.File)
	if err := guard.ValidateMemoryFilename(file); err != nil {
		return err
	}
	if strings.TrimSpace(req.ETag) == "" {
		return errors.New("删除记忆必须提供 etag；请先读取该条目核对后再删除")
	}

	fullPath := filepath.Join(dir, file)
	if err := guard.CheckPathSafe(dir, fullPath); err != nil {
		return err
	}

	lock, err := acquireDirLock(dir)
	if err != nil {
		return fmt.Errorf("获取写锁失败：%w", err)
	}
	defer lock.Release()

	raw, err := os.ReadFile(fullPath)
	if err != nil {
		if isNotExist(err) {
			return fmt.Errorf("记忆文件不存在：%s/%s", req.Scope, file)
		}
		return err
	}
	currentETag := ComputeETag(raw)
	if strings.TrimSpace(req.ETag) != currentETag {
		return fmt.Errorf("版本冲突：%s 已被修改（传入 etag=%s，当前 etag=%s）；请重新读取后再删除", file, req.ETag, currentETag)
	}

	if err := os.Remove(fullPath); err != nil {
		return err
	}
	return removeIndexEntryLocked(dir, file)
}

// RebuildIndex reconstructs MEMORY.md cleanly from all topic files in the given scope(s).
func (s *Store) RebuildIndex(scopeFilter string) (map[Scope]IndexStats, error) {
	scopes, err := s.resolveScopeList(scopeFilter)
	if err != nil {
		return nil, err
	}
	now := s.now()
	out := make(map[Scope]IndexStats, len(scopes))
	for _, sc := range scopes {
		dir := s.dirForScope(sc)
		if dir == "" || !fileExists(dir) {
			continue
		}
		lock, err := acquireDirLock(dir)
		if err != nil {
			return nil, err
		}
		stats, rebuildErr := rebuildIndexLocked(dir, sc, now)
		lock.Release()
		if rebuildErr != nil {
			return nil, rebuildErr
		}
		out[sc] = stats
	}
	return out, nil
}

// Check runs deterministic health diagnostics (no LLM required) and optionally repairs MEMORY.md.
func (s *Store) Check(scopeFilter string, fix bool) (CheckReport, error) {
	scopes, err := s.resolveScopeList(scopeFilter)
	if err != nil {
		return CheckReport{}, err
	}
	now := s.now()
	report := CheckReport{Healthy: true}

	for _, sc := range scopes {
		dir := s.dirForScope(sc)
		if dir == "" || !fileExists(dir) {
			continue
		}
		idx, err := loadScopeIndex(sc, dir, now)
		if err != nil {
			return CheckReport{}, err
		}

		needRebuild := false
		for _, u := range idx.Unindexed {
			needRebuild = true
			report.Issues = append(report.Issues, CheckIssue{
				Scope:    sc,
				Severity: "warning",
				Code:     "unindexed_file",
				File:     u.File,
				Message:  fmt.Sprintf("主题文件 %s 未登记在 MEMORY.md 中", u.File),
			})
		}
		for _, bl := range idx.BrokenLinks {
			needRebuild = true
			report.Issues = append(report.Issues, CheckIssue{
				Scope:    sc,
				Severity: "warning",
				Code:     "broken_index_link",
				File:     bl,
				Message:  fmt.Sprintf("MEMORY.md 引用了不存在的文件 %s", bl),
			})
		}
		if idx.TotalLines > MaxIndexLines || idx.TotalBytes > MaxIndexBytes {
			report.Healthy = false
			report.Issues = append(report.Issues, CheckIssue{
				Scope:    sc,
				Severity: "error",
				Code:     "index_overflow",
				File:     IndexFileName,
				Message:  fmt.Sprintf("MEMORY.md 超出上限（%d 行 / %d 字节），建议运行 dream 整理", idx.TotalLines, idx.TotalBytes),
			})
		}

		// Inspect each topic file for missing frontmatter, extreme age (>90d), or secrets.
		for _, item := range idx.Manifest {
			fullPath := filepath.Join(dir, item.File)
			raw, err := os.ReadFile(fullPath)
			if err != nil {
				continue
			}
			fm, _, hasFM := ParseDocument(raw, item.File)
			if !hasFM {
				report.Issues = append(report.Issues, CheckIssue{
					Scope:    sc,
					Severity: "info",
					Code:     "missing_frontmatter",
					File:     item.File,
					Message:  fmt.Sprintf("%s 缺少 YAML frontmatter（已兼容读取，建议补齐 name/description/type）", item.File),
				})
			} else if fm.Type == TypeUntyped {
				report.Issues = append(report.Issues, CheckIssue{
					Scope:    sc,
					Severity: "warning",
					Code:     "invalid_type",
					File:     item.File,
					Message:  fmt.Sprintf("%s 的 type 不在标准 4 类（user/feedback/project/reference）中", item.File),
				})
			}
			if item.AgeDays >= 90 {
				report.Issues = append(report.Issues, CheckIssue{
					Scope:    sc,
					Severity: "info",
					Code:     "stale_memory",
					File:     item.File,
					Message:  fmt.Sprintf("%s 已 %d 天未更新，建议确认是否仍有效", item.File, item.AgeDays),
				})
			}
			if findings := guard.ScanSecrets(string(raw)); len(findings) > 0 {
				report.Healthy = false
				report.Issues = append(report.Issues, CheckIssue{
					Scope:    sc,
					Severity: "error",
					Code:     "potential_secret",
					File:     item.File,
					Message:  fmt.Sprintf("%s 检测到疑似敏感凭证（%s，第 %d 行），请立即清理！", item.File, findings[0].Rule, findings[0].Line),
				})
			}
		}

		if fix && needRebuild {
			lock, err := acquireDirLock(dir)
			if err != nil {
				return CheckReport{}, err
			}
			_, rebuildErr := rebuildIndexLocked(dir, sc, now)
			lock.Release()
			if rebuildErr != nil {
				return CheckReport{}, rebuildErr
			}
			report.Fixed = true
		}
	}

	for _, iss := range report.Issues {
		if iss.Severity == "error" {
			report.Healthy = false
			break
		}
	}
	return report, nil
}

type existingMatch struct {
	file      string
	name      string
	desc      string
	etag      string
	exactBody bool
}

func (s *Store) findExistingMatchLocked(dir, name, body string) (*existingMatch, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if isNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	normName := strings.ToLower(singleLine(name))
	normBody := normalizeForDedup(body)
	var nameHit *existingMatch

	for _, de := range entries {
		if !isRegularMarkdown(de) {
			continue
		}
		file := de.Name()
		raw, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil {
			continue
		}
		fm, existingBody, _ := ParseDocument(raw, file)
		if normalizeForDedup(existingBody) == normBody {
			return &existingMatch{
				file:      file,
				name:      fm.Name,
				desc:      fm.Description,
				etag:      ComputeETag(raw),
				exactBody: true,
			}, nil
		}
		if strings.ToLower(singleLine(fm.Name)) == normName && nameHit == nil {
			nameHit = &existingMatch{
				file: file,
				name: fm.Name,
				desc: fm.Description,
				etag: ComputeETag(raw),
			}
		}
	}
	return nameHit, nil
}

func normalizeForDedup(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

func allocateUniqueFilename(dir, memType, name string) string {
	base := guard.SlugifyFilename(memType, name)
	if !fileExists(filepath.Join(dir, base)) {
		return base
	}
	stem := strings.TrimSuffix(base, ".md")
	for i := 2; i < 1000; i++ {
		candidate := fmt.Sprintf("%s_%d.md", stem, i)
		if !fileExists(filepath.Join(dir, candidate)) {
			return candidate
		}
	}
	return fmt.Sprintf("%s_%d.md", stem, time.Now().UnixNano())
}
