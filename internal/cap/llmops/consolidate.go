// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// llm_consolidate.go: L2 topic compression call point — the Dream cycle
// asks the LLM which adjacent topics share a conversation thread and
// reconstructs merged keyword tracks into natural-language summaries.

package llmops

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

type L2Group struct {
	SceneID       uint64   `json:"scene_id"`
	NodeHashes    []uint64 `json:"node_hashes"`
	MergedSummary string   `json:"merged_summary"`
}

type ConsolidationOutput struct {
	L2Groups            []L2Group `json:"l2_groups"`
	L2CompressionNeeded bool      `json:"l2_compression_needed"`
}

const SystemConsolidate = `You analyze L2 chat memory topics, identify which adjacent topics belong to the same conversation thread, and reconstruct their keywords into natural text that reads like the original conversation.

Rules:
1. Scan from the most recent topic backwards — group adjacent topics that share the same conversation thread (same subject, causal chain, topic continuation, or semantic overlap)
2. Do NOT merge topics that are clearly about different subjects, even if they occur in the same scene
3. Compression target: the total number of remaining topics after merging must be 20 or fewer. If the input has more than 20 topics, you MUST merge enough groups to bring the count down to 20 or below
4. For each merged group, reconstruct the multi-turn keywords into a single coherent natural-language text — write it as if you are rewriting what was originally said, not summarizing. The keywords are a fact checklist: every keyword or phrase from every topic in the group MUST appear in the reconstructed text, either verbatim or as the exact fact it stands for. Never drop, merge away, or generalize a fact (e.g. "yesterday" must stay "yesterday", not become "recently")
5. No length limit on the reconstructed text — it must be as long as needed to faithfully preserve ALL details: names, numbers, dates, times (including relative references such as "yesterday", "last week", "next month" — keep them exactly as said), locations, cause-effect chains, emotional tone, attitudes, preferences, and specific facts
6. Preserve the emotional tone and attitude present in the keywords — if the original tone was frustrated, excited, curious, etc., the reconstructed text should reflect that
7. Preserve original language; keep mixed-language terms as-is; preserve numbers and proper nouns exactly
8. Echo scene_id and node_hashes EXACTLY as given in the input
9. When no compression is needed (no adjacent topics share a thread, or total topics already <= 20), output l2_groups as an empty array

Output ONLY valid JSON in this exact shape (no markdown, no code fences):
{
  "l2_groups": [
    {
      "scene_id": <number>,
      "node_hashes": [<number>, ...],
      "merged_summary": "<natural-language reconstruction of the original conversation from the keywords>"
    }
  ],
  "l2_compression_needed": <bool>
}
Every merged group MUST include non-empty merged_summary.`

// Consolidate decides whether a batch of L2 topics share a topic and
// returns compression groups preserving all details.
func Consolidate(ctx context.Context, chat Chat, topics []core.TopicSlot) (*ConsolidationOutput, error) {
	if len(topics) == 0 {
		return &ConsolidationOutput{L2Groups: []L2Group{}, L2CompressionNeeded: false}, nil
	}
	user := BuildConsolidatePrompt(topics)
	// Two-budget attempt: the first pass uses the configured ceiling; when
	// the response is truncated (finish_reason=length, common with reasoning
	// models), retry once with the full consolidation budget so merged
	// summaries are never cut mid-JSON.
	response, err := chat.ChatWithRetry(ctx, SystemConsolidate, user, minTokens(chat.MaxOutputTokens(), ConsolidationMaxTokens), ConsolidationMaxTokens)
	if err != nil {
		return nil, err
	}
	return parseConsolidateResponse(response)
}

// BuildConsolidatePrompt lists topics grouped by scene, sorted by user turn
// time (adjacency matters for merge judgment).
func BuildConsolidatePrompt(topics []core.TopicSlot) string {
	byScene := make(map[uint64][]core.TopicSlot)
	for _, t := range topics {
		byScene[t.SceneID] = append(byScene[t.SceneID], t)
	}
	sceneIDs := make([]uint64, 0, len(byScene))
	for sid := range byScene {
		sceneIDs = append(sceneIDs, sid)
	}
	slices.Sort(sceneIDs)

	var b strings.Builder
	fmt.Fprintf(&b, "# L2 Topic Data (%d scenes)\n\n", len(sceneIDs))
	for _, sid := range sceneIDs {
		nodes := byScene[sid]
		slices.SortStableFunc(nodes, func(a, b core.TopicSlot) int {
			if a.UserTimestamp != b.UserTimestamp {
				return cmp.Compare(a.UserTimestamp, b.UserTimestamp)
			}
			return cmp.Compare(a.ID, b.ID)
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
			MergedSummary: g.MergedSummary,
		})
	}
	return out, nil
}
