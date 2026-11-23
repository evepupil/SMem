package memdir

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	// IndexFileName matches Claude Code's exact entrypoint index filename.
	IndexFileName = "MEMORY.md"
	// MaxIndexLines matches Claude Code's 200-line entrypoint limit.
	MaxIndexLines = 200
	// MaxIndexBytes matches Claude Code's 25KB entrypoint limit.
	MaxIndexBytes = 25 * 1024
)

var (
	mdLinkRe   = regexp.MustCompile(`\[[^\]]*\]\(([^)\s]+)\)`)
	wikiLinkRe = regexp.MustCompile(`\[\[([^\[\]\r\n]+)\]\]`)
)

// IndexStats summarizes MEMORY.md size against Claude Code's 200-line / 25KB limits.
type IndexStats struct {
	Lines   int
	Bytes   int
	Warning string
}

// FormatIndexLine builds a clean single-line MEMORY.md pointer matching Claude Code's convention.
func FormatIndexLine(name, file, desc string) string {
	n := singleLine(name)
	if n == "" {
		n = strings.TrimSuffix(file, filepath.Ext(file))
	}
	d := singleLine(desc)
	runes := []rune(d)
	if len(runes) > 140 {
		d = string(runes[:140]) + "…"
	}
	if d == "" {
		return fmt.Sprintf("- [%s](%s)", n, file)
	}
	return fmt.Sprintf("- [%s](%s) — %s", n, file, d)
}

