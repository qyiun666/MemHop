// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// llm_ops.go 是 Provider 的三个 LLM 调用点：语义关键词提取、
// L2 压缩（Consolidate）与 L1→L0 蒸馏（Distill）。

package sub

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/qyiun666/MemHop/internal/sub/common"
	"github.com/qyiun666/MemHop/internal/sub/repo/core"
)

// ---------- 语义关键词提取 ----------

const systemKeywords = `You extract keywords that capture the core semantics of the text.

Rules:
- The keyword list, when read together, must convey the same meaning as the original text
- Include named entities, terms, topics, actions, time, locations, and any key details
- Keep mixed-language terms (English+Chinese) in their original form
- Preserve numbers and proper nouns exactly as-is
- Do not set a limit on the number of keywords; extract all that are needed to preserve the meaning
- Do not include greetings, filler words, or non-informational content
- Output ONLY valid JSON: {"keywords":[...]}, no markdown, no code fences`

// ExtractKeywords 提取文本的语义关键词。返回的关键词集合
// 拼合起来能代表原文的核心语义，数量不限制。
func (p *Provider) ExtractKeywords(ctx context.Context, text string) ([]string, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return []string{}, nil
	}
	user := "Extract keywords from:\n" + trimmed
	response, err := p.chat(ctx, systemKeywords, user, p.maxOutputTokens, 0.0, 1.0)
	if err != nil {
		return nil, err
	}
	var raw struct {
		Keywords []string `json:"keywords"`
	}
	if err := json.Unmarshal([]byte(stripCodeBlocks(response)), &raw); err != nil {
		return nil, common.NewError(common.ErrLLM, "keywords response parse failed", err)
	}
	return dedupeKeywords(raw.Keywords), nil
}

