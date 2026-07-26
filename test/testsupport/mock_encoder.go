package testsupport

import (
	"math"
	"path/filepath"
	"testing"
	"unicode"

	"github.com/qyiun666/MemHop/api"
)

// MockVectorDim is the default dimension used by MockEncoder and OpenMemHopMock.
const MockVectorDim = 256

// MockEncoder is a deterministic, offline memhop.Encoder for tests.
// Vectors are L2-normalized bag-of-words hashes: texts sharing tokens
// get a higher cosine similarity, so retrieval behavior is assertable
// without a running Ollama instance.
type MockEncoder struct {
	dim int
}

// NewMockEncoder returns a MockEncoder with the given dimension (<= 0 means MockVectorDim).
func NewMockEncoder(dim int) *MockEncoder {
	if dim <= 0 {
		dim = MockVectorDim
	}
	return &MockEncoder{dim: dim}
}

// Encode maps text to a deterministic dense vector (f16 bits).
func (m *MockEncoder) Encode(text string) (*memhop.EncoderOutput, error) {
	vec := make([]float32, m.dim)
	for _, tok := range mockTokens(text) {
		vec[int(memhop.HashID(tok)%uint64(m.dim))]++
	}
	var norm float64
	for _, v := range vec {
		norm += float64(v) * float64(v)
	}
	out := make([]uint16, m.dim)
	if norm == 0 {
		return &memhop.EncoderOutput{Dense: out}, nil
	}
	inv := float32(1 / math.Sqrt(norm))
	for i, v := range vec {
		out[i] = f32ToF16Bits(v * inv)
	}
	return &memhop.EncoderOutput{Dense: out}, nil
}

// Dim returns the fixed vector dimensionality.
func (m *MockEncoder) Dim() int { return m.dim }

// Mode returns the encoder label.
func (m *MockEncoder) Mode() string { return "mock:bow-hash" }

// IsAvailable always reports true: the mock needs no external service.
func (m *MockEncoder) IsAvailable() bool { return true }

// mockTokens splits text into lowercase ASCII word tokens plus one token
// per non-ASCII letter rune (CJK unigrams), so Chinese texts with shared
// characters also overlap.
func mockTokens(text string) []string {
	var tokens []string
	var word []rune
	flush := func() {
		if len(word) > 0 {
			tokens = append(tokens, string(word))
			word = word[:0]
		}
	}
	for _, r := range text {
		switch {
		case r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			word = append(word, unicode.ToLower(r))
		case unicode.IsLetter(r):
			flush()
			tokens = append(tokens, string(unicode.ToLower(r)))
		default:
			flush()
		}
	}
	flush()
	return tokens
}

// f32ToF16Bits converts a float32 to IEEE 754 half-precision bits.
func f32ToF16Bits(f float32) uint16 {
	bits := math.Float32bits(f)
	sign := uint16((bits >> 16) & 0x8000)
	exp := int32((bits>>23)&0xff) - 127 + 15
	mant := bits & 0x7fffff
	switch {
	case exp <= 0: // underflow to signed zero
		return sign
	case exp >= 31: // overflow to infinity
		return sign | 0x7c00
	default:
		return sign | uint16(exp)<<10 | uint16(mant>>13)
	}
}

// OpenMemHopMock opens a fully offline MemHop database backed by MockEncoder:
// the DB file lives in t.TempDir() and the LLM config points at an unroutable
// local port, so LLM calls fail instantly and Search falls back to the local
// tokenizer without real network access.
// The caller must call Close() when done.
func OpenMemHopMock(t *testing.T) *memhop.MemHop {
	t.Helper()
	cfg := memhop.Config{
		DBPath:     filepath.Join(t.TempDir(), "mock.meh"),
		VectorDim:  MockVectorDim,
		EmbedModel: "mock-embed",
	}
	cfg.LLM.APIURL = "http://127.0.0.1:1"
	cfg.LLM.APIKey = "sk-test"
	cfg.LLM.Model = "mock-model"
	cfg.LLM.TimeoutSecs = 1
	mh, err := memhop.OpenWithEncoder(&cfg, NewMockEncoder(MockVectorDim))
	if err != nil {
		t.Fatalf("OpenWithEncoder: %v", err)
	}
	return mh
}
