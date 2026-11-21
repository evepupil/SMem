package memory

import (
	"sort"
	"strconv"
	"strings"
	"unicode"

	"smem/internal/workspace"
)

const (
	pinnedLimit  = 3
	relatedLimit = 5
)

type queryAlternative struct {
	phrase string
	terms  []string
}

type rankedRecord struct {
	record  Record
	phrase  bool
	covered int
	total   int
}

func Start(layout workspace.Layout, query string) (pinned []Record, more bool, matches []Record, err error) {
	records, err := readRecords(layout, "all")
	if err != nil {
		return nil, false, nil, err
	}

	var common []Record
	commonKeys := make(map[string]struct{})
	for _, record := range records {
		if !record.Always {
			continue
		}
		key := recordKey(record)
		if _, exists := commonKeys[key]; exists {
			continue
		}
		commonKeys[key] = struct{}{}
		common = append(common, record)
	}
	more = len(common) > pinnedLimit
	if more {
		common = common[:pinnedLimit]
	}
	seen := make(map[string]struct{}, len(common))
	for _, record := range common {
		seen[recordKey(record)] = struct{}{}
	}

	if strings.TrimSpace(query) == "" {
		return common, more, nil, nil
	}
	matches = rankRelated(records, query)
	filtered := matches[:0]
	for _, record := range matches {
		if _, exists := seen[recordKey(record)]; exists {
			continue
		}
		filtered = append(filtered, record)
	}
	if len(filtered) > relatedLimit {
		filtered = filtered[:relatedLimit]
	}
	return common, more, filtered, nil
}

func Related(layout workspace.Layout, query string) ([]Record, error) {
	if strings.TrimSpace(query) == "" {
		return nil, emptyQueryError()
	}
	records, err := readRecords(layout, "all")
	if err != nil {
		return nil, err
	}
	ranked := rankRelated(records, query)
	if len(ranked) > relatedLimit {
		ranked = ranked[:relatedLimit]
	}
	return ranked, nil
}

func rankRelated(records []Record, query string) []Record {
	alternatives := queryAlternatives(query)
	if len(alternatives) == 0 {
		return nil
	}

	var ranked []rankedRecord
	for _, record := range records {
		candidate := normalizePhrase(record.Text)
		terms := make(map[string]struct{})
		for _, term := range lexicalTerms(record.Text) {
			terms[term] = struct{}{}
		}
		best := rankedRecord{record: record}
		matched := false
		for _, alternative := range alternatives {
			if len(alternative.terms) == 0 {
				continue
			}
			current := rankedRecord{
				record: record,
				total:  len(alternative.terms),
				phrase: len([]rune(alternative.phrase)) >= 2 && strings.Contains(candidate, alternative.phrase),
			}
			for _, term := range alternative.terms {
				if _, exists := terms[term]; exists {
					current.covered++
				}
			}
			if current.covered == 0 {
				continue
			}
			if !matched || betterRelevance(current, best) {
				best = current
				matched = true
			}
		}
		if matched {
			ranked = append(ranked, best)
		}
	}

	sort.SliceStable(ranked, func(i, j int) bool {
		left, right := ranked[i], ranked[j]
		if betterRelevance(left, right) {
			return true
		}
		if betterRelevance(right, left) {
			return false
		}
		if left.record.Scope != right.record.Scope {
			return left.record.Scope == "project"
		}
		if left.record.Path != right.record.Path {
			return left.record.Path < right.record.Path
		}
		if left.record.Line != right.record.Line {
			return left.record.Line < right.record.Line
		}
		return left.record.Ref < right.record.Ref
	})

	result := make([]Record, len(ranked))
	for i, item := range ranked {
		result[i] = item.record
	}
	return result
}

func betterRelevance(left, right rankedRecord) bool {
	if left.phrase != right.phrase {
		return left.phrase
	}
	leftComplete := left.covered == left.total
	rightComplete := right.covered == right.total
	if leftComplete != rightComplete {
		return leftComplete
	}
	leftRatio := left.covered * right.total
	rightRatio := right.covered * left.total
	if leftRatio != rightRatio {
		return leftRatio > rightRatio
	}
	return left.covered > right.covered
}

func queryAlternatives(query string) []queryAlternative {
	parts := splitExplicitAlternatives(query)
	alternatives := make([]queryAlternative, 0, len(parts))
	for _, part := range parts {
		terms := uniqueTerms(lexicalTerms(part))
		if len(terms) == 0 {
			continue
		}
		alternatives = append(alternatives, queryAlternative{
			phrase: normalizePhrase(part),
			terms:  terms,
		})
	}
	return alternatives
}

