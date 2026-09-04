package memory

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

func memoryCleanupHarness(t *testing.T) (*Service, interfaces.LearningProfileService, *gorm.DB, context.Context) {
	t.Helper()
	svc, db, tenants := newMemoryHarness(t)
	for _, page := range []*types.WikiPage{
		{ID: "x", TenantID: 1, KnowledgeBaseID: "kb", Slug: "x", Title: "X"},
		{ID: "y", TenantID: 1, KnowledgeBaseID: "kb", Slug: "y", Title: "Y"},
		{ID: "foreign", TenantID: 2, KnowledgeBaseID: "kb-other", Slug: "foreign", Title: "Other"},
	} { require.NoError(t, db.Create(page).Error) }
	profile := NewLearningProfileService(repository.NewLearningProfileRepository(db), repository.NewMemoryWikiRepository(db))
	return svc, profile, db, enabledCtx(t, tenants, 1, "alice")
}

func seedMemoryCleanupLink(t *testing.T, db *gorm.DB, profile interfaces.LearningProfileService, ctx context.Context, itemID, linkID, pageID string) {
	t.Helper()
	scope, err := ResolveScope(ctx); require.NoError(t, err)
	var count int64
	require.NoError(t, db.Model(&types.MemoryItem{}).Where("id = ?", itemID).Count(&count).Error)
	if count == 0 {
		require.NoError(t, db.Create(&types.MemoryItem{ID: itemID, TenantID: scope.TenantID, SubjectID: scope.SubjectID,
			Kind: types.MemoryKindFact, Content: "memory " + itemID, Status: types.MemoryStatusActive, ValidFrom: time.Now()}).Error)
	}
	kbID := "kb"
	if scope.TenantID == 2 { kbID = "kb-other" }
	link := &types.MemoryWikiLink{ID: linkID, TenantID: scope.TenantID, SubjectID: scope.SubjectID,
		MemoryItemID: itemID, WikiPageID: pageID, KnowledgeBaseID: kbID, Method: types.MemoryWikiMethodManual}
	require.NoError(t, db.Create(link).Error)
	require.NoError(t, profile.SyncMemoryWikiLink(ctx, link))
}

func seedMemoryCleanupChat(t *testing.T, profile interfaces.LearningProfileService, ctx context.Context, pageID string) {
	t.Helper()
	require.NoError(t, profile.RecordChatInteractions(ctx, "session", "message-"+pageID, "kb", []*types.MemoryWikiCandidate{
		{WikiPageID: pageID, KnowledgeBaseID: "kb", Method: types.MemoryWikiMethodChunkRef},
	}))
}

func requireMemoryCleanupState(t *testing.T, db *gorm.DB, ctx context.Context, pageID, status string, count int) {
	t.Helper()
	scope, err := ResolveScope(ctx); require.NoError(t, err)
	state, err := repository.NewLearningProfileRepository(db).GetKnowledgeState(ctx, scope, pageID)
	require.NoError(t, err)
	if status == "unknown" { require.Nil(t, state); return }
	require.NotNil(t, state)
	require.Equal(t, status, state.Status)
	require.Equal(t, count, state.EvidenceCount)
}

func requireMemoryCleanupRows(t *testing.T, db *gorm.DB, ctx context.Context, items, links, evidence int64) {
	t.Helper()
	scope, err := ResolveScope(ctx); require.NoError(t, err)
	for table, expected := range map[string]int64{"memory_items": items, "memory_wiki_links": links, "learning_evidences": evidence} {
		var count int64
		require.NoError(t, db.Table(table).Where("tenant_id = ? AND subject_id = ?", scope.TenantID, scope.SubjectID).Count(&count).Error)
		require.Equal(t, expected, count, table)
	}
}

func TestMemoryDeleteLearningCleanupMemoryOnly(t *testing.T) {
	svc, profile, db, ctx := memoryCleanupHarness(t)
	seedMemoryCleanupLink(t, db, profile, ctx, "a", "link-a", "x")
	requireMemoryCleanupState(t, db, ctx, "x", "familiar", 1)
	require.NoError(t, svc.DeleteItem(ctx, "a"))
	requireMemoryCleanupRows(t, db, ctx, 0, 0, 0)
	requireMemoryCleanupState(t, db, ctx, "x", "unknown", 0)
}

func TestMemoryDeleteLearningCleanupKeepsChat(t *testing.T) {
	svc, profile, db, ctx := memoryCleanupHarness(t)
	seedMemoryCleanupLink(t, db, profile, ctx, "a", "link-a", "x")
	// The same polymorphic source ID must not make chat evidence deletable.
	require.NoError(t, profile.RecordChatInteractions(ctx, "session", "link-a", "kb", []*types.MemoryWikiCandidate{
		{WikiPageID: "x", KnowledgeBaseID: "kb", Method: types.MemoryWikiMethodChunkRef},
	}))
	requireMemoryCleanupState(t, db, ctx, "x", "familiar", 2)
	require.NoError(t, svc.DeleteItem(ctx, "a"))
	requireMemoryCleanupRows(t, db, ctx, 0, 0, 1)
	requireMemoryCleanupState(t, db, ctx, "x", "exposed", 1)
	evidence, err := profile.ListEvidence(ctx, "x"); require.NoError(t, err)
	require.Equal(t, types.LearningEvidenceSourceChatMessage, evidence[0].SourceType)
	require.Equal(t, "link-a", evidence[0].SourceID)
}

