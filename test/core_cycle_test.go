//go:build integration

package test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	memhop "github.com/qyiun666/MemHop/api"
	internal "github.com/qyiun666/MemHop/internal"
	"github.com/qyiun666/MemHop/test/testsupport"
)

// gatherSessionSurface renders the host session's read surface: each depth-1
// topic with its turn timestamps and its single keyword track.
func gatherSessionSurface(res *memhop.SearchResult) string {
	var sb strings.Builder
	for i := range res.Topics {
		c := &res.Topics[i]
		if c.UserTimestamp > 0 {
			fmt.Fprintf(&sb, "[user: %s] ", time.UnixMilli(c.UserTimestamp).UTC().Format("2006-01-02 15:04"))
		}
		if c.AgentTimestamp > 0 {
			fmt.Fprintf(&sb, "[agent: %s] ", time.UnixMilli(c.AgentTimestamp).UTC().Format("2006-01-02 15:04"))
		}
		sb.WriteByte('\n')
		for _, kw := range c.FusedKeywords {
			sb.WriteString(kw)
			sb.WriteByte(' ')
		}
	}
	return sb.String()
}

// TestCoreCycleUpdateDream exercises the full memory loop against real
// services: N turns settled into one host session, then Dream consolidation.
// It pins what the re-designed loop promises: originals stay verbatim in L4,
// the read surface is the distilled keyword track, and consolidation shrinks
// that surface without dropping the facts.
func TestCoreCycleUpdateDream(t *testing.T) {
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

	base := time.Now().UnixMilli()
	sceneID, _, err := db.OpenTurn("")
	if err != nil {
		t.Fatalf("open session: %v", err)
	}

	// Phase 1: settle each turn with Update (the real host pattern), checking
	// L0/L2/L4 consistency every few turns.
	for i, f := range facts {
		ts := base + int64(i)*1000
		_, turnID, err := db.OpenTurn(sceneID)
		if err != nil {
			t.Fatalf("open turn %d: %v", i, err)
		}
		if _, err := db.Update(memhop.TurnUpdate{
			SceneID: sceneID, TopicID: turnID, UserText: f, UserTS: ts,
			AgentText: "Agent: 明白了，已记录。", AgentTS: ts + 500,
		}); err != nil {
			t.Fatalf("update ingest %d: %v", i, err)
		}
		if (i+1)%8 == 0 {
			if _, err := db.GetL0(); err != nil {
				t.Fatalf("GetL0 at turn %d: %v", i+1, err)
			}
			res, err := db.Search(memhop.SearchQuery{SceneID: sceneID})
			if err != nil || len(res.Topics) == 0 {
				t.Fatalf("session read at turn %d: topics=%d err=%v", i+1, len(res.Topics), err)
			}
			arc, err := db.SearchL4(internal.L4Query{Start: ts - 100, End: ts + 600})
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
			t.Logf("periodic check @turn %d: L0 ok, surface=%d topics, L4 verbatim=%v", i+1, len(res.Topics), verbatim)
		}
	}

	before, err := db.Search(memhop.SearchQuery{SceneID: sceneID})
	if err != nil {
		t.Fatalf("session read before dream: %v", err)
	}
	surfaceBefore := len(before.Topics)

	// Phase 2: Dream consolidation (LLM merge of the same-topic turns).
	rep, err := db.Dream(context.Background(), sceneID)
	if err != nil {
		t.Fatalf("dream: %v", err)
	}
	if rep == nil {
		t.Fatal("dream returned no report")
	}
	t.Logf("dream consolidated %d scene(s), compressed %d topic group(s)", rep.ConsolidatedScenes, rep.L2TopicsCompressed)

	// Phase 2.5: post-Dream L0/L2 consistency — profile readable, the session
	// still resolves and still has a surface.
	if _, err := db.GetL0(); err != nil {
		t.Fatalf("GetL0 after Dream: %v", err)
	}
	after, err := db.Search(memhop.SearchQuery{SceneID: sceneID})
	if err != nil {
		t.Fatalf("session read after dream: %v", err)
	}
	if len(after.Topics) == 0 {
		t.Fatal("session surface went empty after consolidation")
	}
	if rep.ConsolidatedScenes > 0 && len(after.Topics) >= surfaceBefore {
		t.Errorf("consolidation reported work but the surface did not shrink: %d -> %d", surfaceBefore, len(after.Topics))
	}
	t.Logf("post-Dream surface = %d topics (was %d)", len(after.Topics), surfaceBefore)

	// Phase 3: consolidation must not cost the host its facts. The originals
	// are the source of truth in L4; the read surface must still carry the
	// distilled keywords.
	for _, want := range facts {
		hit, err := db.SearchL4(internal.L4Query{Keyword: want[:18]})
		if err != nil {
			t.Fatalf("SearchL4 for %q: %v", want[:18], err)
		}
		if len(hit) == 0 {
			t.Errorf("fact lost from L4 after consolidation: %.24s", want)
		}
	}
	surfaceText := gatherSessionSurface(after)
	if strings.TrimSpace(surfaceText) == "" {
		t.Error("post-dream surface carries no keywords")
	}
	t.Logf("post-dream surface: %d topics, %d chars of keywords", len(after.Topics), len(surfaceText))
}
