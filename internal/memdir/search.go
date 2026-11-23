package memdir

import (
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

const (
	bm25K1 = 1.2
)

type searchField int

const (
	fieldName searchField = iota
	fieldDesc
	fieldFile
	fieldBody
	fieldCount
)

var (
	fieldWeights = [fieldCount]float64{
		fieldName: 3.5,
		fieldDesc: 2.5,
		fieldFile: 2.0,
		fieldBody: 1.0,
	}
	fieldLengthB = [fieldCount]float64{
		fieldName: 0.40,
		fieldDesc: 0.50,
		fieldFile: 0.40,
		fieldBody: 0.75,
	}
)

var stopWords = map[string]bool{
	// English functional words
	"the": true, "a": true, "an": true, "is": true, "are": true, "was": true, "were": true,
	"be": true, "been": true, "to": true, "of": true, "in": true, "on": true, "at": true,
	"for": true, "with": true, "by": true, "from": true, "as": true, "it": true, "its": true,
	"this": true, "that": true, "these": true, "those": true, "and": true, "or": true,
	"how": true, "what": true, "when": true, "where": true, "why": true, "can": true, "please": true,
	// Chinese conversational & functional filler words
	"怎么": true, "如何": true, "什么": true, "哪个": true, "哪些": true, "为什么": true,
	"这个": true, "那个": true, "这些": true, "那些": true, "我们": true, "你们": true, "他们": true,
	"可以": true, "需要": true, "一下": true, "一个": true, "一些": true, "帮我": true, "请问": true,
	"现在": true, "如果": true, "因为": true, "所以": true, "但是": true, "还是": true, "以及": true,
	"看看": true, "处理": true, "问题": true, "关于": true,
}

var cjkStopRunes = map[rune]bool{
	'的': true, '了': true, '着': true, '过': true, '在': true, '是': true,
	'有': true, '把': true, '被': true, '给': true, '让': true, '对': true,
	'和': true, '与': true, '及': true, '或': true, '吗': true, '呢': true,
	'吧': true, '啊': true, '呀': true, '哦': true, '嘛': true,
}

type indexedSearchDoc struct {
	scope       Scope
	file        string
	fm          Frontmatter
	body        string
	etag        string
	ageLabel    string
	ageDays     int
	staleWarn   string
	nameLower   string
	descLower   string
	fileLower   string
	bodyLower   string
	fieldTF     [fieldCount]map[string]int
	fieldLength [fieldCount]int
}

// Search performs a fast, pure-Go in-memory BM25F scan over Markdown files across scopes.
// Combines field-weighted BM25F (name > description > filename > body), IDF rare-term weighting,
// CJK bigram + stopword filtering, code identifier splitting, phrase boosts, and gentle freshness decay.
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

	now := s.now()
	var docs []indexedSearchDoc
	var sumFieldLen [fieldCount]int

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

			fileStem := strings.TrimSuffix(file, filepath.Ext(file))
			ageLabel, ageDays, staleWarn := FormatAge(info.ModTime(), now)
			doc := indexedSearchDoc{
				scope:     sc,
				file:      file,
				fm:        fm,
				body:      body,
				etag:      ComputeETag(raw),
				ageLabel:  ageLabel,
				ageDays:   ageDays,
				staleWarn: staleWarn,
				nameLower: strings.ToLower(fm.Name),
				descLower: strings.ToLower(fm.Description),
				fileLower: strings.ToLower(fileStem),
				bodyLower: strings.ToLower(body),
			}

			fieldSources := [fieldCount]string{
				fieldName: fm.Name,
				fieldDesc: fm.Description,
				fieldFile: fileStem,
				fieldBody: body,
			}
			for f := searchField(0); f < fieldCount; f++ {
				toks := tokenizeStream(fieldSources[f])
				tfMap := make(map[string]int, len(toks))
				for _, tok := range toks {
					tfMap[tok]++
				}
				doc.fieldTF[f] = tfMap
				doc.fieldLength[f] = len(toks)
				sumFieldLen[f] += len(toks)
			}
			docs = append(docs, doc)
		}
	}

	docCount := len(docs)
	if docCount == 0 {
		return nil, nil
	}

	var avgFieldLen [fieldCount]float64
	for f := searchField(0); f < fieldCount; f++ {
		avg := float64(sumFieldLen[f]) / float64(docCount)
		if avg < 1.0 {
			avg = 1.0
		}
		avgFieldLen[f] = avg
	}

	queryLower := strings.ToLower(query)
	phraseProbe := cleanPhraseProbe(queryLower)
	qTokens := uniqueTokens(tokenizeStream(query))

	// Compute document frequency (DF) per unique query token across the corpus.
	docFreq := make(map[string]int, len(qTokens))
	for _, tok := range qTokens {
		df := 0
		for i := range docs {
			if docHasTerm(&docs[i], tok) {
				df++
			}
		}
		docFreq[tok] = df
	}

	var matches []SearchMatch
	for i := range docs {
		d := &docs[i]
		bm25Score, matchedCount := scoreDocBM25F(d, qTokens, docFreq, docCount, avgFieldLen)
		phraseBoost := scoreExactPhrase(d, phraseProbe)

		if bm25Score <= 0 && phraseBoost <= 0 {
			continue
		}

		if len(qTokens) > 1 && matchedCount > 1 {
			coverage := float64(matchedCount) / float64(len(qTokens))
			bm25Score *= (1.0 + 0.25*coverage)
		}

		score := bm25Score + phraseBoost
		// Gentle tie-breakers (never override strong lexical relevance): freshness, scope, feedback priority.
		score += 0.20 / (1.0 + float64(d.ageDays)/30.0)
		if d.scope == ScopeProject {
			score += 0.15
		}
		if d.fm.Type == TypeFeedback {
			score += 0.08
		}

		matches = append(matches, SearchMatch{
			Scope:        d.scope,
			File:         d.file,
			Name:         d.fm.Name,
			Description:  d.fm.Description,
			Type:         d.fm.Type,
			Snippet:      extractSnippet(d.body, qTokens, phraseProbe),
			Score:        score,
			ETag:         d.etag,
			Age:          d.ageLabel,
			StaleWarning: d.staleWarn,
		})
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

func docHasTerm(d *indexedSearchDoc, term string) bool {
	for f := searchField(0); f < fieldCount; f++ {
		if d.fieldTF[f][term] > 0 {
			return true
		}
	}
	return false
}

func scoreDocBM25F(d *indexedSearchDoc, qTokens []string, docFreq map[string]int, docCount int, avgFieldLen [fieldCount]float64) (float64, int) {
	if len(qTokens) == 0 {
		return 0, 0
	}
	total := 0.0
	matched := 0

	for _, tok := range qTokens {
		df := docFreq[tok]
		if df <= 0 {
			continue
		}
		idf := math.Log(1.0 + (float64(docCount)-float64(df)+0.5)/(float64(df)+0.5))
		pseudoTF := 0.0
		for f := searchField(0); f < fieldCount; f++ {
			tf := d.fieldTF[f][tok]
			if tf <= 0 {
				continue
			}
			b := fieldLengthB[f]
			normLen := 1.0 - b + b*(float64(d.fieldLength[f])/avgFieldLen[f])
			if normLen <= 0 {
				normLen = 1.0
			}
			pseudoTF += fieldWeights[f] * (float64(tf) / normLen)
		}
		if pseudoTF > 0 {
			matched++
			total += idf * (pseudoTF * (bm25K1 + 1.0)) / (bm25K1 + pseudoTF)
		}
	}
	return total, matched
}

func scoreExactPhrase(d *indexedSearchDoc, phrase string) float64 {
	if len([]rune(phrase)) < 2 {
		return 0
	}
	score := 0.0
	if strings.Contains(d.nameLower, phrase) {
		score += 4.5
	}
	if strings.Contains(d.descLower, phrase) {
		score += 3.2
	}
	if strings.Contains(d.fileLower, phrase) {
		score += 2.0
	}
	if strings.Contains(d.bodyLower, phrase) {
		score += 1.8
	}
	return score
}

func cleanPhraseProbe(qLower string) string {
	trimmed := strings.TrimSpace(qLower)
	if stopWords[trimmed] {
		return ""
	}
	return trimmed
}

// tokenizeStream splits Latin/code identifiers (camelCase, PascalCase, snake_case, kebab-case, hex codes)
// and generates CJK phrase + bigram tokens while filtering out Chinese and English stopwords.
// Preserves multiplicity so document term frequency (TF) is accurate.
func tokenizeStream(text string) []string {
	var out []string
	emit := func(tok string) {
		tok = strings.ToLower(strings.TrimSpace(tok))
		if tok == "" || stopWords[tok] {
			return
		}
		// Skip single ASCII letters ("a", "x"), keep digits or multi-char tokens.
		if len(tok) == 1 && tok[0] >= 'a' && tok[0] <= 'z' {
			return
		}
		out = append(out, tok)
	}

	var latinBuf []rune
	var cjkBuf []rune

	flushLatin := func() {
		if len(latinBuf) == 0 {
			return
		}
		raw := string(latinBuf)
		emit(raw)
		parts := splitCamelCase(raw)
		if len(parts) > 1 {
			for _, sub := range parts {
				if len(sub) >= 2 {
					emit(sub)
				}
			}
		}
		latinBuf = latinBuf[:0]
	}

	flushCJK := func() {
		n := len(cjkBuf)
		if n == 0 {
			return
		}
		defer func() { cjkBuf = cjkBuf[:0] }()

		if n == 1 {
			if !cjkStopRunes[cjkBuf[0]] {
				emit(string(cjkBuf))
			}
			return
		}
		if n == 2 {
			if !(cjkStopRunes[cjkBuf[0]] && cjkStopRunes[cjkBuf[1]]) {
				emit(string(cjkBuf))
			}
			return
		}

		// For compact 3..6 char CJK compounds without structural particles, also emit full compound.
		if n <= 6 && !containsStructuralParticle(cjkBuf) {
			emit(string(cjkBuf))
		}
		for i := 0; i < n-1; i++ {
			r1, r2 := cjkBuf[i], cjkBuf[i+1]
			if cjkStopRunes[r1] && cjkStopRunes[r2] {
				continue
			}
			// Drop cross-particle noise bigrams like "的服" / "器的" inside 3+ char clauses.
			if r1 == '的' || r2 == '的' || r1 == '了' || r2 == '了' || r1 == '吗' || r2 == '呢' || r2 == '吧' {
				continue
			}
			emit(string([]rune{r1, r2}))
		}
	}

	for _, r := range text {
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

func containsStructuralParticle(runes []rune) bool {
	for _, r := range runes {
		if cjkStopRunes[r] {
			return true
		}
	}
	return false
}

func uniqueTokens(tokens []string) []string {
	seen := make(map[string]bool, len(tokens))
	var out []string
	for _, t := range tokens {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
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
		lowerToUpper := unicode.IsLower(prev) && unicode.IsUpper(curr)
		acronymToWord := unicode.IsUpper(prev) && unicode.IsUpper(curr) && i+1 < len(runes) && unicode.IsLower(runes[i+1])
		if lowerToUpper || acronymToWord {
			parts = append(parts, string(runes[start:i]))
			start = i
		}
	}
	parts = append(parts, string(runes[start:]))
	return parts
}

func extractSnippet(body string, tokens []string, phraseLower string) string {
	lines := strings.Split(body, "\n")
	if phraseLower != "" {
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed != "" && strings.Contains(strings.ToLower(trimmed), phraseLower) {
				return truncateRunes(trimmed, 160)
			}
		}
	}
	bestLine := ""
	bestHits := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		low := strings.ToLower(trimmed)
		hits := 0
		for _, tok := range tokens {
			if strings.Contains(low, tok) {
				hits++
			}
		}
		if hits > bestHits {
			bestHits = hits
			bestLine = trimmed
		}
	}
	if bestLine != "" {
		return truncateRunes(bestLine, 160)
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
