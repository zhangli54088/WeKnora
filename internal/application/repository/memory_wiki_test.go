package repository

import (
	"fmt"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func newMemoryWikiRepositoryHarness(t *testing.T) (*gorm.DB, interfaces.MemoryWikiRepository) {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())),
		&gorm.Config{Logger: logger.Discard},
	)
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&types.MemoryItem{}, &types.WikiPage{}, &types.MemoryWikiLink{},
	))
	return db, NewMemoryWikiRepository(db)
}

func seedMemoryWikiEndpoint(
	t *testing.T, db *gorm.DB, tenantID uint64, subjectID, memoryID, kbID, pageID string,
) {
	t.Helper()
	require.NoError(t, db.Create(&types.MemoryItem{
		ID: memoryID, TenantID: tenantID, SubjectID: subjectID,
		Kind: types.MemoryKindFact, Content: "memory " + memoryID,
		Status: types.MemoryStatusActive, ValidFrom: time.Now(),
	}).Error)
	require.NoError(t, db.Create(&types.WikiPage{
		ID: pageID, TenantID: tenantID, KnowledgeBaseID: kbID,
		Slug: "concept/" + pageID, Title: pageID,
		PageType: types.WikiPageTypeConcept, Status: types.WikiPageStatusPublished,
	}).Error)
}

func TestMemoryWikiRepositoryUpsertIsIdempotent(t *testing.T) {
	db, repo := newMemoryWikiRepositoryHarness(t)
	scope := interfaces.MemoryScope{TenantID: 1, SubjectID: "web_user:alice"}
	seedMemoryWikiEndpoint(t, db, 1, scope.SubjectID, "memory-a", "kb-a", "page-a")

	first, err := repo.UpsertLink(t.Context(), scope, &types.MemoryWikiLink{
		MemoryItemID: "memory-a", WikiPageID: "page-a", KnowledgeBaseID: "kb-a",
		Score: 0.7, Method: types.MemoryWikiMethodChunkRef,
	})
	require.NoError(t, err)
	second, err := repo.UpsertLink(t.Context(), scope, &types.MemoryWikiLink{
		MemoryItemID: "memory-a", WikiPageID: "page-a", KnowledgeBaseID: "kb-a",
		Score: 0.9, Method: types.MemoryWikiMethodChunkRef,
	})
	require.NoError(t, err)
	require.Equal(t, first.ID, second.ID)
	require.InDelta(t, 0.9, second.Score, 0.0001)

	var count int64
	require.NoError(t, db.Model(&types.MemoryWikiLink{}).Count(&count).Error)
	require.Equal(t, int64(1), count)
}

func TestMemoryWikiRepositoryTenantAndSubjectIsolation(t *testing.T) {
	db, repo := newMemoryWikiRepositoryHarness(t)
	scopeA := interfaces.MemoryScope{TenantID: 1, SubjectID: "web_user:alice"}
	scopeB := interfaces.MemoryScope{TenantID: 2, SubjectID: "web_user:bob"}
	seedMemoryWikiEndpoint(t, db, 1, scopeA.SubjectID, "memory-a", "kb-a", "page-a")
	seedMemoryWikiEndpoint(t, db, 2, scopeB.SubjectID, "memory-b", "kb-b", "page-b")

	linkB, err := repo.UpsertLink(t.Context(), scopeB, &types.MemoryWikiLink{
		MemoryItemID: "memory-b", WikiPageID: "page-b", KnowledgeBaseID: "kb-b",
		Score: 0.8, Method: types.MemoryWikiMethodChunkRef,
	})
	require.NoError(t, err)

	linksA, err := repo.ListLinks(t.Context(), scopeA)
	require.NoError(t, err)
	require.Empty(t, linksA)

	_, err = repo.UpsertLink(t.Context(), scopeA, &types.MemoryWikiLink{
		MemoryItemID: "memory-b", WikiPageID: "page-b", KnowledgeBaseID: "kb-b",
		Score: 1, Method: types.MemoryWikiMethodManual,
	})
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)

	deleted, err := repo.DeleteLink(t.Context(), scopeA, linkB.ID)
	require.NoError(t, err)
	require.False(t, deleted)
	linksB, err := repo.ListLinks(t.Context(), scopeB)
	require.NoError(t, err)
	require.Len(t, linksB, 1)

	deleted, err = repo.DeleteLink(t.Context(), scopeB, linkB.ID)
	require.NoError(t, err)
	require.True(t, deleted)
}

func TestMemoryWikiRepositoryRejectsCrossTenantPageLookup(t *testing.T) {
	db, repo := newMemoryWikiRepositoryHarness(t)
	seedMemoryWikiEndpoint(t, db, 2, "web_user:bob", "memory-b", "kb-b", "page-b")

	page, err := repo.GetWikiPage(t.Context(), 1, "kb-b", "page-b")
	require.NoError(t, err)
	require.Nil(t, page)
}

func TestMemoryWikiRepositoryListsSourceRefsOnSQLite(t *testing.T) {
	db, repo := newMemoryWikiRepositoryHarness(t)
	pages := []*types.WikiPage{
		{ID: "bare", TenantID: 1, KnowledgeBaseID: "kb-a", Slug: "concept/bare", Title: "Bare", PageType: types.WikiPageTypeConcept, Status: types.WikiPageStatusPublished, SourceRefs: types.StringArray{"doc-a"}, ChunkRefs: types.StringArray{"chunk-a"}},
		{ID: "titled", TenantID: 1, KnowledgeBaseID: "kb-a", Slug: "concept/titled", Title: "Titled", PageType: types.WikiPageTypeConcept, Status: types.WikiPageStatusPublished, SourceRefs: types.StringArray{"doc-b|Document B"}, ChunkRefs: types.StringArray{"chunk-b"}},
		{ID: "archived", TenantID: 1, KnowledgeBaseID: "kb-a", Slug: "concept/archived", Title: "Archived", PageType: types.WikiPageTypeConcept, Status: types.WikiPageStatusArchived, SourceRefs: types.StringArray{"doc-a"}},
		{ID: "foreign", TenantID: 2, KnowledgeBaseID: "kb-a", Slug: "concept/foreign", Title: "Foreign", PageType: types.WikiPageTypeConcept, Status: types.WikiPageStatusPublished, SourceRefs: types.StringArray{"doc-a"}},
	}
	for _, page := range pages {
		require.NoError(t, db.Create(page).Error)
	}

	got, err := repo.ListWikiPagesBySourceRefs(t.Context(), 1, "kb-a", []string{"doc-a", "doc-b"})
	require.NoError(t, err)
	require.Len(t, got, 2)
	ids := map[string]bool{}
	for _, page := range got {
		ids[page.ID] = true
	}
	require.True(t, ids["bare"])
	require.True(t, ids["titled"])
}