func TestMemoryDeleteLearningCleanupKeepsOtherMemory(t *testing.T) {
	svc, profile, db, ctx := memoryCleanupHarness(t)
	seedMemoryCleanupLink(t, db, profile, ctx, "a", "link-a", "x")
	seedMemoryCleanupLink(t, db, profile, ctx, "b", "link-b", "x")
	require.NoError(t, svc.DeleteItem(ctx, "a"))
	requireMemoryCleanupRows(t, db, ctx, 1, 1, 1)
	requireMemoryCleanupState(t, db, ctx, "x", "familiar", 1)
	evidence, err := profile.ListEvidence(ctx, "x"); require.NoError(t, err)
	require.Equal(t, "link-b", evidence[0].SourceID)
}

func TestMemoryDeleteLearningCleanupMultiplePages(t *testing.T) {
	svc, profile, db, ctx := memoryCleanupHarness(t)
	seedMemoryCleanupLink(t, db, profile, ctx, "a", "link-x", "x")
	seedMemoryCleanupLink(t, db, profile, ctx, "a", "link-y", "y")
	seedMemoryCleanupChat(t, profile, ctx, "y")
	require.NoError(t, svc.DeleteItem(ctx, "a"))
	requireMemoryCleanupRows(t, db, ctx, 0, 0, 1)
	requireMemoryCleanupState(t, db, ctx, "x", "unknown", 0)
	requireMemoryCleanupState(t, db, ctx, "y", "exposed", 1)
}

func TestMemoryDeleteLearningCleanupScopeIsolation(t *testing.T) {
	svc, profile, db, ctx := memoryCleanupHarness(t)
	bob := memoryWikiTestContext(t, 1, "bob")
	foreign := memoryWikiTestContext(t, 2, "alice")
	seedMemoryCleanupLink(t, db, profile, ctx, "a", "link-a", "x")
	seedMemoryCleanupLink(t, db, profile, bob, "b", "link-b", "x")
	seedMemoryCleanupLink(t, db, profile, foreign, "c", "link-c", "foreign")
	for _, id := range []string{"b", "c"} { require.ErrorIs(t, svc.DeleteItem(ctx, id), ErrItemNotFound) }
	require.NoError(t, svc.DeleteItem(ctx, "a"))
	requireMemoryCleanupRows(t, db, bob, 1, 1, 1)
	requireMemoryCleanupRows(t, db, foreign, 1, 1, 1)
	requireMemoryCleanupState(t, db, bob, "x", "familiar", 1)
	requireMemoryCleanupState(t, db, foreign, "foreign", "familiar", 1)
	_, err := svc.Clear(ctx); require.NoError(t, err)
	requireMemoryCleanupRows(t, db, bob, 1, 1, 1)
	requireMemoryCleanupRows(t, db, foreign, 1, 1, 1)
}

func TestMemoryClearLearningCleanupKeepsChatAndDeduplicatesPages(t *testing.T) {
	svc, profile, db, ctx := memoryCleanupHarness(t)
	seedMemoryCleanupLink(t, db, profile, ctx, "a", "link-a", "x")
	seedMemoryCleanupLink(t, db, profile, ctx, "b", "link-b", "x")
	seedMemoryCleanupLink(t, db, profile, ctx, "b", "link-y", "y")
	seedMemoryCleanupChat(t, profile, ctx, "y")
	scope, err := ResolveScope(ctx); require.NoError(t, err)
	// Inspect the actual coordinator's affected pages without committing changes.
	require.ErrorIs(t, svc.repo.WithLearningCleanup(ctx, scope, "", func(_ interfaces.MemoryRepository, _ interfaces.LearningProfileRepository, pages []string) error {
		require.Equal(t, []string{"x", "y"}, pages)
		return gorm.ErrInvalidData
	}), gorm.ErrInvalidData)
	removed, err := svc.Clear(ctx); require.NoError(t, err); require.Equal(t, int64(2), removed)
	requireMemoryCleanupRows(t, db, ctx, 0, 0, 1)
	requireMemoryCleanupState(t, db, ctx, "x", "unknown", 0)
	requireMemoryCleanupState(t, db, ctx, "y", "exposed", 1)
	removed, err = svc.Clear(ctx); require.NoError(t, err); require.Zero(t, removed)
	requireMemoryCleanupRows(t, db, ctx, 0, 0, 1)
}

func TestMemoryDeleteAndClearLearningCleanupRollback(t *testing.T) {
	for _, clear := range []bool{false, true} {
		name := "delete"; if clear { name = "clear" }
		t.Run(name, func(t *testing.T) {
			svc, profile, db, ctx := memoryCleanupHarness(t)
			seedMemoryCleanupLink(t, db, profile, ctx, "a", "link-a", "x")
			seedMemoryCleanupChat(t, profile, ctx, "x")
			require.NoError(t, db.Exec("CREATE TRIGGER fail_memory_delete BEFORE DELETE ON memory_items BEGIN SELECT RAISE(ABORT, 'injected delete failure'); END").Error)
			if clear { _, err := svc.Clear(ctx); require.Error(t, err) } else { require.Error(t, svc.DeleteItem(ctx, "a")) }
			requireMemoryCleanupRows(t, db, ctx, 1, 1, 2)
			requireMemoryCleanupState(t, db, ctx, "x", "familiar", 2)
		})
	}
}
