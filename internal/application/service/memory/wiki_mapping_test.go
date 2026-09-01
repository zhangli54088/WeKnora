package memory

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type memoryWikiMemoryRepoStub struct {
	interfaces.MemoryRepository
	item *types.MemoryItem
}

func (s *memoryWikiMemoryRepoStub) GetItem(
	_ context.Context, scope interfaces.MemoryScope, id string,
) (*types.MemoryItem, error) {
	if s.item != nil && s.item.ID == id && s.item.TenantID == scope.TenantID &&
		s.item.SubjectID == scope.SubjectID {
		return s.item, nil
	}
	return nil, nil
}

type memoryWikiKBServiceStub struct {
	interfaces.KnowledgeBaseService
	kb         *types.KnowledgeBase
	results    []*types.SearchResult
	lastParams types.SearchParams
}

func (s *memoryWikiKBServiceStub) GetKnowledgeBaseByID(
	_ context.Context, id string,
) (*types.KnowledgeBase, error) {
	if s.kb != nil && s.kb.ID == id {
		return s.kb, nil
	}
	return nil, nil
}

func (s *memoryWikiKBServiceStub) HybridSearch(
	_ context.Context, _ string, params types.SearchParams,
) ([]*types.SearchResult, error) {
	s.lastParams = params
	return s.results, nil
}

type memoryWikiLinkRepoStub struct {
	interfaces.MemoryWikiRepository
	pages map[string][]*types.WikiPage
	page  *types.WikiPage
	link  *types.MemoryWikiLink
}

func (s *memoryWikiLinkRepoStub) ListWikiPagesBySourceRefs(
	_ context.Context, _ uint64, _ string, knowledgeIDs []string,
) ([]*types.WikiPage, error) {
	var pages []*types.WikiPage
	for _, knowledgeID := range knowledgeIDs {
		pages = append(pages, s.pages[knowledgeID]...)
	}
	return pages, nil
}

func (s *memoryWikiLinkRepoStub) GetWikiPage(
	_ context.Context, tenantID uint64, knowledgeBaseID, pageID string,
) (*types.WikiPage, error) {
	if s.page == nil || s.page.ID != pageID || s.page.TenantID != tenantID ||
		knowledgeBaseID != "" && s.page.KnowledgeBaseID != knowledgeBaseID {
		return nil, nil
	}
	return s.page, nil
}

func (s *memoryWikiLinkRepoStub) UpsertLink(
	_ context.Context, scope interfaces.MemoryScope, link *types.MemoryWikiLink,
) (*types.MemoryWikiLink, error) {
	link.ID = "link-1"
	link.TenantID = scope.TenantID
	link.SubjectID = scope.SubjectID
	link.CreatedAt = time.Now()
	link.UpdatedAt = link.CreatedAt
	s.link = link
	return link, nil
}

func (s *memoryWikiLinkRepoStub) GetLink(
	_ context.Context, scope interfaces.MemoryScope, id string,
) (*types.MemoryWikiLink, error) {
	if s.link == nil || s.link.ID != id || s.link.TenantID != scope.TenantID ||
		s.link.SubjectID != scope.SubjectID {
		return nil, nil
	}
	return s.link, nil
}

func (s *memoryWikiLinkRepoStub) DeleteLink(
	_ context.Context, scope interfaces.MemoryScope, id string,
) (bool, error) {
	if s.link == nil || s.link.ID != id || s.link.TenantID != scope.TenantID ||
		s.link.SubjectID != scope.SubjectID {
		return false, nil
	}
	s.link = nil
	return true, nil
}

type memoryWikiProfileStub struct {
	interfaces.LearningProfileService
	synced  *types.MemoryWikiLink
	removed *types.MemoryWikiLink
}

func (s *memoryWikiProfileStub) SyncMemoryWikiLink(
	_ context.Context, link *types.MemoryWikiLink,
) error {
	s.synced = link
	return nil
}

func (s *memoryWikiProfileStub) RemoveMemoryWikiLinkEvidence(
	_ context.Context, link *types.MemoryWikiLink,
) error {
	s.removed = link
	return nil
}

func memoryWikiTestContext(t *testing.T, tenantID uint64, userID string) context.Context {
	t.Helper()
	ctx := context.WithValue(t.Context(), types.TenantIDContextKey, tenantID)
	return types.WithPrincipal(ctx, types.Principal{Type: types.PrincipalWebUser, ID: userID})
}

func newMemoryWikiCandidateService(
	results []*types.SearchResult, pages map[string][]*types.WikiPage,
) (*memoryWikiService, *memoryWikiKBServiceStub) {
	memoryRepo := &memoryWikiMemoryRepoStub{item: &types.MemoryItem{
		ID: "memory-1", TenantID: 1, SubjectID: "web_user:alice",
		Topic: "RAG", Content: "用户曾使用向量数据库搭建 RAG 系统",
	}}
	kbService := &memoryWikiKBServiceStub{
		kb: &types.KnowledgeBase{
			ID: "kb-1", TenantID: 1,
			IndexingStrategy: types.IndexingStrategy{
				VectorEnabled: true, KeywordEnabled: true, WikiEnabled: true,
			},
		},
		results: results,
	}
	return &memoryWikiService{
		memoryRepo: memoryRepo,
		linkRepo:   &memoryWikiLinkRepoStub{pages: pages},
		kbService:  kbService,
		profile:    &memoryWikiProfileStub{},
	}, kbService
}

