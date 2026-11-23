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

var mdLinkRe = regexp.MustCompile(`\[[^\]]*\]\(([^)\s]+)\)`)

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

// rebuildIndexLocked regenerates MEMORY.md cleanly from all topic .md files in dir.
// Caller must hold dirLock on dir.
func rebuildIndexLocked(dir string, scope Scope, now time.Time) (IndexStats, error) {
	items, err := scanDirectoryManifest(scope, dir, nil, now)
	if err != nil {
		return IndexStats{}, err
	}

	// Sort by MemoryType group (user -> feedback -> project -> reference -> untyped), then newest first.
	sort.SliceStable(items, func(i, j int) bool {
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

func lineReferencesFile(line, file string) bool {
	matches := mdLinkRe.FindAllStringSubmatch(line, -1)
	for _, m := range matches {
		if len(m) >= 2 && normalizeLinkTarget(m[1]) == file {
			return true
		}
	}
	return false
}

func extractLinkedFiles(content string) []string {
	seen := make(map[string]bool)
	var files []string
	for _, m := range mdLinkRe.FindAllStringSubmatch(content, -1) {
		if len(m) < 2 {
			continue
		}
		target := normalizeLinkTarget(m[1])
		if target == "" || strings.Contains(target, "://") || !strings.HasSuffix(strings.ToLower(target), ".md") {
			continue
		}
		if !seen[target] {
			seen[target] = true
			files = append(files, target)
		}
	}
	return files
}

func normalizeLinkTarget(raw string) string {
	target := strings.TrimSpace(raw)
	target = strings.TrimPrefix(target, "./")
	if idx := strings.IndexByte(target, '#'); idx >= 0 {
		target = target[:idx]
	}
	return target
}

// loadScopeIndex reads MEMORY.md with non-silent 200-line / 25KB budgeting and
// scans topic files to discover unindexed entries and broken links.
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
		return res, fmt.Errorf("记忆目录路径不是文件夹：%s", dir)
	}
	res.Exists = true

	indexPath := filepath.Join(dir, IndexFileName)
	indexedSet := make(map[string]bool)
	var linkedFiles []string

	if raw, err := os.ReadFile(indexPath); err == nil {
		cleaned := strings.ReplaceAll(string(bytes.TrimPrefix(raw, utf8BOM)), "\r\n", "\n")
		linkedFiles = extractLinkedFiles(cleaned)
		for _, lf := range linkedFiles {
			indexedSet[lf] = true
		}
		stats := indexStatsFromBytes(raw)
		res.TotalLines = stats.Lines
		res.TotalBytes = stats.Bytes
		res.Warning = stats.Warning

		bounded, truncated, omitted := truncateIndexNonSilent(cleaned, MaxIndexLines, MaxIndexBytes)
		res.IndexContent = bounded
		res.Truncated = truncated
		res.OmittedLines = omitted
	} else if !isNotExist(err) {
		return res, err
	}

	manifest, err := scanDirectoryManifest(scope, dir, indexedSet, now)
	if err != nil {
		return res, err
	}
	res.Manifest = manifest
	res.TopicCount = len(manifest)

	existingSet := make(map[string]bool, len(manifest))
	for _, item := range manifest {
		existingSet[item.File] = true
		if !item.Indexed {
			res.Unindexed = append(res.Unindexed, item)
		}
	}
	for _, lf := range linkedFiles {
		if !existingSet[lf] {
			res.BrokenLinks = append(res.BrokenLinks, lf)
		}
	}

	return res, nil
}

func scanDirectoryManifest(scope Scope, dir string, indexedSet map[string]bool, now time.Time) ([]ManifestItem, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if isNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var items []ManifestItem
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
		fm, _, _ := ParseDocument(raw, file)
		ageLabel, ageDays, _ := FormatAge(info.ModTime(), now)
		items = append(items, ManifestItem{
			Scope:       scope,
			File:        file,
			Name:        fm.Name,
			Description: fm.Description,
			Type:        fm.Type,
			ETag:        ComputeETag(raw),
			Age:         ageLabel,
			AgeDays:     ageDays,
			Indexed:     indexedSet != nil && indexedSet[file],
		})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].AgeDays != items[j].AgeDays {
			return items[i].AgeDays < items[j].AgeDays
		}
		return items[i].File < items[j].File
	})
	return items, nil
}

func truncateIndexNonSilent(content string, maxLines, maxBytes int) (bounded string, truncated bool, omitted int) {
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return "", false, 0
	}
	var kept []string
	byteCount := 0
	for i, line := range lines {
		lineBytes := len(line) + 1
		if i >= maxLines || (byteCount+lineBytes > maxBytes && len(kept) > 0) {
			truncated = true
			omitted = len(lines) - i
			break
		}
		kept = append(kept, line)
		byteCount += lineBytes
	}
	out := strings.Join(kept, "\n")
	if truncated {
		out += fmt.Sprintf("\n\n> [SMem 索引提示] MEMORY.md 超出上限（%d 行 / %dKB），已省略其余 %d 行。请调用 memory_search 检索，或运行 dream 整理精简。", maxLines, maxBytes/1024, omitted)
	}
	return out, truncated, omitted
}
