// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package index

import (
	"fmt"
	"strings"
	"sync"
	"unicode"
)

// Tokenizer defines the interface for text tokenization engines.
type Tokenizer interface {
	// Cut segments text into words.
	Cut(text string) []string
	// Close releases resources held by the tokenizer.
	Close()
}

// Engine names for configuration.
const (
	EngineAuto    = "auto"
	EngineGojieba = "gojieba"
	EngineGse     = "gse"
)

var (
	globalTokenizer Tokenizer
	tokenizerOnce   sync.Once
	tokenizerMu     sync.Mutex
	tokenizerErr    error
)

// InitTokenizer initializes the global tokenizer with the specified engine.
// Pass "auto" to prefer gojieba (if compiled with CGO), falling back to gse.
// Returns an error if tokenizer initialization fails.
// Safe to call multiple times; only the first call takes effect unless
// ResetTokenizer is called.
func InitTokenizer(engine string) error {
	tokenizerOnce.Do(func() {
		globalTokenizer, tokenizerErr = createTokenizer(engine)
	})
	return tokenizerErr
}

// ResetTokenizer releases the current tokenizer and allows re-initialization.
func ResetTokenizer() {
	tokenizerMu.Lock()
	defer tokenizerMu.Unlock()
	if globalTokenizer != nil {
		globalTokenizer.Close()
		globalTokenizer = nil
	}
	tokenizerErr = nil
	tokenizerOnce = sync.Once{}
}

func getTokenizer() Tokenizer {
	if globalTokenizer == nil {
		if err := InitTokenizer(EngineAuto); err != nil {
			// This should never happen in normal operation since auto
			// initialization errors indicate a fatal configuration problem.
			panic(fmt.Sprintf("memhop: tokenizer auto-init failed: %v", err))
		}
	}
	return globalTokenizer
}

// stopWords contains common Chinese and English stop words.
var stopWords = map[string]struct{}{
	"the": {}, "a": {}, "an": {}, "is": {}, "are": {}, "was": {}, "were": {},
	"be": {}, "been": {}, "being": {}, "have": {}, "has": {}, "had": {},
	"do": {}, "does": {}, "did": {}, "will": {}, "would": {}, "could": {},
	"should": {}, "may": {}, "might": {}, "can": {}, "shall": {},
	"to": {}, "of": {}, "in": {}, "for": {}, "on": {}, "with": {},
	"at": {}, "by": {}, "from": {}, "as": {}, "into": {}, "through": {},
	"during": {}, "before": {}, "after": {}, "above": {}, "below": {},
	"between": {}, "out": {}, "off": {}, "over": {}, "under": {},
	"again": {}, "further": {}, "then": {}, "once": {}, "here": {},
	"there": {}, "when": {}, "where": {}, "why": {}, "how": {},
	"all": {}, "both": {}, "each": {}, "few": {}, "more": {}, "most": {},
	"other": {}, "some": {}, "such": {}, "no": {}, "nor": {}, "not": {},
	"only": {}, "own": {}, "same": {}, "so": {}, "than": {}, "too": {},
	"very": {}, "just": {}, "and": {}, "but": {}, "if": {}, "or": {},
	"because": {}, "until": {}, "while": {},
	"this": {}, "that": {}, "these": {}, "those": {},
	"i": {}, "me": {}, "my": {}, "we": {}, "our": {}, "you": {}, "your": {},
	"he": {}, "him": {}, "his": {}, "she": {}, "her": {},
	"it": {}, "its": {}, "they": {}, "them": {}, "their": {},
	"what": {}, "which": {}, "who": {},
	// Chinese stop words
	"的": {}, "了": {}, "在": {}, "是": {}, "我": {}, "有": {}, "和": {},
	"就": {}, "不": {}, "人": {}, "都": {}, "一": {}, "一个": {}, "上": {},
	"也": {}, "很": {}, "到": {}, "说": {}, "要": {}, "去": {}, "你": {},
	"会": {}, "着": {}, "没有": {}, "看": {}, "好": {}, "自己": {},
	"这": {}, "他": {}, "她": {}, "它": {}, "们": {}, "那": {}, "些": {},
	"什么": {}, "怎么": {}, "为什么": {}, "哪": {}, "谁": {},
	"吗": {}, "呢": {}, "吧": {}, "啊": {}, "哦": {}, "嗯": {},
	"把": {}, "被": {}, "让": {}, "给": {}, "呀": {},
}

