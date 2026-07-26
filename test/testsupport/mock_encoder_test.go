package testsupport

import (
	"math"
	"testing"
	"time"

	"github.com/qyiun666/MemHop/api"
)

func mustEncode(t *testing.T, enc *MockEncoder, text string) []uint16 {
	t.Helper()
	out, err := enc.Encode(text)
	if err != nil {
		t.Fatalf("Encode(%q): %v", text, err)
	}
	if len(out.Dense) != enc.Dim() {
		t.Fatalf("Encode(%q): dim = %d, want %d", text, len(out.Dense), enc.Dim())
	}
	return out.Dense
}

// 同一文本两次编码必须得到完全相同的向量（确定性，不依赖外部服务）。
func TestMockEncoderDeterministic(t *testing.T) {
	enc := NewMockEncoder(0) // 默认维度
	a := mustEncode(t, enc, "apple banana cherry 天气")
	b := mustEncode(t, enc, "apple banana cherry 天气")
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("Encode 不确定: index %d differs (%d != %d)", i, a[i], b[i])
		}
	}
	if enc.Mode() == "" {
		t.Error("Mode() 为空")
	}
	if !enc.IsAvailable() {
		t.Error("IsAvailable() = false")
	}
}

// f16BitsToF32 是 f32ToF16Bits 的逆变换（次正规数按 0 处理，测试不会触及）。
func f16BitsToF32(b uint16) float32 {
	sign := uint32(b&0x8000) << 16
	exp := uint32(b>>10) & 0x1f
	mant := uint32(b & 0x3ff)
	var bits uint32
	switch exp {
	case 0:
		bits = sign
	case 0x1f:
		bits = sign | 0x7f800000 | mant<<13
	default:
		bits = sign | (exp-15+127)<<23 | mant<<13
	}
	return math.Float32frombits(bits)
}

func cosineDense(a, b []uint16) float64 {
	var dot, na, nb float64
	for i := range a {
		x, y := float64(f16BitsToF32(a[i])), float64(f16BitsToF32(b[i]))
		dot += x * y
		na += x * x
		nb += y * y
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / math.Sqrt(na*nb)
}

// 词袋 hash 向量必须让有词汇重叠的文本余弦相似度更高（检索链路可断言的前提）。
func TestMockEncoderSimilarity(t *testing.T) {
	enc := NewMockEncoder(MockVectorDim)

	base := mustEncode(t, enc, "apple banana cherry")
	overlap := mustEncode(t, enc, "apple banana date")
	disjoint := mustEncode(t, enc, "xylophone quasar juniper")

	simOverlap := cosineDense(base, overlap)
	simDisjoint := cosineDense(base, disjoint)
	t.Logf("英文: overlap=%.4f disjoint=%.4f", simOverlap, simDisjoint)
	if simOverlap <= simDisjoint {
		t.Errorf("重叠文本相似度 %.4f 应高于无关文本 %.4f", simOverlap, simDisjoint)
	}
	if simOverlap < 0.5 {
		t.Errorf("重叠文本相似度 %.4f 过低（共享 2/3 token，期望 ~0.67）", simOverlap)
	}

	// CJK 按单字成 token，共享汉字的文本也应更接近
	cjkBase := mustEncode(t, enc, "今天天气很好")
	cjkOverlap := mustEncode(t, enc, "今天天气不错")
	cjkDisjoint := mustEncode(t, enc, "苹果香蕉樱桃")
	simCJK := cosineDense(cjkBase, cjkOverlap)
	simCJKDisjoint := cosineDense(cjkBase, cjkDisjoint)
	t.Logf("中文: overlap=%.4f disjoint=%.4f", simCJK, simCJKDisjoint)
	if simCJK <= simCJKDisjoint {
		t.Errorf("中文重叠文本相似度 %.4f 应高于无关文本 %.4f", simCJK, simCJKDisjoint)
	}
}

// 全离线链路：OpenMemHopMock 不连 Ollama/LLM，AutoCreate 建话题后，
// 相同文本再次检索必须命中同一话题。
func TestOpenMemHopMockOffline(t *testing.T) {
	mh := OpenMemHopMock(t)
	defer mh.Close()

	const text = "apple banana cherry smoothie recipe"
	created, err := mh.Search(memhop.SearchQuery{Timestamp: time.Now().UnixMilli(), Text: text, AutoCreate: true})
	if err != nil {
		t.Fatalf("AutoCreate Search: %v", err)
	}
	if len(created.Contexts) != 1 {
		t.Fatalf("AutoCreate 应返回 1 个 context, got %d", len(created.Contexts))
	}
	topicID := created.Contexts[0].ID

	got, err := mh.Search(memhop.SearchQuery{Timestamp: time.Now().UnixMilli(), Text: text})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got.Contexts) == 0 {
		t.Fatal("相同文本再次检索应命中已建话题")
	}
	if got.Contexts[0].ID != topicID {
		t.Errorf("top context ID = %s, want %s", got.Contexts[0].ID, topicID)
	}
}