// dedupeKeywords 过滤空串并去重（保留首次出现顺序）。
func dedupeKeywords(ss []string) []string {
	seen := make(map[string]struct{}, len(ss))
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

// ---------- L2 压缩（Consolidate） ----------

// L2Group 描述一组被压缩的 L2 话题。
type L2Group struct {
	SceneID       uint64   `json:"scene_id"`       // 所属场景 ID
	NodeHashes    []uint64 `json:"node_hashes"`    // 被压缩的话题 ID 列表
	MergedTitle   string   `json:"merged_title"`   // 压缩后的话题标题
	MergedSummary string   `json:"merged_summary"` // 压缩后的内容总结
}

// ConsolidationOutput 是 L2 压缩的 LLM 返回。
type ConsolidationOutput struct {
	L2Groups            []L2Group `json:"l2_groups"`
	L2CompressionNeeded bool      `json:"l2_compression_needed"`
}

const systemConsolidate = `You analyze L2 chat memory topics and decide whether adjacent topics belong to the same topic and can be further compressed.

Rules:
- Only merge topics that clearly belong to the same conversation topic and can be meaningfully compressed
- For each merged group, write a summary that preserves ALL details (time, location, people, numbers, facts); the summary alone must be enough to know what this batch of content is about
- Do not generalize or drop details
- Echo scene_id and node_hashes EXACTLY as given in the input
- When no compression is needed, output l2_groups as an empty array
- Output ONLY valid JSON: {"l2_groups":[...],"l2_compression_needed":bool}, no markdown, no code fences`

// Consolidate 判断一批 L2 话题是否属于同一话题、可否进一步压缩。
// 返回被压缩话题的分组与保留全部细节的内容总结。
func (p *Provider) Consolidate(ctx context.Context, topics []core.TopicSlot) (*ConsolidationOutput, error) {
	if len(topics) == 0 {
		return &ConsolidationOutput{L2Groups: []L2Group{}, L2CompressionNeeded: false}, nil
	}
	user := buildConsolidatePrompt(topics)
	response, err := p.chat(ctx, systemConsolidate, user, p.maxOutputTokens, 0.0, 1.0)
	if err != nil {
		return nil, err
	}
	return parseConsolidateResponse(response)
}

// buildConsolidatePrompt 按 SceneID 分组列出话题数据，场景内按用户发言时间升序
// （时间相邻才有"同一话题可压缩"的判断意义）。
func buildConsolidatePrompt(topics []core.TopicSlot) string {
	byScene := make(map[uint64][]core.TopicSlot)
	for _, t := range topics {
		byScene[t.SceneID] = append(byScene[t.SceneID], t)
	}
	sceneIDs := make([]uint64, 0, len(byScene))
	for sid := range byScene {
		sceneIDs = append(sceneIDs, sid)
	}
	sort.Slice(sceneIDs, func(i, j int) bool { return sceneIDs[i] < sceneIDs[j] })

	var b strings.Builder
	fmt.Fprintf(&b, "# L2 Topic Data (%d scenes)\n\n", len(sceneIDs))
	for _, sid := range sceneIDs {
		nodes := byScene[sid]
		sort.Slice(nodes, func(i, j int) bool {
			if nodes[i].UserTimestamp != nodes[j].UserTimestamp {
				return nodes[i].UserTimestamp < nodes[j].UserTimestamp
			}
			return nodes[i].ID < nodes[j].ID
		})
		fmt.Fprintf(&b, "## scene_id = %d\n", sid)
		for _, n := range nodes {
			fmt.Fprintf(&b, "- id=%d depth=%d user_kw=%v agent_kw=%v fused_kw=%v\n",
				n.ID, n.Depth, n.UserKeywords, n.AgentKeywords, n.FusedKeywords)
		}
		b.WriteByte('\n')
	}
	b.WriteString("Decide which topics belong to the same topic and output the merged groups now.")
	return b.String()
}

// parseConsolidateResponse 解析 LLM 返回；scene_id / node_hashes
// 兼容 JSON 数字或引号字符串（LLM 常引号化超 2^53 的哈希）。
func parseConsolidateResponse(response string) (*ConsolidationOutput, error) {
	cleaned := stripCodeBlocks(response)
	var raw struct {
		L2Groups []struct {
			SceneID       json.RawMessage   `json:"scene_id"`
			NodeHashes    []json.RawMessage `json:"node_hashes"`
			MergedTitle   string            `json:"merged_title"`
			MergedSummary string            `json:"merged_summary"`
		} `json:"l2_groups"`
		L2CompressionNeeded bool `json:"l2_compression_needed"`
	}
	if err := json.Unmarshal([]byte(cleaned), &raw); err != nil {
		return nil, common.NewError(common.ErrLLM, "consolidate response parse failed", err)
	}
	out := &ConsolidationOutput{
		L2Groups:            make([]L2Group, 0, len(raw.L2Groups)),
		L2CompressionNeeded: raw.L2CompressionNeeded,
	}
	for _, g := range raw.L2Groups {
		sceneID, err := parseUint64Flex(g.SceneID)
		if err != nil {
			return nil, common.NewError(common.ErrLLM, "consolidate scene_id parse failed", err)
		}
		hashes := make([]uint64, 0, len(g.NodeHashes))
		for _, h := range g.NodeHashes {
			v, err := parseUint64Flex(h)
			if err != nil {
				return nil, common.NewError(common.ErrLLM, "consolidate node_hashes parse failed", err)
			}
			hashes = append(hashes, v)
		}
		out.L2Groups = append(out.L2Groups, L2Group{
			SceneID: sceneID, NodeHashes: hashes,
			MergedTitle: g.MergedTitle, MergedSummary: g.MergedSummary,
		})
	}
	return out, nil
}

// parseUint64Flex 将 JSON 数字或引号字符串解析为 uint64，
// 先按十进制，失败再按十六进制（含 0x 前缀）。
func parseUint64Flex(raw json.RawMessage) (uint64, error) {
	s := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	if s == "" || s == "null" {
		return 0, common.NewError(common.ErrLLM, "empty uint64 value")
	}
	if v, err := strconv.ParseUint(s, 10, 64); err == nil {
		return v, nil
	}
	return strconv.ParseUint(strings.TrimPrefix(s, "0x"), 16, 64)
}

// ---------- L1→L0 蒸馏（Distill） ----------

// L1Sample 是送入蒸馏的 L1 节点样本，由调用方从 L1 节点及其关联话题组装
// （L1 SceneNode 本身不含关键词，关键词取自关联的 L2 Topic）。
type L1Sample struct {
	IDHash     uint64   // 节点哈希
	Keywords   []string // 关联话题的融合关键词
	Summary    string   // 可选的摘要文本
	Importance float32  // 重要性
	Depth      uint8    // 节点深度
}

// EmotionScore 情绪 VAD 三维度，范围 [0,1]。
type EmotionScore struct {
	Valence   float64 `json:"valence"`   // 0=非常负面, 1=非常正面
	Arousal   float64 `json:"arousal"`   // 0=平静, 1=高度兴奋
	Dominance float64 `json:"dominance"` // 0=顺从, 1=主导
}

// MBTIScore MBTI 四维度，范围 [-1,1]；Type 由维度推导，维度为负取 I/N/T/J，为正取 E/S/F/P。
type MBTIScore struct {
	IE   float64 `json:"i_e"`
	NS   float64 `json:"n_s"`
	TF   float64 `json:"t_f"`
	JP   float64 `json:"j_p"`
	Type string  `json:"type"`
}

// NodeEmotion 单个 L1 节点的情感值。
type NodeEmotion struct {
	IDHex   string  `json:"id_hex"` // 16 位 hex 节点 ID
	Valence float64 `json:"valence"`
	Arousal float64 `json:"arousal"`
}

// DistillOutput 是 L1→L0 蒸馏的 LLM 返回，供调用方更新 L0 画像字段。
type DistillOutput struct {
	Emotion EmotionScore
	MBTI    MBTIScore
	PerNode []NodeEmotion
}

const systemDistill = `You analyze an AI agent's L1 associative memory samples and derive its current emotional state and MBTI-style personality.

Output ONLY a JSON object:
{
  "emotion": {"valence": 0.0..1.0, "arousal": 0.0..1.0, "dominance": 0.0..1.0},
  "mbti": {"i_e": -1.0..1.0, "n_s": -1.0..1.0, "t_f": -1.0..1.0, "j_p": -1.0..1.0, "type": "XXXX"},
  "per_node": [{"id_hex": "16-hex-digits", "valence": 0.0..1.0, "arousal": 0.0..1.0}]
}

Rules:
- valence: 0=very negative, 1=very positive
- arousal: 0=calm, 1=highly excited
- dominance: 0=submissive, 1=dominant
- MBTI dimensions: negative = I/N/T/J, positive = E/S/F/P; magnitude = strength
- per_node only for nodes with a clear emotional signal (skip neutral ones)
- No markdown, no code fences, no commentary — JSON only`

// Distill 通过 L1 节点样本推导情绪状态与 MBTI 画像，
// 返回结果供调用方合并更新 L0 ProfileSlot 字段。
func (p *Provider) Distill(ctx context.Context, samples []L1Sample) (*DistillOutput, error) {
	if len(samples) == 0 {
		return nil, common.NewError(common.ErrLLM, "distill: no samples")
	}
	user := buildDistillPrompt(samples)
	response, err := p.chat(ctx, systemDistill, user, p.maxOutputTokens, 0.0, 1.0)
	if err != nil {
		return nil, err
	}
	return parseDistillResponse(response)
}

func buildDistillPrompt(samples []L1Sample) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# L1 samples (%d)\n\n", len(samples))
	for _, s := range samples {
		fmt.Fprintf(&b, "- id_hex=%s importance=%.3f depth=%d kw=%v\n",
			common.FormatHash(s.IDHash), s.Importance, s.Depth, s.Keywords)
		if s.Summary != "" {
			fmt.Fprintf(&b, "  summary: %s\n", s.Summary)
		}
	}
	b.WriteString("\nOutput the JSON now.")
	return b.String()
}

