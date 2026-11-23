package memdir

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

// Search performs a fast, pure-Go in-memory scan over Markdown files across scopes.
// Supports English words, camelCase/snake_case code identifiers, and Chinese bigrams.
func (s *Store) Search(opts SearchOptions) ([]SearchMatch, error) {
	query := strings.TrimSpace(opts.Query)
	if query == "" {
		return nil, nil
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 8
	}
	if limit > 50 {
		limit = 50
	}

	scopes, err := s.resolveScopeList(opts.Scope)
	if err != nil {
		return nil, err
	}

	var typeFilter MemoryType
	if strings.TrimSpace(opts.Type) != "" {
		mt, err := ParseMemoryType(opts.Type)
		if err != nil {
			return nil, err
		}
		typeFilter = mt
	}

	queryLower := strings.ToLower(query)
	tokens := tokenizeQuery(query)
	now := s.now()

	var matches []SearchMatch
	for _, sc := range scopes {
		dir := s.dirForScope(sc)
		if dir == "" {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			if isNotExist(err) {
				continue
			}
			return nil, err
		}
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
			if typeFilter != "" && fm.Type != typeFilter {
				continue
			}

			score := scoreEntry(queryLower, tokens, fm, file, body)
			if score <= 0 {
				continue
			}
			// Slight tie-breaker preferring project scope over global when equally relevant.
			if sc == ScopeProject {
				score += 0.15
			}

			ageLabel, _, staleWarn := FormatAge(info.ModTime(), now)
			matches = append(matches, SearchMatch{
				Scope:        sc,
				File:         file,
				Name:         fm.Name,
				Description:  fm.Description,
				Type:         fm.Type,
				Snippet:      extractSnippet(body, tokens, queryLower),
				Score:        score,
				ETag:         ComputeETag(raw),
				Age:          ageLabel,
				StaleWarning: staleWarn,
			})
		}
	}

	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].Score != matches[j].Score {
			return matches[i].Score > matches[j].Score
		}
		if matches[i].Scope != matches[j].Scope {
			return matches[i].Scope == ScopeProject
		}
		return matches[i].File < matches[j].File
	})

	if len(matches) > limit {
		matches = matches[:limit]
	}
	return matches, nil
}

func scoreEntry(queryLower string, tokens []string, fm Frontmatter, file, body string) float64 {
	nameLower := strings.ToLower(fm.Name)
	descLower := strings.ToLower(fm.Description)
	fileLower := strings.ToLower(file)
	bodyLower := strings.ToLower(body)

	var score float64
	// Exact full phrase bonus across weighted fields.
	if strings.Contains(nameLower, queryLower) {
		score += 12.0
	}
	if strings.Contains(descLower, queryLower) {
		score += 9.0
	}
	if strings.Contains(bodyLower, queryLower) {
		score += 6.0
	}
	if strings.Contains(fileLower, queryLower) {
		score += 4.0
	}

	if len(tokens) == 0 {
		return score
	}

	matchedTokens := 0
	for _, tok := range tokens {
		hit := false
		if strings.Contains(nameLower, tok) {
			score += 3.0
			hit = true
		}
		if strings.Contains(descLower, tok) {
			score += 2.5
			hit = true
		}
		if strings.Contains(fileLower, tok) {
			score += 1.5
			hit = true
		}
		if strings.Contains(bodyLower, tok) {
			score += 1.2
			hit = true
		}
		if hit {
			matchedTokens++
		}
	}

	// Coverage bonus when multiple distinct query tokens hit the same memory.
	if matchedTokens > 0 {
		coverage := float64(matchedTokens) / float64(len(tokens))
		score += coverage * 4.0
	}
	return score
}

// tokenizeQuery splits Latin/code identifiers (camelCase, snake_case, kebab-case, paths)
// and generates CJK terms + overlapping bigrams so Chinese/English mixed queries work naturally.
func tokenizeQuery(query string) []string {
	seen := make(map[string]bool)
	var out []string
	add := func(tok string) {
		tok = strings.ToLower(strings.TrimSpace(tok))
		if tok == "" || seen[tok] {
			return
		}
		seen[tok] = true
		out = append(out, tok)
	}

	// Extract ASCII/identifier chunks and CJK runs.
	var latinBuf []rune
	var cjkBuf []rune

	flushLatin := func() {
		if len(latinBuf) == 0 {
			return
		}
		raw := string(latinBuf)
		add(raw)
		// Split camelCase / PascalCase
		for _, sub := range splitCamelCase(raw) {
			if len(sub) >= 2 {
				add(sub)
			}
		}
		latinBuf = latinBuf[:0]
	}

	flushCJK := func() {
		if len(cjkBuf) == 0 {
			return
		}
		if len(cjkBuf) == 1 {
			add(string(cjkBuf))
		} else {
			add(string(cjkBuf))
			for i := 0; i < len(cjkBuf)-1; i++ {
				add(string(cjkBuf[i : i+2]))
			}
		}
		cjkBuf = cjkBuf[:0]
	}

	for _, r := range query {
		switch {
		case unicode.Is(unicode.Han, r):
			flushLatin()
			cjkBuf = append(cjkBuf, r)
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
			flushCJK()
			latinBuf = append(latinBuf, r)
		default:
			flushLatin()
			flushCJK()
		}
	}
	flushLatin()
	flushCJK()
	return out
}

func splitCamelCase(s string) []string {
	runes := []rune(s)
	if len(runes) <= 1 {
		return []string{s}
	}
	var parts []string
	start := 0
	for i := 1; i < len(runes); i++ {
		prev := runes[i-1]
		curr := runes[i]
		isBoundary := unicode.IsLower(prev) && unicode.IsUpper(curr)
		if isBoundary {
			parts = append(parts, string(runes[start:i]))
			start = i
		}
	}
	parts = append(parts, string(runes[start:]))
	return parts
}

func extractSnippet(body string, tokens []string, queryLower string) string {
	lines := strings.Split(body, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		low := strings.ToLower(trimmed)
		if strings.Contains(low, queryLower) {
			return truncateRunes(trimmed, 160)
		}
		for _, tok := range tokens {
			if strings.Contains(low, tok) {
				return truncateRunes(trimmed, 160)
			}
		}
	}
	for _, line := range lines {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return truncateRunes(trimmed, 160)
		}
	}
	return ""
}

func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}
