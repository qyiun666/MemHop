// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// consolidate.go: the L2 consolidation call point — asks the LLM which adjacent
// topics share a conversation thread and reconstructs merged keyword tracks into
// natural-language summaries.

package llmops

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

type L2Group struct {
	NodeHashes    []uint64 `json:"node_hashes"`
	MergedSummary string   `json:"merged_summary"`
}

type ConsolidationOutput struct {
	L2Groups []L2Group `json:"l2_groups"`
}

// systemConsolidate states the contract for one pass. The topic count it aims at
// is the caller's configured compression floor, not a constant of this package:
// a number here that the engine does not run by would have the model merge to a
// target the next pass then refuses to consider reached.
func systemConsolidate(floor int) string {
	return fmt.Sprintf(`You analyze L2 chat memory topics, identify which adjacent topics belong to the same conversation thread, and reconstruct their keywords into natural text that reads like the original conversation.

Rules:
1. Scan from the most recent topic backwards — group adjacent topics that share the same conversation thread (same subject, causal chain, topic continuation, or semantic overlap)
2. Do NOT merge topics that are clearly about different subjects, even if they occur in the same scene. This rule outranks rule 3: an unrelated pair fused into one thread is a false memory, while an unmerged pair costs nothing but a later pass
3. Compression target: bring the number of remaining topics down toward %d or below, by as many groups as rule 2 actually allows — merge no further than that
4. For each merged group, reconstruct the multi-turn keywords into a single coherent natural-language text — write it as if you are rewriting what was originally said, not summarizing. The keywords are a fact checklist: every keyword or phrase from every topic in the group MUST appear in the reconstructed text, either verbatim or as the exact fact it stands for. Never drop, merge away, or generalize a fact (e.g. "yesterday" must stay "yesterday", not become "recently")
5. No length limit on the reconstructed text — it must be as long as needed to faithfully preserve ALL details: names, numbers, dates, times (including relative references such as "yesterday", "last week", "next month" — keep them exactly as said), locations, cause-effect chains, emotional tone, attitudes, preferences, and specific facts
6. Preserve the emotional tone and attitude present in the keywords — if the original tone was frustrated, excited, curious, etc., the reconstructed text should reflect that
7. Preserve original language; keep mixed-language terms as-is; preserve numbers and proper nouns exactly
8. Echo node_hashes EXACTLY as given in the input
9. When no compression is possible (no adjacent topics share a thread, or the total is already %d or fewer), output l2_groups as an empty array

Output ONLY valid JSON in this exact shape (no markdown, no code fences):
{
  "l2_groups": [
    {
      "node_hashes": [<number>, ...],
      "merged_summary": "<natural-language reconstruction of the original conversation from the keywords>"
    }
  ]
}
Every merged group MUST include non-empty merged_summary.`, floor, floor)
}

// Consolidate decides whether a batch of L2 topics share a topic and
// returns compression groups preserving all details. floor is the caller's
// configured topic count a pass compresses towards — the target the prompt states.
func Consolidate(ctx context.Context, chat Chat, topics []core.TopicSlot, floor int) (*ConsolidationOutput, error) {
	if len(topics) == 0 {
		return &ConsolidationOutput{L2Groups: []L2Group{}}, nil
	}
	system := systemConsolidate(floor)
	user := BuildConsolidatePrompt(topics)
	// Two-budget attempt: the first pass uses the configured ceiling; when
	// the response is truncated (finish_reason=length, common with reasoning
	// models), retry once at the endpoint's own ceiling so merged summaries are
	// never cut mid-JSON.
	primary := minTokens(chat.MaxOutputTokens(), ConsolidationMaxTokens)
	response, err := chat.ChatWithRetry(ctx, system, user, primary, escalationCeiling(chat))
	if err != nil {
		return nil, err
	}
	out, perr := parseConsolidateResponse(response)
	if perr == nil {
		return out, nil
	}
	// One format-constrained retry before failing the call; ExtractKeywords
	// applies the same self-healing pattern.
	retry, rerr := chat.Chat(ctx, system, user+consolidateFormatRetry, primary)
	if rerr != nil {
		return nil, perr
	}
	if out, perr = parseConsolidateResponse(retry); perr != nil {
		return nil, perr
	}
	return out, nil
}

// consolidateFormatRetry is appended to the user prompt for the
// format-constrained retry.
const consolidateFormatRetry = `

Output ONLY valid JSON in the exact shape from the system prompt. No markdown, no code fences, no commentary.`

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
		slices.SortStableFunc(nodes, core.CompareTopicOrder)
		fmt.Fprintf(&b, "## scene_id = %d\n", sid)
		for _, n := range nodes {
			fmt.Fprintf(&b, "- id=%d depth=%d kw=%v\n", n.ID, n.Depth, n.FusedKeywords)
		}
		b.WriteByte('\n')
	}
	b.WriteString("Decide which topics belong to the same topic and output the merged groups now.")
	return b.String()
}

// parseConsolidateResponse parses the LLM reply; node_hashes accept JSON
// numbers or quoted strings. A group whose members do not parse is not a group
// the engine could apply, and the caller counts what it could not apply apart
// from what it chose not to.
func parseConsolidateResponse(response string) (*ConsolidationOutput, error) {
	cleaned := stripCodeBlocks(response)
	var raw struct {
		L2Groups []struct {
			NodeHashes    []json.RawMessage `json:"node_hashes"`
			MergedSummary string            `json:"merged_summary"`
		} `json:"l2_groups"`
	}
	if err := json.Unmarshal([]byte(cleaned), &raw); err != nil {
		return nil, common.NewError(common.ErrLLM, "consolidate response parse failed", err)
	}
	out := &ConsolidationOutput{L2Groups: make([]L2Group, 0, len(raw.L2Groups))}
	for _, g := range raw.L2Groups {
		hashes := make([]uint64, 0, len(g.NodeHashes))
		for _, h := range g.NodeHashes {
			v, err := parseUint64Flex(h)
			if err != nil {
				return nil, common.NewError(common.ErrLLM, "consolidate node_hashes parse failed", err)
			}
			hashes = append(hashes, v)
		}
		out.L2Groups = append(out.L2Groups, L2Group{NodeHashes: hashes, MergedSummary: g.MergedSummary})
	}
	return out, nil
}