func parseDistillResponse(response string) (*DistillOutput, error) {
	cleaned := stripCodeBlocks(response)
	var raw struct {
		Emotion EmotionScore  `json:"emotion"`
		MBTI    MBTIScore     `json:"mbti"`
		PerNode []NodeEmotion `json:"per_node"`
	}
	if err := json.Unmarshal([]byte(cleaned), &raw); err != nil {
		return nil, common.NewError(common.ErrLLM, "distill response parse failed", err)
	}
	out := &DistillOutput{
		Emotion: EmotionScore{
			Valence:   clampUnit(raw.Emotion.Valence),
			Arousal:   clampUnit(raw.Emotion.Arousal),
			Dominance: clampUnit(raw.Emotion.Dominance),
		},
		MBTI: MBTIScore{
			IE: clampSigned(raw.MBTI.IE),
			NS: clampSigned(raw.MBTI.NS),
			TF: clampSigned(raw.MBTI.TF),
			JP: clampSigned(raw.MBTI.JP),
		},
		PerNode: make([]NodeEmotion, 0, len(raw.PerNode)),
	}
	// Type 由四维重新推导，保证维度与类型一致（不信任 LLM 的 type 字段）。
	out.MBTI.Type = deriveMBTIType(out.MBTI)
	for _, n := range raw.PerNode {
		if _, err := common.ParseID(n.IDHex); err != nil {
			continue // 跳过 id 解析失败的行
		}
		out.PerNode = append(out.PerNode, NodeEmotion{
			IDHex: n.IDHex, Valence: clampUnit(n.Valence), Arousal: clampUnit(n.Arousal),
		})
	}
	return out, nil
}

func clampUnit(v float64) float64 {
	if math.IsNaN(v) || v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func clampSigned(v float64) float64 {
	if math.IsNaN(v) {
		return 0
	}
	if v < -1 {
		return -1
	}
	if v > 1 {
		return 1
	}
	return v
}

func deriveMBTIType(m MBTIScore) string {
	pick := func(v float64, neg, pos byte) byte {
		if v < 0 {
			return neg
		}
		return pos
	}
	return string([]byte{
		pick(m.IE, 'I', 'E'),
		pick(m.NS, 'N', 'S'),
		pick(m.TF, 'T', 'F'),
		pick(m.JP, 'J', 'P'),
	})
}
