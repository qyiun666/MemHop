// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package dream

import (
	"encoding/json"
	"strings"

	"github.com/qyiun666/MemHop/internal/common/mherrors"
)

// FactExtractionResult holds the extracted atomic facts from dialogue content.
type FactExtractionResult struct {
	Facts []string `json:"facts"`
}

const systemFactExtraction = `You extract atomic memory facts from dialogue content. Output JSON only.

Rules:
- Each fact is a complete, self-contained sentence with subject + predicate + object
- Resolve all pronouns to concrete names/entities (e.g. "他" → actual name from context)
- Preserve ALL temporal references exactly as-is (dates, "去年三月", "上周三", "三年")
- Preserve ALL locations, person names, organization names exactly
- Keep bilingual terms as-is (e.g. "Docker容器", "JWT认证")
- Include relationship context (who did what to whom, when, where)
- Do NOT truncate, generalize, or omit details
- Each fact must be independently understandable without other facts
- No limit on number of facts — extract ALL meaningful information
- Do NOT include greetings, filler words, or non-informational content

Examples:
Input: "我叫林小明，在杭州阿里巴巴工作，后端工程师。女朋友王芳是设计师，在一起三年了。"
Output: {"facts":["林小明在杭州阿里巴巴工作，职位是后端工程师","林小明的女朋友叫王芳，王芳是设计师","林小明和王芳在一起三年了"]}

Input: "上周三我带小花去打了第二针疫苗，花了200块，医生说她很健康。"
Output: {"facts":["上周三带小花去打了第二针疫苗","打疫苗花了200块","医生说小花很健康"]}

Input: "我去年三月从北京搬到了深圳，现在在腾讯做算法工程师，年薪80万。"
Output: {"facts":["去年三月从北京搬到了深圳","现在在腾讯工作，职位是算法工程师","年薪80万"]}
`

// ExtractFacts uses LLM to extract atomic memory facts from dialogue content.
// Returns error if LLM is unavailable or extraction fails.
func ExtractFacts(llm ChatProvider, content string) (*FactExtractionResult, error) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return &FactExtractionResult{Facts: []string{}}, nil
	}

	userPrompt := "Extract atomic facts from:\n" + trimmed + "\n\nOutput JSON."
	response, err := callLLMWithRetry(llm, systemFactExtraction, userPrompt, 4096, 0.0)
	if err != nil {
		return nil, mherrors.NewError(mherrors.ErrLLM, "fact extraction LLM call failed", err)
	}
	if strings.TrimSpace(response) == "" {
		return nil, mherrors.NewError(mherrors.ErrLLM, "fact extraction returned empty response")
	}

	result, err := parseFactResponse(response)
	if err != nil {
		return nil, mherrors.NewError(mherrors.ErrLLM, "fact extraction response parse failed", err)
	}
	return result, nil
}

func parseFactResponse(response string) (*FactExtractionResult, error) {
	cleaned := stripCodeBlocksLLM(response)
	var raw struct {
		Facts []string `json:"facts"`
	}
	if err := json.Unmarshal([]byte(cleaned), &raw); err != nil {
		return nil, mherrors.NewError(mherrors.ErrDeserialization, "parse fact extraction", err)
	}
	facts := filterEmptyStrings(raw.Facts)
	return &FactExtractionResult{Facts: facts}, nil
}