func isStopWord(w string) bool {
	_, ok := stopWords[w]
	return ok
}

// Tokenize performs unified tokenization with stop-word filtering.
// Pipeline: preSplitCamelCase → protect underscores → engine.Cut →
//
//	restore underscores → splitCamelCase → trim punctuation → filter stop words.
func Tokenize(text string) []string {
	return runPipeline(text, true)
}

// TokenizeWords tokenizes without stop-word filtering (for entity index).
func TokenizeWords(text string) []string {
	return runPipeline(text, false)
}

// runPipeline is the shared tokenization pipeline.
func runPipeline(text string, filterStop bool) []string {
	tok := getTokenizer()
	preprocessed := preSplitCamelCase(text)
	protected := strings.ReplaceAll(preprocessed, "_", "\x01")
	segments := tok.Cut(protected)
	return processSegments(segments, filterStop)
}

// preSplitCamelCase inserts spaces at camelCase boundaries so the
// segmentation engine can split them.
func preSplitCamelCase(text string) string {
	runes := []rune(text)
	var result []rune
	for i, r := range runes {
		if i > 0 && unicode.IsUpper(r) {
			prev := runes[i-1]
			if unicode.IsLower(prev) || unicode.IsDigit(prev) {
				result = append(result, ' ')
			}
			if i+1 < len(runes) && unicode.IsUpper(prev) && unicode.IsLower(runes[i+1]) {
				result = append(result, ' ')
			}
		}
		result = append(result, r)
	}
	return string(result)
}

func processSegments(segments []string, filterStop bool) []string {
	var tokens []string
	for _, s := range segments {
		s = strings.ReplaceAll(s, "\x01", "_")
		s = strings.TrimSpace(s)
		if s != "" {
			tokens = append(tokens, s)
		}
	}

	var result []string
	for _, tok := range tokens {
		parts := splitCamelCase(tok)
		for _, p := range parts {
			cleaned := trimPunctuation(p)
			cleaned = strings.ToLower(cleaned)
			if cleaned == "" {
				continue
			}
			if filterStop && isStopWord(cleaned) {
				continue
			}
			result = append(result, cleaned)
		}
	}
	return result
}

// splitCamelCase splits camelCase/PascalCase identifiers:
//
//	"fetchUserData" → ["fetch", "user", "data"]
//	"JSONParser"    → ["json", "parser"]
//	"getUserID"     → ["get", "user", "id"]
//
// Tokens containing '_' are kept intact.
func splitCamelCase(word string) []string {
	if strings.Contains(word, "_") {
		return []string{word}
	}

	runes := []rune(word)
	hasUpper, hasLower := false, false
	for _, r := range runes {
		if unicode.IsUpper(r) {
			hasUpper = true
		}
		if unicode.IsLower(r) {
			hasLower = true
		}
	}
	if !hasUpper || !hasLower {
		return []string{word}
	}

	lower := []rune(strings.ToLower(word))
	n := len(runes)
	var parts []string
	start := 0

	for i := 1; i < n; i++ {
		if unicode.IsLower(runes[i-1]) && unicode.IsUpper(runes[i]) {
			parts = append(parts, string(lower[start:i]))
			start = i
			continue
		}
		if i+1 < n && unicode.IsUpper(runes[i-1]) && unicode.IsUpper(runes[i]) && unicode.IsLower(runes[i+1]) {
			parts = append(parts, string(lower[start:i]))
			start = i
		}
	}
	parts = append(parts, string(lower[start:]))
	return parts
}

// trimPunctuation removes non-alphanumeric (except '_') from both ends.
func trimPunctuation(s string) string {
	return strings.TrimFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
	})
}
