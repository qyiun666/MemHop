// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package index

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"unicode"

	"github.com/go-ego/gse"

	"github.com/qyiun666/MemHop/internal/sub/common"
)

// Tokenizer defines the interface for text tokenization engines.
type Tokenizer interface {
	// Cut segments text into words.
	Cut(text string) []string
	// Close releases resources held by the tokenizer.
	Close()
}

// Engine names for configuration. Only "auto" and "gse" are supported;
// any other value is rejected by InitTokenizer.
const (
	EngineAuto = "auto"
	EngineGse  = "gse"
)

var (
	globalTokenizer    Tokenizer
	tokenizerOnce      sync.Once
	tokenizerMu        sync.Mutex
	tokenizerErr       error
	tokenizerEngine    string // normalized engine of the active singleton
	tokenizerErrLogged sync.Once
)

// normalizeEngine maps the empty string to EngineAuto.
func normalizeEngine(engine string) string {
	if engine == "" {
		return EngineAuto
	}
	return engine
}

// InitTokenizer initializes the global tokenizer (process-wide singleton).
// Accepts "", "auto" or "gse"; any other value returns an error so stale
// configuration is surfaced instead of silently downgraded.
// A repeated call with the same engine is a no-op; a repeated call with a
// DIFFERENT engine returns an explicit error rather than silently keeping
// the first instance. Use ResetTokenizer to re-initialize.
func InitTokenizer(engine string) error {
	want := normalizeEngine(engine)
	tokenizerOnce.Do(func() {
		switch want {
		case EngineAuto, EngineGse:
			globalTokenizer, tokenizerErr = createTokenizer()
			tokenizerEngine = want
		default:
			tokenizerErr = common.NewError(common.ErrConfig, fmt.Sprintf("unknown tokenizer engine %q (supported: %q, %q)", engine, EngineAuto, EngineGse))
		}
	})
	if tokenizerErr != nil {
		return tokenizerErr
	}
	if want != tokenizerEngine {
		return common.NewError(common.ErrConfig, fmt.Sprintf("tokenizer already initialized with engine %q; cannot switch to %q in the same process (call ResetTokenizer first)", tokenizerEngine, want))
	}
	return nil
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
	tokenizerEngine = ""
	tokenizerOnce = sync.Once{}
	tokenizerErrLogged = sync.Once{}
}

// getTokenizer returns the global tokenizer, lazily initializing it via
// EngineAuto on first use. If init fails (e.g. missing dictionaries), the
// error is logged once and nil is returned; callers must treat nil as an
// empty-token result rather than panicking. Hosts should call InitTokenizer
// explicitly at startup to surface configuration issues early.
func getTokenizer() Tokenizer {
	tokenizerMu.Lock()
	defer tokenizerMu.Unlock()
	if globalTokenizer == nil {
		if err := InitTokenizer(EngineAuto); err != nil {
			tokenizerErrLogged.Do(func() {
				slog.Error("memhop: tokenizer auto-init failed; tokenization will return empty",
					"error", err)
			})
			return nil
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
	if tok == nil {
		return nil
	}
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
		parts := common.SplitCamelCase(tok)
		for _, p := range parts {
			cleaned := common.TrimPunctuation(p)
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

// gseTokenizer implements Tokenizer using the pure-Go gse library.
// It loads the embedded "zh_s" dictionary (~350k Simplified-Chinese
// tokens) at construction so BM25 recall stays strong without CGO or
// external dictionary files.
type gseTokenizer struct {
	seg *gse.Segmenter
}

// createTokenizer builds the global tokenizer. gse is the only backend.
func createTokenizer() (Tokenizer, error) {
	s, err := gse.NewEmbed("zh_s")
	if err != nil {
		return nil, common.NewError(common.ErrConfig, "gse tokenizer init failed", err)
	}
	return &gseTokenizer{seg: &s}, nil
}

// Cut segments text in precise mode with HMM enabled so out-of-vocabulary
// CJK terms are still recognised.
func (g *gseTokenizer) Cut(text string) []string {
	return g.seg.Cut(text, true)
}

// Close is a no-op for gse (no native resources to release).
func (g *gseTokenizer) Close() {}
