//go:build integration

package test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	memhop "github.com/qyiun666/MemHop/api"
	internal "github.com/qyiun666/MemHop/internal"
	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/test/testsupport"
)

// TestCoreCycleSearchUpdateDream exercises the full memory loop against real
// services: N Search+Update turns ingested into one scene, then Dream
// consolidation, then retrieval must still surface the consolidated facts
// (the fused summary preserves details) plus the newest facts.
func TestCoreCycleSearchUpdateDream(t *testing.T) {
	const turns = 24
	facts := []string{
		"我昨天（5月7日）去了 LGBTQ 支持小组，见到了 Caroline，她讲了很多鼓舞人心的故事。",
		"支持小组在市中心教堂地下室，每周三晚上 7 点活动，我付了 5 美元入场费。",
		"小组里有 12 个成员，主持人叫 David，他说小组已经运营了 3 年。",
		"Caroline 说她去年 11 月向父母出柜，妈妈很支持，爸爸花了 2 个月才接受。",
		"我表哥 Mark 上个月也出柜了，家里为他举办了庆祝晚餐，来了 8 个亲戚。",
		"小组下次活动是 5 月 14 日，主题是工作场所权益，邀请了一个律师来讲解。",
		"我在小组认识了 Emily，她是设计师，说可以帮助我做简历。",
		"支持小组的匿名规则很重要，David 强调不要透露成员身份。",
		"5 月 7 日的活动上，Caroline 分享了她参与彩虹游行（6 月 28 日）的计划。",
		"我考虑下周三带弟弟 Jake 一起去小组，他还未成年，需要家长陪同。",
		"Emily 说她在小组找到自信后换了工作，薪水涨了 30%。",
		"小组的微信群有 45 个人，日常讨论很活跃。",
		"David 提到小组明年会搬到更大的场地，预算需要 2 万元。",
		"我打算在 6 月彩虹游行帮忙当志愿者，Caroline 已经报名了。",
		"5 月 14 日的律师叫张律师，说可以免费提供首次咨询。",
		"Jake 同意下周三去，妈妈会开车送我们，晚上 6 点半出发。",
		"小组活动结束后大家常去附近的面馆聚餐，AA 制每人 25 元。",
		"Emily 帮我改了简历，周三面试前会再讨论一次。",
		"David 说小组最近获得了社区 5000 元赞助，用于印刷宣传册。",
		"6 月 28 日游行集合地点在市政府广场，早上 8 点开始。",
		"Caroline 邀请我参加她 7 月的生日聚会，在城东的公园，大约 20 人。",
		"我决定每周都去小组，已经加入了微信群。",
		"小组这周的活动取消了，因为教堂要装修。",
		"David 说下周会恢复活动，地点暂时改在社区中心。",
	}
	if len(facts) < turns {
		t.Fatalf("need %d facts, have %d", turns, len(facts))
	}

	cfg := &internal.MemHopConfig{
		DBPath:      filepath.Join(t.TempDir(), "cycle.meh"),
		VectorDim:   1024,
		EncoderAddr: "http://127.0.0.1:11434",
		EmbedModel:  "qllama/bge-m3:q4_k_m",
		Defaults:    *internal.DefaultMemHopDefaults,
	}
	if err := testsupport.LoadLLMConfig(cfg); err != nil {
		t.Skipf("skip: %v", err)
	}
	db, err := memhop.Open(cfg)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	// Phase 1: ingest via Search + Update (the real host pattern).
	base := time.Now().UnixMilli()
	for i, f := range facts {
		res, err := db.Search(context.Background(), internal.SearchQuery{Text: f, Timestamp: base + int64(i)*1000})
		if err != nil {
			t.Fatalf("search ingest %d: %v", i, err)
		}
		if err := db.Update(common.FormatHash(res.NewTopicID), "Agent: 明白了，已记录。", base+int64(i)*1000+500); err != nil {
			t.Fatalf("update ingest %d: %v", i, err)
		}
	}

	// Phase 2: Dream consolidation (LLM merge of the 24 same-topic turns).
	ok, err := db.Dream(context.Background(), "")
	if err != nil {
		t.Fatalf("dream: %v", err)
	}
	if !ok {
		t.Logf("dream reported no consolidation (no active scenes or below threshold)")
	}

	// Phase 3: retrieval must still surface the facts, including the newest
	// one added right before Dream and the merged summary details.
	res, err := db.Search(context.Background(), internal.SearchQuery{Text: "支持小组最近有什么安排？", Timestamp: base + 1_000_000})
	if err != nil {
		t.Fatalf("post-dream search: %v", err)
	}
	ctxText := gatherLocomoContext(db, res)
	for _, want := range []string{"5月7日", "David", "5000", "张律师", "社区中心"} {
		if !strings.Contains(ctxText, want) {
			t.Errorf("post-dream context missing %q", want)
		}
	}
	t.Logf("post-dream: %d contexts, %d chars, facts preserved", len(res.Contexts), len(ctxText))
}
