//go:build integration

package test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	internal "github.com/qyiun666/MemHop/internal"
	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/test/testsupport"
)

// gatherLocomoContext flattens the returned topics' keyword tracks (user +
// agent + fused) with their timestamps into one searchable text. It reflects
// what a host sees after Search: the L2 keyword view of each returned topic.
func gatherLocomoContext(_ *testsupport.Handle, res *internal.SearchResult) string {
	var sb strings.Builder
	for i := range res.Contexts {
		c := &res.Contexts[i]
		if c.UserTimestamp > 0 {
			fmt.Fprintf(&sb, "[user: %s] ", time.UnixMilli(c.UserTimestamp).UTC().Format("2006-01-02 15:04"))
		}
		if c.AgentTimestamp > 0 {
			fmt.Fprintf(&sb, "[agent: %s] ", time.UnixMilli(c.AgentTimestamp).UTC().Format("2006-01-02 15:04"))
		}
		sb.WriteByte('\n')
		for _, kw := range c.UserKeywords {
			sb.WriteString(kw)
			sb.WriteByte(' ')
		}
		for _, kw := range c.AgentKeywords {
			sb.WriteString(kw)
			sb.WriteByte(' ')
		}
		for _, kw := range c.FusedKeywords {
			sb.WriteString(kw)
			sb.WriteByte(' ')
		}
	}
	return sb.String()
}

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

	db := testsupport.OpenMemHop(t)
	defer db.Close()

	// Phase 1: ingest via Search + Update (the real host pattern), checking
	// L0/L1/L4 consistency every few turns — the periodic verification a host
	// should be able to rely on at any point in the loop.
	base := time.Now().UnixMilli()
	var sceneID uint64
	for i, f := range facts {
		res, err := db.Search(context.Background(), internal.SearchQuery{Text: f, Timestamp: base + int64(i)*1000})
		if err != nil {
			t.Fatalf("search ingest %d: %v", i, err)
		}
		if len(res.Contexts) > 0 && sceneID == 0 {
			sceneID = res.Contexts[0].SceneID
		}
		if err := db.Update(common.FormatHash(res.NewTopicID), "Agent: 明白了，已记录。", base+int64(i)*1000+500); err != nil {
			t.Fatalf("update ingest %d: %v", i, err)
		}
		// Periodic consistency check: L0 profile readable, L1 scene graph
		// present, L4 archive holds the just-ingested utterance verbatim.
		if (i+1)%8 == 0 {
			if _, err := db.GetL0(); err != nil {
				t.Fatalf("GetL0 at turn %d: %v", i+1, err)
			}
			scenes, err := db.ListScenes()
			if err != nil || len(scenes) == 0 {
				t.Fatalf("ListScenes at turn %d: scenes=%d err=%v", i+1, len(scenes), err)
			}
			if sc, err := db.SceneContext(common.FormatHash(sceneID)); err != nil || sc.TopicCount == 0 {
				t.Fatalf("SceneContext at turn %d: topics=%d err=%v", i+1, sc.TopicCount, err)
			}
			arc, err := db.SearchL4(internal.L4Query{
				Start: base + int64(i)*1000 - 100,
				End:   base + int64(i)*1000 + 600,
			})
			if err != nil || len(arc) == 0 {
				t.Fatalf("SearchL4 at turn %d: archives=%d err=%v", i+1, len(arc), err)
			}
			// The window holds both the user utterance and the agent reply;
			// one archive must carry the raw user text verbatim.
			verbatim := false
			for _, a := range arc {
				if strings.Contains(a.Content, f) {
					verbatim = true
					break
				}
			}
			if !verbatim {
				t.Errorf("L4 at turn %d does not hold the raw utterance verbatim", i+1)
			}
			t.Logf("periodic check @turn %d: L0 ok, scenes=%d, L4 verbatim=%v", i+1, len(scenes), verbatim)
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

	// Phase 2.5: post-Dream L0/L1 consistency — profile still readable, the
	// scene graph still exposes the consolidated scene with topics.
	if _, err := db.GetL0(); err != nil {
		t.Fatalf("GetL0 after Dream: %v", err)
	}
	scenes, err := db.ListScenes()
	if err != nil || len(scenes) == 0 {
		t.Fatalf("ListScenes after Dream: scenes=%d err=%v", len(scenes), err)
	}
	sc, err := db.SceneContext(common.FormatHash(sceneID))
	if err != nil || sc.TopicCount == 0 {
		t.Fatalf("SceneContext after Dream: topics=%d err=%v", sc.TopicCount, err)
	}
	if sc.TopicCount >= len(facts) {
		t.Logf("note: Dream merged nothing this run (topics=%d == ingested %d)", sc.TopicCount, len(facts))
	} else {
		t.Logf("post-Dream scene topics=%d (compressed from %d)", sc.TopicCount, len(facts))
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
