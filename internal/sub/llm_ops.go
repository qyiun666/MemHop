// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// llm_ops.go hosts the Provider's three LLM call points: keyword extraction,
// L2 consolidation and L1→L0 distillation.

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

const systemKeywords = `You extract keywords that capture the core semantics of the text.

Rules:
- The keyword list, when read together, must convey the same meaning as the original text
- Include named entities, terms, topics, actions, time, locations, and any key details
- Keep mixed-language terms (English+Chinese) in their original form
- Preserve numbers and proper nouns exactly as-is
- Do not set a limit on the number of keywords; extract all that are needed to preserve the meaning
- Do not include greetings, filler words, or non-informational content
- Output ONLY valid JSON: {"keywords":[...]}, no markdown, no code fences`

// ExtractKeywords extracts semantic keywords whose union represents the
// text's core meaning (unlimited count).
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

type L2Group struct {
	SceneID       uint64   `json:"scene_id"`
	NodeHashes    []uint64 `json:"node_hashes"`
	MergedTitle   string   `json:"merged_title"`
	MergedSummary string   `json:"merged_summary"`
}

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

Output ONLY valid JSON in this exact shape (no markdown, no code fences):
{
  "l2_groups": [
    {
      "scene_id": <number>,
      "node_hashes": [<number>, ...],
      "merged_title": "<short topic title>",
      "merged_summary": "<detailed summary preserving all facts>"
    }
  ],
  "l2_compression_needed": <bool>
}
Every merged group MUST include non-empty merged_title and merged_summary.`

// Consolidate decides whether a batch of L2 topics share a topic and
// returns compression groups preserving all details.
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

// buildConsolidatePrompt lists topics grouped by scene, sorted by user turn
// time (adjacency matters for merge judgment).
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

// parseConsolidateResponse parses the LLM reply; scene_id/node_hashes
// accept JSON numbers or quoted strings.
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

// parseUint64Flex parses a JSON number or quoted string as uint64, decimal
// first then hex (0x prefix).
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

// L1Sample is a distill input assembled from an L1 node and its topics
// (keywords come from linked L2 topics).
type L1Sample struct {
	IDHash     uint64
	Keywords   []string
	Summary    string
	Importance float32
	Depth      uint8
}

type EmotionScore struct {
	Valence   float64 `json:"valence"`
	Arousal   float64 `json:"arousal"`
	Dominance float64 `json:"dominance"`
}

// MBTIScore holds four MBTI dimensions in [-1,1]; Type is derived from the dimensions.
type MBTIScore struct {
	IE   float64 `json:"i_e"`
	NS   float64 `json:"n_s"`
	TF   float64 `json:"t_f"`
	JP   float64 `json:"j_p"`
	Type string  `json:"type"`
}

type NodeEmotion struct {
	IDHex   string  `json:"id_hex"`
	Valence float64 `json:"valence"`
	Arousal float64 `json:"arousal"`
}

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

// Distill derives emotional state and MBTI profile from L1 node samples
// for L0 profile merging.
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
	// Type re-derived from the four dimensions (LLM type field not trusted).
	out.MBTI.Type = deriveMBTIType(out.MBTI)
	for _, n := range raw.PerNode {
		if _, err := common.ParseID(n.IDHex); err != nil {
			continue // skip rows with unparsable ids
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

// CrystallizePlugin is one reusable capability package extracted from a
// trajectory; its manifest follows the L5 plugin layout.
type CrystallizePlugin struct {
	Name       string              `json:"name"`
	Trigger    string              `json:"trigger"`
	PluginType string              `json:"plugin_type"`
	Manifest   core.PluginManifest `json:"manifest"`
}

type CrystallizeOutput struct {
	Plugins []CrystallizePlugin `json:"plugins"`
}

const systemCrystallize = `You analyze an agent's operation trajectory and extract reusable capabilities as plugins.

Rules:
- Only extract capabilities that are clearly reusable (appear at least twice or are obviously generic procedures)
- A plugin is a self-contained capability package; choose its primary type label (plugin_type) from: skill, mcp, toolkit, workflow, service, or another short label that fits
- Fill the manifest sections that apply (skills / mcps / tools / prompts / services); omit empty sections
- Each manifest entry has a name, an optional description, and an optional config (JSON string or template text)
- Do not invent tools or services that are not present in the trajectory
- When no reusable capability exists, output plugins as an empty array

Output ONLY valid JSON in this exact shape (no markdown, no code fences):
{
  "plugins": [
    {
      "name": "<short plugin name>",
      "trigger": "<when this plugin applies>",
      "plugin_type": "<skill|mcp|toolkit|workflow|service|...>",
      "manifest": {
        "skills": [{"name": "...", "description": "...", "config": "..."}],
        "mcps": [{"name": "...", "config": "<endpoint or connection JSON>"}],
        "tools": [{"name": "<tool name>", "description": "..."}],
        "prompts": [{"name": "...", "config": "<prompt template>"}],
        "services": [{"name": "...", "config": "<service definition JSON>"}]
      }
    }
  ]
}`

// Crystallize extracts reusable plugins from a trajectory event batch.
func (p *Provider) Crystallize(ctx context.Context, events []core.TrajectorySlot) (*CrystallizeOutput, error) {
	if len(events) == 0 {
		return &CrystallizeOutput{Plugins: []CrystallizePlugin{}}, nil
	}
	user := buildCrystallizePrompt(events)
	response, err := p.chat(ctx, systemCrystallize, user, p.maxOutputTokens, 0.0, 1.0)
	if err != nil {
		return nil, err
	}
	return parseCrystallizeResponse(response)
}

// buildCrystallizePrompt lists trajectory events in sequence order.
func buildCrystallizePrompt(events []core.TrajectorySlot) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Operation Trajectory (%d events)\n\n", len(events))
	for _, ev := range events {
		fmt.Fprintf(&b, "[seq=%d type=%s] %s\n", ev.Seq, ev.EventType, ev.Payload)
	}
	b.WriteString("\nExtract reusable plugins now.")
	return b.String()
}

// parseCrystallizeResponse parses the LLM reply, dropping malformed rows.
func parseCrystallizeResponse(response string) (*CrystallizeOutput, error) {
	cleaned := stripCodeBlocks(response)
	var raw struct {
		Plugins []struct {
			Name       string `json:"name"`
			Trigger    string `json:"trigger"`
			PluginType string `json:"plugin_type"`
			Manifest   struct {
				Skills   []core.PluginItem `json:"skills"`
				MCPs     []core.PluginItem `json:"mcps"`
				Tools    []core.PluginItem `json:"tools"`
				Prompts  []core.PluginItem `json:"prompts"`
				Services []core.PluginItem `json:"services"`
			} `json:"manifest"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal([]byte(cleaned), &raw); err != nil {
		return nil, common.NewError(common.ErrLLM, "crystallize response parse failed", err)
	}
	out := &CrystallizeOutput{Plugins: make([]CrystallizePlugin, 0, len(raw.Plugins))}
	for _, p := range raw.Plugins {
		if strings.TrimSpace(p.Name) == "" || strings.TrimSpace(p.Trigger) == "" {
			continue
		}
		out.Plugins = append(out.Plugins, CrystallizePlugin{
			Name: p.Name, Trigger: p.Trigger, PluginType: p.PluginType,
			Manifest: core.PluginManifest{
				Skills:   p.Manifest.Skills,
				MCPs:     p.Manifest.MCPs,
				Tools:    p.Manifest.Tools,
				Prompts:  p.Manifest.Prompts,
				Services: p.Manifest.Services,
			},
		})
	}
	return out, nil
}