func TestMemoryWikiCandidatesUseChunkEvidenceAndLimitTopK(t *testing.T) {
	results := []*types.SearchResult{
		{ID: "chunk-rag", KnowledgeID: "doc-1", KnowledgeBaseID: "kb-1", Score: 0.91},
		{ID: "chunk-vector", KnowledgeID: "doc-1", KnowledgeBaseID: "kb-1", Score: 0.83},
		{ID: "chunk-other", KnowledgeID: "doc-2", KnowledgeBaseID: "kb-1", Score: 0.71},
	}
	pages := map[string][]*types.WikiPage{
		"doc-1": {
			{ID: "page-rag", TenantID: 1, KnowledgeBaseID: "kb-1", Title: "RAG", Slug: "concept/rag", Status: types.WikiPageStatusPublished, SourceRefs: types.StringArray{"doc-1"}, ChunkRefs: types.StringArray{"chunk-rag"}},
			{ID: "page-vector", TenantID: 1, KnowledgeBaseID: "kb-1", Title: "Vector Database", Slug: "concept/vector-database", Status: types.WikiPageStatusPublished, SourceRefs: types.StringArray{"doc-1"}, ChunkRefs: types.StringArray{"chunk-vector"}},
			{ID: "page-summary", TenantID: 1, KnowledgeBaseID: "kb-1", Title: "Document summary", Slug: "summary/doc-1", Status: types.WikiPageStatusPublished, SourceRefs: types.StringArray{"doc-1"}},
		},
		"doc-2": {
			{ID: "page-other", TenantID: 1, KnowledgeBaseID: "kb-1", Title: "Other", Slug: "concept/other", Status: types.WikiPageStatusPublished, SourceRefs: types.StringArray{"doc-2"}, ChunkRefs: types.StringArray{"chunk-other"}},
			{ID: "foreign-page", TenantID: 2, KnowledgeBaseID: "kb-1", Title: "Foreign", Slug: "concept/foreign", Status: types.WikiPageStatusPublished, SourceRefs: types.StringArray{"doc-2"}, ChunkRefs: types.StringArray{"chunk-other"}},
		},
	}
	svc, kbService := newMemoryWikiCandidateService(results, pages)

	candidates, err := svc.FindCandidates(memoryWikiTestContext(t, 1, "alice"), "memory-1", "kb-1", 2)
	require.NoError(t, err)
	require.Len(t, candidates, 2)
	require.Equal(t, "page-rag", candidates[0].WikiPageID)
	require.Equal(t, types.MemoryWikiMethodChunkRef, candidates[0].Method)
	require.Equal(t, "page-vector", candidates[1].WikiPageID)
	require.Contains(t, kbService.lastParams.QueryText, "RAG")
	require.Contains(t, kbService.lastParams.QueryText, "向量数据库")
	require.Equal(t, minMemoryWikiSearch, kbService.lastParams.MatchCount)
	require.True(t, kbService.lastParams.SkipContextEnrichment)
}

func TestMemoryWikiCandidatesUseDiscountedSummaryFallback(t *testing.T) {
	results := []*types.SearchResult{
		{ID: "chunk-rag", KnowledgeID: "doc-1", KnowledgeBaseID: "kb-1", Score: 0.9},
	}
	pages := map[string][]*types.WikiPage{
		"doc-1": {
			{ID: "summary", TenantID: 1, KnowledgeBaseID: "kb-1", Title: "Summary", Slug: "summary/doc-1", Status: types.WikiPageStatusPublished, SourceRefs: types.StringArray{"doc-1"}},
		},
	}
	svc, _ := newMemoryWikiCandidateService(results, pages)

	candidates, err := svc.FindCandidates(memoryWikiTestContext(t, 1, "alice"), "memory-1", "kb-1", 5)
	require.NoError(t, err)
	require.Len(t, candidates, 1)
	require.InDelta(t, 0.72, candidates[0].Score, 0.0001)
	require.Equal(t, types.MemoryWikiMethodSourceRef, candidates[0].Method)
}

func TestMemoryWikiCandidatesReturnEmptyWithoutRetrieverHits(t *testing.T) {
	svc, _ := newMemoryWikiCandidateService(nil, nil)

	candidates, err := svc.FindCandidates(memoryWikiTestContext(t, 1, "alice"), "memory-1", "kb-1", 5)
	require.NoError(t, err)
	require.NotNil(t, candidates)
	require.Empty(t, candidates)
}

func TestMemoryWikiCandidatesRejectCrossTenantKB(t *testing.T) {
	svc, kbService := newMemoryWikiCandidateService(nil, nil)
	kbService.kb.TenantID = 2

	_, err := svc.FindCandidates(memoryWikiTestContext(t, 1, "alice"), "memory-1", "kb-1", 5)
	require.ErrorIs(t, err, ErrMemoryWikiKnowledgeBaseNotFound)
}

func TestMemoryWikiLinkSynchronizesAndRemovesLearningProfile(t *testing.T) {
	svc, _ := newMemoryWikiCandidateService(nil, nil)
	repo := svc.linkRepo.(*memoryWikiLinkRepoStub)
	repo.page = &types.WikiPage{
		ID: "page-a", TenantID: 1, KnowledgeBaseID: "kb-1",
		Title: "RAG", Slug: "concept/rag", Status: types.WikiPageStatusPublished,
	}
	profile := svc.profile.(*memoryWikiProfileStub)
	ctx := memoryWikiTestContext(t, 1, "alice")

	view, err := svc.UpsertLink(
		ctx, "memory-1", "page-a", 0.0155, types.MemoryWikiMethodChunkRef,
	)
	require.NoError(t, err)
	require.NotNil(t, view)
	require.NotNil(t, profile.synced)
	require.Equal(t, "link-1", profile.synced.ID)
	require.Equal(t, 0.0155, profile.synced.Score)

	require.NoError(t, svc.DeleteLink(ctx, "link-1"))
	require.NotNil(t, profile.removed)
	require.Equal(t, "link-1", profile.removed.ID)
	require.Nil(t, repo.link)
}