func splitExplicitAlternatives(query string) []string {
	runes := []rune(query)
	parts := make([]string, 0, 2)
	start := 0
	for i, char := range runes {
		if (char != '/' && char != '|') || i == 0 || i+1 == len(runes) ||
			!unicode.IsSpace(runes[i-1]) || !unicode.IsSpace(runes[i+1]) {
			continue
		}
		parts = append(parts, string(runes[start:i]))
		start = i + 1
	}
	if len(parts) == 0 {
		return []string{query}
	}
	return append(parts, string(runes[start:]))
}

func uniqueTerms(terms []string) []string {
	seen := make(map[string]struct{}, len(terms))
	unique := make([]string, 0, len(terms))
	for _, term := range terms {
		if _, exists := seen[term]; exists {
			continue
		}
		seen[term] = struct{}{}
		unique = append(unique, term)
	}
	return unique
}

func lexicalTerms(text string) []string {
	runes := []rune(strings.ToLower(text))
	var terms []string
	for i := 0; i < len(runes); {
		if unicode.Is(unicode.Han, runes[i]) {
			start := i
			for i < len(runes) && unicode.Is(unicode.Han, runes[i]) {
				i++
			}
			terms = append(terms, hanTerms(runes[start:i])...)
			continue
		}
		if !unicode.IsLetter(runes[i]) && !unicode.IsDigit(runes[i]) {
			i++
			continue
		}
		start := i
		for i < len(runes) && (unicode.IsLetter(runes[i]) || unicode.IsDigit(runes[i]) || unicode.IsMark(runes[i])) && !unicode.Is(unicode.Han, runes[i]) {
			i++
		}
		term := normalizeEnglishWord(string(runes[start:i]))
		if term != "" && !englishStopWord(term) {
			terms = append(terms, term)
		}
	}
	return terms
}

func hanTerms(text []rune) []string {
	compact := make([]rune, 0, len(text))
	for _, char := range text {
		if !hanStopCharacter(char) {
			compact = append(compact, char)
		}
	}
	if len(compact) == 0 {
		return nil
	}
	if len(compact) == 1 {
		return []string{string(compact)}
	}
	terms := make([]string, 0, len(compact)-1)
	for i := 0; i+1 < len(compact); i++ {
		terms = append(terms, string(compact[i:i+2]))
	}
	return terms
}

func normalizeEnglishWord(word string) string {
	if len(word) > 4 && strings.HasSuffix(word, "ies") {
		return strings.TrimSuffix(word, "ies") + "y"
	}
	if len(word) > 5 && strings.HasSuffix(word, "ing") {
		word = strings.TrimSuffix(word, "ing")
		if len(word) > 2 && word[len(word)-1] == word[len(word)-2] {
			word = word[:len(word)-1]
		}
		return word
	}
	if len(word) > 4 && strings.HasSuffix(word, "ed") {
		return strings.TrimSuffix(word, "ed")
	}
	if len(word) > 4 && strings.HasSuffix(word, "es") {
		return strings.TrimSuffix(word, "es")
	}
	if len(word) > 3 && strings.HasSuffix(word, "s") && !strings.HasSuffix(word, "ss") {
		return strings.TrimSuffix(word, "s")
	}
	return word
}

func englishStopWord(word string) bool {
	switch word {
	case "a", "an", "and", "are", "as", "at", "be", "by", "for", "from", "in", "is", "it", "of", "on", "or", "the", "to", "was", "were", "with":
		return true
	default:
		return false
	}
}

func hanStopCharacter(char rune) bool {
	return strings.ContainsRune("的地得了着过吗呢吧啊呀嘛呗和与及或在从到向对把被为由于以而并则", char)
}

func normalizePhrase(text string) string {
	var phrase strings.Builder
	for _, char := range strings.ToLower(text) {
		if unicode.Is(unicode.Han, char) && hanStopCharacter(char) {
			continue
		}
		if unicode.IsLetter(char) || unicode.IsDigit(char) {
			phrase.WriteRune(char)
		}
	}
	return phrase.String()
}

func recordKey(record Record) string {
	key := record.Scope + "\x00"
	if record.Ref != "" {
		return key + record.Ref
	}
	return key + record.Path + "\x00" + strconv.Itoa(record.Line) + "\x00" + record.Text
}