func indexStatsFromBytes(raw []byte) IndexStats {
	text := strings.TrimRight(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	lines := 0
	if text != "" {
		lines = strings.Count(text, "\n") + 1
	}
	nbytes := len(raw)
	warning := ""
	switch {
	case lines > MaxIndexLines || nbytes > MaxIndexBytes:
		warning = fmt.Sprintf("MEMORY.md 已超出上限（%d 行 / %d 字节，上限 %d 行 / %d 字节），建议运行 dream 整理精简索引。", lines, nbytes, MaxIndexLines, MaxIndexBytes)
	case lines >= MaxIndexLines-20 || nbytes >= MaxIndexBytes-2048:
		warning = fmt.Sprintf("MEMORY.md 接近上限（%d/%d 行，%d/%d 字节），建议适时运行 dream 整理。", lines, MaxIndexLines, nbytes, MaxIndexBytes)
	}
	return IndexStats{Lines: lines, Bytes: nbytes, Warning: warning}
}

func defaultIndexHeader(scope Scope) string {
	if scope == ScopeGlobal {
		return "# Global Memory Index"
	}
	return "# Project Memory Index"
}

// upsertIndexEntryLocked atomically inserts or replaces a single pointer line in MEMORY.md.
// Caller must hold dirLock on dir.
func upsertIndexEntryLocked(dir string, scope Scope, file, name, desc string) (IndexStats, error) {
	indexPath := filepath.Join(dir, IndexFileName)
	newLine := FormatIndexLine(name, file, desc)

	var lines []string
	raw, err := os.ReadFile(indexPath)
	if err != nil {
		if !isNotExist(err) {
			return IndexStats{}, err
		}
		lines = []string{defaultIndexHeader(scope), "", newLine}
	} else {
		cleaned := strings.ReplaceAll(string(bytes.TrimPrefix(raw, utf8BOM)), "\r\n", "\n")
		lines = strings.Split(strings.TrimRight(cleaned, "\n"), "\n")
		replaced := false
		var out []string
		for _, line := range lines {
			if lineReferencesFile(line, file) {
				if !replaced {
					out = append(out, newLine)
					replaced = true
				}
				continue
			}
			out = append(out, line)
		}
		if !replaced {
			if len(out) == 0 || (len(out) == 1 && strings.TrimSpace(out[0]) == "") {
				out = []string{defaultIndexHeader(scope), "", newLine}
			} else {
				out = append(out, newLine)
			}
		}
		lines = out
	}

	rendered := []byte(strings.Join(lines, "\n") + "\n")
	if err := atomicWriteFile(indexPath, rendered, 0600); err != nil {
		return IndexStats{}, err
	}
	return indexStatsFromBytes(rendered), nil
}

// removeIndexEntryLocked removes all pointer lines referencing file from MEMORY.md.
// Caller must hold dirLock on dir.
func removeIndexEntryLocked(dir, file string) error {
	indexPath := filepath.Join(dir, IndexFileName)
	raw, err := os.ReadFile(indexPath)
	if err != nil {
		if isNotExist(err) {
			return nil
		}
		return err
	}
	cleaned := strings.ReplaceAll(string(bytes.TrimPrefix(raw, utf8BOM)), "\r\n", "\n")
	lines := strings.Split(strings.TrimRight(cleaned, "\n"), "\n")
	var out []string
	for _, line := range lines {
		if lineReferencesFile(line, file) {
			continue
		}
		out = append(out, line)
	}
	rendered := []byte(strings.Join(out, "\n") + "\n")
	return atomicWriteFile(indexPath, rendered, 0600)
}

// rebuildIndexLocked regenerates MEMORY.md cleanly from top-level topic .md files in dir,
// preserving sub-files already organized under Hub overview files via Markdown links or [[wikilinks]].
// Caller must hold dirLock on dir.
func rebuildIndexLocked(dir string, scope Scope, now time.Time) (IndexStats, error) {
	docs, err := loadDirectoryDocs(scope, dir, now)
	if err != nil {
		return IndexStats{}, err
	}

	existingDirect := make(map[string]bool)
	if idxRaw, err := os.ReadFile(filepath.Join(dir, IndexFileName)); err == nil {
		for _, lf := range ExtractLinkedFiles(string(idxRaw)) {
			existingDirect[lf] = true
		}
	}

	subArchived := computeRebuildSubArchived(docs, existingDirect)

	var items []ManifestItem
	for _, d := range docs {
		if subArchived[d.item.File] {
			continue
		}
		items = append(items, d.item)
	}

	// Sort: pinned/critical rules first within type, then MemoryType group, then newest first.
	sort.SliceStable(items, func(i, j int) bool {
		pi := isPinnedCue(items[i].Name) || isPinnedCue(items[i].Description)
		pj := isPinnedCue(items[j].Name) || isPinnedCue(items[j].Description)
		if pi != pj {
			return pi
		}
		oi, oj := typeOrder(items[i].Type), typeOrder(items[j].Type)
		if oi != oj {
			return oi < oj
		}
		if items[i].AgeDays != items[j].AgeDays {
			return items[i].AgeDays < items[j].AgeDays
		}
		return items[i].File < items[j].File
	})

	var lines []string
	lines = append(lines, defaultIndexHeader(scope), "")
	for _, item := range items {
		lines = append(lines, FormatIndexLine(item.Name, item.File, item.Description))
	}

	rendered := []byte(strings.Join(lines, "\n") + "\n")
	indexPath := filepath.Join(dir, IndexFileName)
	if err := atomicWriteFile(indexPath, rendered, 0600); err != nil {
		return IndexStats{}, err
	}
	return indexStatsFromBytes(rendered), nil
}

func computeRebuildSubArchived(docs []loadedTopicDoc, existingDirect map[string]bool) map[string]bool {
	byFile := make(map[string]loadedTopicDoc, len(docs))
	inDegree := make(map[string]int, len(docs))
	adj := make(map[string][]string, len(docs))

	for _, d := range docs {
		byFile[d.item.File] = d
	}
	for _, d := range docs {
		src := d.item.File
		for _, target := range ExtractLinkedFiles(d.body) {
			if target == src {
				continue
			}
			if _, ok := byFile[target]; ok {
				adj[src] = append(adj[src], target)
				inDegree[target]++
			}
		}
	}

	// Roots are files already explicitly placed in MEMORY.md OR files not referenced by any sibling topic file.
	var roots []string
	rootSet := make(map[string]bool)
	for _, d := range docs {
		f := d.item.File
		if existingDirect[f] || inDegree[f] == 0 {
			roots = append(roots, f)
			rootSet[f] = true
		}
	}

	visited := make(map[string]bool)
	subArchived := make(map[string]bool)
	bfs := func(start string) {
		queue := []string{start}
		visited[start] = true
		for len(queue) > 0 {
			curr := queue[0]
			queue = queue[1:]
			for _, child := range adj[curr] {
				if !existingDirect[child] {
					subArchived[child] = true
				}
				if !visited[child] {
					visited[child] = true
					queue = append(queue, child)
				}
			}
		}
	}

	for _, r := range roots {
		if !visited[r] {
			bfs(r)
		}
	}
	// Guarantee cycle safety: if a pure cycle had no external root, keep its first file in MEMORY.md.
	for _, d := range docs {
		f := d.item.File
		if !visited[f] && !rootSet[f] {
			delete(subArchived, f)
			bfs(f)
		}
	}
	return subArchived
}

func lineReferencesFile(line, file string) bool {
	for _, target := range ExtractLinkedFiles(line) {
		if target == file {
			return true
		}
	}
	return false
}

// ExtractLinkedFiles parses both Markdown links `[label](topic.md)` and Obsidian-style
// WikiLinks `[[topic]]`, `[[topic.md]]`, `[[topic|alias]]`, `[[topic#heading]]`,
// returning normalized single-level `.md` filenames (excluding MEMORY.md and external URLs).
func ExtractLinkedFiles(content string) []string {
	stripped := stripFencedBlocksForLinks(content)
	seen := make(map[string]bool)
	var files []string

	add := func(target string) {
		if target == "" || strings.EqualFold(target, IndexFileName) {
			return
		}
		if !seen[target] {
			seen[target] = true
			files = append(files, target)
		}
	}

	for _, m := range mdLinkRe.FindAllStringSubmatch(stripped, -1) {
		if len(m) < 2 {
			continue
		}
		target := normalizeMarkdownLinkTarget(m[1])
		if target != "" {
			add(target)
		}
	}

	for _, m := range wikiLinkRe.FindAllStringSubmatch(stripped, -1) {
		if len(m) < 2 {
			continue
		}
		target := normalizeWikiLinkTarget(m[1])
		if target != "" {
			add(target)
		}
	}

	return files
}

func normalizeMarkdownLinkTarget(raw string) string {
	target := strings.TrimSpace(raw)
	if strings.Contains(target, "://") {
		return ""
	}
	target = strings.TrimPrefix(target, "./")
	if idx := strings.IndexByte(target, '#'); idx >= 0 {
		target = target[:idx]
	}
	if idx := strings.IndexByte(target, '?'); idx >= 0 {
		target = target[:idx]
	}
	target = strings.TrimSpace(target)
	if target == "" || strings.ContainsAny(target, `/\`) {
		return ""
	}
	if !strings.HasSuffix(strings.ToLower(target), ".md") {
		return ""
	}
	return target
}

func normalizeWikiLinkTarget(raw string) string {
	target := strings.TrimSpace(raw)
	if pipe := strings.IndexByte(target, '|'); pipe >= 0 {
		target = target[:pipe]
	}
	if hash := strings.IndexByte(target, '#'); hash >= 0 {
		target = target[:hash]
	}
	target = strings.TrimSpace(strings.TrimPrefix(target, "./"))
	if target == "" || strings.Contains(target, "://") || strings.ContainsAny(target, `/\`) {
		return ""
	}
	if !strings.HasSuffix(strings.ToLower(target), ".md") {
		target += ".md"
	}
	return target
}

func stripFencedBlocksForLinks(s string) string {
	lines := strings.Split(s, "\n")
	var out []string
	inFence := false
	fenceMarker := ""
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			marker := trimmed[:3]
			if !inFence {
				inFence = true
				fenceMarker = marker
				continue
			}
			if marker == fenceMarker {
				inFence = false
				fenceMarker = ""
				continue
			}
		}
		if !inFence {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

type loadedTopicDoc struct {
	item ManifestItem
	body string
}

// loadScopeIndex reads MEMORY.md with priority-aware 200-line / 25KB budgeting and
// traverses Hub links (both Markdown links and [[WikiLinks]]) so hierarchical sub-memories
// are recognized without bloating root context or triggering false unindexed warnings.
func loadScopeIndex(scope Scope, dir string, now time.Time) (ScopeIndex, error) {
	res := ScopeIndex{
		Scope: scope,
		Dir:   dir,
	}
	if dir == "" {
		return res, nil
	}
	info, err := os.Stat(dir)
	if err != nil {
		if isNotExist(err) {
			return res, nil
		}
		return res, err
	}
	if !info.IsDir() {
		return res, fmt.Errorf("记忆目录路径为非文件夹：%s", dir)
	}
	res.Exists = true

	docs, err := loadDirectoryDocs(scope, dir, now)
	if err != nil {
		return res, err
	}
	byFile := make(map[string]loadedTopicDoc, len(docs))
	typeByFile := make(map[string]MemoryType, len(docs))
	for _, d := range docs {
		byFile[d.item.File] = d
		typeByFile[d.item.File] = d.item.Type
	}

	indexPath := filepath.Join(dir, IndexFileName)
	directSet := make(map[string]bool)
	var directLinkedFiles []string

	if raw, err := os.ReadFile(indexPath); err == nil {
		cleaned := strings.ReplaceAll(string(bytes.TrimPrefix(raw, utf8BOM)), "\r\n", "\n")
		directLinkedFiles = ExtractLinkedFiles(cleaned)
		for _, lf := range directLinkedFiles {
			directSet[lf] = true
		}
		stats := indexStatsFromBytes(raw)
		res.TotalLines = stats.Lines
		res.TotalBytes = stats.Bytes
		res.Warning = stats.Warning

		bounded, truncated, omitted := truncateIndexWithPriority(cleaned, typeByFile, MaxIndexLines, MaxIndexBytes)
		res.IndexContent = bounded
		res.Truncated = truncated
		res.OmittedLines = omitted
	} else if !isNotExist(err) {
		return res, err
	}

	// BFS transitive Hub traversal from directly indexed files.
	subIndexedSet := make(map[string]bool)
	var queue []string
	for _, lf := range directLinkedFiles {
		if _, ok := byFile[lf]; ok {
			queue = append(queue, lf)
		}
	}
	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]
		doc, ok := byFile[curr]
		if !ok {
			continue
		}
		for _, child := range ExtractLinkedFiles(doc.body) {
			if _, exists := byFile[child]; !exists {
				continue
			}
			if directSet[child] || subIndexedSet[child] {
				continue
			}
			subIndexedSet[child] = true
			queue = append(queue, child)
		}
	}

	res.SubIndexedCount = len(subIndexedSet)
	manifest := make([]ManifestItem, 0, len(docs))
	for _, d := range docs {
		it := d.item
		f := it.File
		it.Indexed = directSet[f] || subIndexedSet[f]
		it.SubIndexed = subIndexedSet[f]
		manifest = append(manifest, it)
		if !it.Indexed {
			res.Unindexed = append(res.Unindexed, it)
		}
	}
	res.Manifest = manifest
	res.TopicCount = len(manifest)

	for _, lf := range directLinkedFiles {
		if _, exists := byFile[lf]; !exists {
			res.BrokenLinks = append(res.BrokenLinks, lf)
		}
	}

	return res, nil
}

func loadDirectoryDocs(scope Scope, dir string, now time.Time) ([]loadedTopicDoc, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if isNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var docs []loadedTopicDoc
	for _, de := range entries {
		if !isRegularMarkdown(de) {
			continue
		}
		file := de.Name()
		fullPath := filepath.Join(dir, file)
		raw, err := os.ReadFile(fullPath)
		if err != nil {
			continue
		}
		info, err := de.Info()
		if err != nil {
			continue
		}
		fm, body, _ := ParseDocument(raw, file)
		ageLabel, ageDays, _ := FormatAge(info.ModTime(), now)
		docs = append(docs, loadedTopicDoc{
			item: ManifestItem{
				Scope:       scope,
				File:        file,
				Name:        fm.Name,
				Description: fm.Description,
				Type:        fm.Type,
				ETag:        ComputeETag(raw),
				Age:         ageLabel,
				AgeDays:     ageDays,
			},
			body: body,
		})
	}
	sort.Slice(docs, func(i, j int) bool {
		if docs[i].item.AgeDays != docs[j].item.AgeDays {
			return docs[i].item.AgeDays < docs[j].item.AgeDays
		}
		return docs[i].item.File < docs[j].item.File
	})
	return docs, nil
}

func isPinnedCue(s string) bool {
	low := strings.ToLower(s)
	for _, kw := range []string{"必须记", "铁律", "永不", "严禁", "[pinned]", "(must)", "must remember"} {
		if strings.Contains(low, kw) {
			return true
		}
	}
	return false
}

func isHighPriorityIndexLine(line string, typeByFile map[string]MemoryType) bool {
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "#") {
		return true
	}
	if isPinnedCue(trimmed) {
		return true
	}
	if len(typeByFile) > 0 {
		for _, f := range ExtractLinkedFiles(trimmed) {
			if typeByFile[f] == TypeFeedback {
				return true
			}
		}
	}
	return false
}

func truncateIndexNonSilent(content string, maxLines, maxBytes int) (bounded string, truncated bool, omitted int) {
	return truncateIndexWithPriority(content, nil, maxLines, maxBytes)
}

// truncateIndexWithPriority preserves original line order when within budget; when over budget,
// it reserves slots for headings, pinned redlines (e.g. `必须记`), and `feedback` rules first
// so critical guardrails at the bottom of MEMORY.md are never silently chopped off.
func truncateIndexWithPriority(content string, typeByFile map[string]MemoryType, maxLines, maxBytes int) (bounded string, truncated bool, omitted int) {
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return "", false, 0
	}

	totalBytes := len(content)
	if len(lines) <= maxLines && totalBytes <= maxBytes {
		return strings.Join(lines, "\n"), false, 0
	}

	keep := make([]bool, len(lines))
	keptLines := 0
	byteCount := 0

	trySelect := func(idx int) bool {
		if keep[idx] {
			return true
		}
		lineBytes := len(lines[idx]) + 1
		if keptLines >= maxLines || (byteCount+lineBytes > maxBytes && keptLines > 0) {
			return false
		}
		keep[idx] = true
		keptLines++
		byteCount += lineBytes
		return true
	}

	// Pass 1: preserve top title/headings and critical redline / feedback entries anywhere in MEMORY.md.
	for i, line := range lines {
		if isHighPriorityIndexLine(line, typeByFile) {
			_ = trySelect(i)
		}
	}

	// Pass 2: fill remaining budget in original top-to-bottom order.
	for i := range lines {
		if !keep[i] {
			_ = trySelect(i)
		}
	}

	var kept []string
	for i, line := range lines {
		if keep[i] {
			kept = append(kept, line)
		}
	}
	omitted = len(lines) - len(kept)
	truncated = omitted > 0
	out := strings.Join(kept, "\n")
	if truncated {
		out += fmt.Sprintf("\n\n> [SMem 索引提示] MEMORY.md 超出上限（%d 行 / %dKB），已优先保留核心红线与纠错规则并省略其余 %d 行。请调用 memory_search 检索，或运行 dream 整理精简。", maxLines, maxBytes/1024, omitted)
	}
	return out, truncated, omitted
}
