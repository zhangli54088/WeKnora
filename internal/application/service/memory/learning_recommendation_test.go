package memory

import (
	"context"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

func recommendationPage(id string, links ...string) *types.WikiPage {
	return &types.WikiPage{ID: id, Slug: id, Title: id, TenantID: 1, KnowledgeBaseID: "kb-a", PageType: types.WikiPageTypeConcept, Status: types.WikiPageStatusPublished, OutLinks: links}
}

func recommendationState(id, status string) *types.UserKnowledgeState {
	return &types.UserKnowledgeState{WikiPageID: id, TenantID: 1, SubjectID: "web_user:alice", Status: status, EvidenceCount: 1}
}

func rankRecommendationFixture(pages []*types.WikiPage, states map[string]*types.UserKnowledgeState, memory map[string]bool, now time.Time, limit int) []types.LearningRecommendation {
	g := buildRecommendationGraph(pages)
	c, _ := generateLearningCandidates(g, states, limit)
	return rankLearningCandidates(c, g, states, memory, now)
}

func TestLearningRecommendationCandidatesAndRanking(t *testing.T) {
	pages := []*types.WikiPage{recommendationPage("a", "x"), recommendationPage("b", "y"), recommendationPage("x"), recommendationPage("y")}
	states := map[string]*types.UserKnowledgeState{"a": recommendationState("a", "familiar"), "b": recommendationState("b", "exposed")}
	ranked := rankRecommendationFixture(pages, states, nil, time.Now(), 5)
	require.Len(t, ranked, 2)
	require.Equal(t, "x", ranked[0].WikiPageID)
	require.Greater(t, ranked[0].Score, ranked[1].Score)
	for _, status := range []string{"exposed", "familiar", "mastered"} {
		states["x"] = recommendationState("x", status)
		ranked = rankRecommendationFixture(pages, states, nil, time.Now(), 5)
		for _, r := range ranked { require.NotEqual(t, "x", r.WikiPageID) }
	}
	require.Empty(t, rankRecommendationFixture(pages, nil, nil, time.Now(), 5))
}

func TestLearningRecommendationMultipleAnchorsAndCappedDegree(t *testing.T) {
	pages := []*types.WikiPage{recommendationPage("a", "x", "x"), recommendationPage("b", "x"), recommendationPage("c", "y"), recommendationPage("x"), recommendationPage("y")}
	states := map[string]*types.UserKnowledgeState{"a": recommendationState("a", "familiar"), "b": recommendationState("b", "familiar"), "c": recommendationState("c", "exposed")}
	ranked := rankRecommendationFixture(pages, states, nil, time.Now(), 5)
	require.Equal(t, "x", ranked[0].WikiPageID)
	require.Len(t, ranked[0].SupportingNodes, 2)
	require.Equal(t, 0.5, ranked[0].ScoreComponents.MultiAnchor)
	for i:=0; i<10; i++ { id:=fmt.Sprintf("anchor-%02d",i); pages=append(pages,recommendationPage(id,"x")); states[id]=recommendationState(id,"familiar") }
	ranked = rankRecommendationFixture(pages, states, nil, time.Now(), 5)
	require.Equal(t, 1.0, ranked[0].ScoreComponents.MultiAnchor)
	require.LessOrEqual(t, ranked[0].Score, 1.0)
}

func TestLearningRecommendationTwoHopFallbackAndCycles(t *testing.T) {
	pages := []*types.WikiPage{recommendationPage("a", "a", "b", "b"), recommendationPage("b", "a", "c"), recommendationPage("c", "b")}
	states := map[string]*types.UserKnowledgeState{"a": recommendationState("a", "familiar")}
	ranked := rankRecommendationFixture(pages, states, nil, time.Now(), 5)
	require.Len(t, ranked, 2)
	require.Equal(t, 1, ranked[0].Hop)
	require.Equal(t, 2, ranked[1].Hop)
	require.Equal(t, []string{"a","b","c"}, ranked[1].SupportingNodes[0].Path)
	require.Len(t, ranked[0].SupportingNodes, 1)
	require.Len(t, rankRecommendationFixture(pages, states, nil, time.Now(), 1), 1)
	weakOneHop := scoreLearningCandidate(types.RecommendationScoreComponents{Structural: 1, AnchorStrength: 0.3})
	strongTwoHop := scoreLearningCandidate(types.RecommendationScoreComponents{Structural: 0.45, AnchorStrength: 1, MultiAnchor: 1, Recency: 1, LongTermMemory: 1})
	require.Greater(t, weakOneHop, strongTwoHop)
}

func TestLearningRecommendationRecencyMemoryAndScoreSafety(t *testing.T) {
	now := time.Date(2026,9,3,12,0,0,0,time.UTC)
	require.Equal(t, 0.0, recommendationRecency(time.Time{}, now))
	require.Equal(t, 1.0, recommendationRecency(now.Add(time.Hour), now))
	require.InDelta(t, math.Exp(-1), recommendationRecency(now.Add(-30*24*time.Hour), now), 1e-10)
	require.Less(t, recommendationRecency(now.Add(-3650*24*time.Hour), now), 0.001)
	require.Equal(t, 1.0, recommendationRecency(now.In(time.FixedZone("other",8*3600)),now))
	pages:=[]*types.WikiPage{recommendationPage("a","b"),recommendationPage("b")}
	state:=recommendationState("a","familiar")
	states:=map[string]*types.UserKnowledgeState{"a":state}
	base:=rankRecommendationFixture(pages,states,nil,now,5)[0]
	state.LastEvidenceAt=now
	recent:=rankRecommendationFixture(pages,states,nil,now,5)[0]
	require.InDelta(t, recommendationRecencyWeight, recent.Score-base.Score,1e-10)
	memory:=rankRecommendationFixture(pages,states,map[string]bool{"a":true},now,5)[0]
	require.InDelta(t,recommendationMemoryWeight,memory.Score-recent.Score,1e-10)
	require.Contains(t,memory.ReasonCodes,"supported_by_long_term_memory")
	for _, v:=range []float64{-10,0,1,5,math.NaN(),math.Inf(1)} {
		score:=scoreLearningCandidate(types.RecommendationScoreComponents{Structural:v,AnchorStrength:v,MultiAnchor:v,Recency:v,LongTermMemory:v})
		require.False(t,math.IsNaN(score));require.GreaterOrEqual(t,score,0.0);require.LessOrEqual(t,score,1.0)
	}
}

func TestLearningRecommendationStableRankingAndTraversalBound(t *testing.T) {
	pages:=[]*types.WikiPage{recommendationPage("a","c","b"),recommendationPage("c"),recommendationPage("b")}
	pages[1].Title="Same title";pages[2].Title="Same title"
	states:=map[string]*types.UserKnowledgeState{"a":recommendationState("a","familiar")}
	now:=time.Now()
	expected:=rankRecommendationFixture(pages,states,nil,now,5)
	require.Equal(t,"b",expected[0].WikiPageID)
	for i:=0;i<20;i++ { pages[1],pages[2]=pages[2],pages[1]; require.Equal(t,expected,rankRecommendationFixture(pages,states,nil,now,5)) }
	// Dense graph exercises both the edge cap and bounded expansion.
	pages=nil
	for i:=0;i<250;i++ { pages=append(pages,recommendationPage(fmt.Sprintf("p%03d",i))) }
	for _,p:=range pages { for _,q:=range pages { p.OutLinks=append(p.OutLinks,q.Slug) } }
	g:=buildRecommendationGraph(pages)
	require.True(t,g.truncated);require.Len(t,g.edges,recommendationMaxEdges)
	candidates,truncated:=generateLearningCandidates(g,map[string]*types.UserKnowledgeState{"p000":recommendationState("p000","exposed")},20)
	require.True(t,truncated);require.LessOrEqual(t,len(candidates),len(pages)-1)
	// Even a dense graph with no eligible unknowns stops the second-hop work.
	allKnown:=map[string]*types.UserKnowledgeState{}
	for _,p:=range pages { allKnown[p.ID]=recommendationState(p.ID,"familiar") }
	g.truncated=false // Isolate the traversal budget from the already-tested edge cap.
	candidates,truncated=generateLearningCandidates(g,allKnown,20)
	require.True(t,truncated);require.Empty(t,candidates)
}

func TestLearningRecommendationIncomingWikiLinksAreAdjacencyNotPrerequisites(t *testing.T) {
	pages:=[]*types.WikiPage{recommendationPage("anchor"),recommendationPage("candidate","anchor")}
	states:=map[string]*types.UserKnowledgeState{"anchor":recommendationState("anchor","familiar")}
	g:=buildRecommendationGraph(pages)
	candidates,_:=generateLearningCandidates(g,states,5)
	ranked:=rankLearningCandidates(candidates,g,states,nil,time.Now())
	require.Len(t,ranked,1);require.Equal(t,"candidate",ranked[0].WikiPageID)
	require.Equal(t,[]string{"adjacent_to_familiar"},ranked[0].ReasonCodes)
	contextGraph:=recommendationContextGraph(g,ranked)
	require.Equal(t,[]types.WikiGraphEdge{{Source:"candidate",Target:"anchor"}},contextGraph.Edges)
}

type recommendationKBRepoFake struct { interfaces.KnowledgeBaseRepository; kb *types.KnowledgeBase }
func (f *recommendationKBRepoFake) GetKnowledgeBaseByID(context.Context,string)(*types.KnowledgeBase,error){return f.kb,nil}
type recommendationWikiRepoFake struct { interfaces.WikiPageRepository; pages []*types.WikiPage; tenant uint64; kb string; limit int }
func (f *recommendationWikiRepoFake) ListLearningGraphPages(_ context.Context,tenant uint64,kb string,limit int)([]*types.WikiPage,error){f.tenant=tenant;f.kb=kb;f.limit=limit;return f.pages,nil}
type recommendationProfileRepoFake struct { interfaces.LearningProfileRepository; signals *types.LearningRecommendationSignals; scope interfaces.MemoryScope }
func (f *recommendationProfileRepoFake) ListRecommendationSignals(_ context.Context,scope interfaces.MemoryScope,_ []string)(*types.LearningRecommendationSignals,error){f.scope=scope;return f.signals,nil}

func TestLearningRecommendationServiceScopeAndEmptyResults(t *testing.T) {
	kb:=&recommendationKBRepoFake{kb:&types.KnowledgeBase{ID:"kb-a",TenantID:1,IndexingStrategy:types.IndexingStrategy{WikiEnabled:true}}}
	wiki:=&recommendationWikiRepoFake{pages:[]*types.WikiPage{recommendationPage("a","b","foreign","other-kb"),recommendationPage("b"),recommendationPage("foreign"),recommendationPage("other-kb")}}
	wiki.pages[2].TenantID=2;wiki.pages[3].KnowledgeBaseID="kb-other"
	profile:=&recommendationProfileRepoFake{signals:&types.LearningRecommendationSignals{States:[]*types.UserKnowledgeState{recommendationState("a","familiar")}}}
	svc:=NewLearningRecommendationService(kb,wiki,profile)
	ctx:=memoryWikiTestContext(t,1,"alice")
	view,err:=svc.ListRecommendations(ctx,"kb-a",5)
	require.NoError(t,err);require.Len(t,view.Recommendations,1);require.Equal(t,"b",view.Recommendations[0].WikiPageID)
	require.Equal(t,uint64(1),wiki.tenant);require.Equal(t,"kb-a",wiki.kb)
	require.Equal(t,interfaces.MemoryScope{TenantID:1,SubjectID:"web_user:alice"},profile.scope)
	profile.signals.States[0].SubjectID="web_user:bob"
	view,err=svc.ListRecommendations(ctx,"kb-a",5);require.NoError(t,err);require.Empty(t,view.Recommendations)
	kb.kb.TenantID=2
	_,err=svc.ListRecommendations(ctx,"kb-a",5);require.ErrorIs(t,err,ErrMemoryWikiKnowledgeBaseNotFound)
	kb.kb.TenantID=1;kb.kb.IndexingStrategy.WikiEnabled=false
	view,err=svc.ListRecommendations(ctx,"kb-a",5);require.NoError(t,err);require.False(t,view.WikiEnabled);require.NotNil(t,view.Recommendations)
	kb.kb.IndexingStrategy.WikiEnabled=true
	wiki.pages=nil
	for i:=0;i<types.LearningRecommendationMaxGraphNodes+1;i++ {wiki.pages=append(wiki.pages,recommendationPage(fmt.Sprintf("p%04d",i)))}
	view,err=svc.ListRecommendations(ctx,"kb-a",5);require.NoError(t,err);require.True(t,view.Truncated)
	require.Equal(t,types.LearningRecommendationMaxGraphNodes+1,wiki.limit)
}

func TestLearningRecommendationClosedLoopAndMappingScoreIgnored(t *testing.T) {
	profile,repo,db:=newLearningProfileServiceHarness(t)
	ctx:=memoryWikiTestContext(t,1,"alice")
	page:=recommendationPage("candidate")
	require.NoError(t,db.Create(page).Error)
	require.NoError(t,db.Model(&types.WikiPage{}).Where("id = ?","page-a").Update("out_links",types.StringArray{"candidate"}).Error)
	link:=learningProfileTestLink("link-a",time.Now())
	require.NoError(t,profile.SyncMemoryWikiLink(ctx,link))
	svc:=NewLearningRecommendationService(&recommendationKBRepoFake{kb:&types.KnowledgeBase{ID:"kb-a",TenantID:1,IndexingStrategy:types.IndexingStrategy{WikiEnabled:true}}},repository.NewWikiPageRepository(db),repo)
	before,err:=svc.ListRecommendations(ctx,"kb-a",5);require.NoError(t,err);require.Len(t,before.Recommendations,1)
	link.Score=0.01
	require.NoError(t,profile.SyncMemoryWikiLink(ctx,link))
	afterScore,err:=svc.ListRecommendations(ctx,"kb-a",5);require.NoError(t,err)
	require.Equal(t,before.Recommendations[0].Score,afterScore.Recommendations[0].Score)
	require.NoError(t,profile.RecordChatInteractions(ctx,"session","message","kb-a",[]*types.MemoryWikiCandidate{{WikiPageID:"candidate",KnowledgeBaseID:"kb-a",Method:types.MemoryWikiMethodChunkRef}}))
	after,err:=svc.ListRecommendations(ctx,"kb-a",5);require.NoError(t,err);require.Empty(t,after.Recommendations)
	state,err:=repo.GetKnowledgeState(ctx,interfaces.MemoryScope{TenantID:1,SubjectID:"web_user:alice"},"candidate")
	require.NoError(t,err);require.Equal(t,types.UserKnowledgeStatusExposed,state.Status)
}
