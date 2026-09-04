package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

func newLearningProfileDataHarness(t *testing.T) (*gorm.DB, interfaces.LearningProfileRepository) {
	t.Helper()
	db, repo := newLearningProfileRepositoryHarness(t)
	require.NoError(t, db.AutoMigrate(&types.MemoryItem{}, &types.MemoryTopicStat{}, &types.MemoryDocAffinity{}, &types.MemoryWikiLink{}, &types.MemorySubject{}, &types.MemoryItemEmbedding{}))
	// Public resources are sentinels, not dependencies of profile deletion.
	require.NoError(t, db.Exec("CREATE TABLE knowledge_bases (id TEXT PRIMARY KEY, tenant_id INTEGER)").Error)
	require.NoError(t, db.Exec("INSERT INTO knowledge_bases VALUES ('kb-a', 1)").Error)
	return db, repo
}

func seedLearningProfileData(t *testing.T, db *gorm.DB, scope interfaces.MemoryScope, suffix, pageID string) {
	t.Helper()
	now := time.Now()
	for _, row := range []interface{}{
		&types.MemoryItem{ID: "item-" + suffix, TenantID: scope.TenantID, SubjectID: scope.SubjectID, Kind: "fact", Content: "saved memory " + suffix, ValidFrom: now},
		&types.MemoryTopicStat{ID: "topic-" + suffix, TenantID: scope.TenantID, SubjectID: scope.SubjectID, NormalizedKey: suffix, Aliases: types.MemoryTopicAliases{}},
		&types.MemoryDocAffinity{ID: "doc-" + suffix, TenantID: scope.TenantID, SubjectID: scope.SubjectID, KnowledgeID: "knowledge-" + suffix},
		&types.MemorySubject{ID: "subject-" + suffix, TenantID: scope.TenantID, SubjectID: scope.SubjectID, Enabled: true},
		&types.MemoryItemEmbedding{ItemID: "item-" + suffix, TenantID: scope.TenantID, SubjectID: scope.SubjectID},
		&types.MemoryWikiLink{ID: "link-" + suffix, TenantID: scope.TenantID, SubjectID: scope.SubjectID, MemoryItemID: "item-" + suffix, WikiPageID: pageID, KnowledgeBaseID: "kb-a", Method: "manual"},
		&types.LearningEvidence{ID: "evidence-" + suffix, TenantID: scope.TenantID, SubjectID: scope.SubjectID, WikiPageID: pageID, EvidenceType: "memory_link", Level: "familiarity", SourceType: "memory_wiki_link", SourceID: "link-" + suffix, OccurredAt: now},
		&types.UserKnowledgeState{ID: "state-" + suffix, TenantID: scope.TenantID, SubjectID: scope.SubjectID, WikiPageID: pageID, Status: "familiar", EvidenceCount: 1, LastEvidenceAt: now},
	} {
		require.NoError(t, db.Create(row).Error)
	}
}

func TestLearningProfileDataExportScopeAndEmpty(t *testing.T) {
	db, repo := newLearningProfileDataHarness(t)
	seedLearningProfilePage(t, db, 1, "page-a", "kb-a")
	scopes := []interfaces.MemoryScope{{TenantID: 1, SubjectID: "web_user:alice"}, {TenantID: 1, SubjectID: "web_user:bob"}, {TenantID: 2, SubjectID: "web_user:alice"}}
	for i, scope := range scopes { seedLearningProfileData(t, db, scope, fmt.Sprint(i), "page-a") }
	for i, scope := range scopes {
		snapshot, err := repo.ExportSnapshot(t.Context(), scope)
		require.NoError(t, err)
		require.Len(t, snapshot.Memory.Items, 1)
		require.Equal(t, "item-"+fmt.Sprint(i), snapshot.Memory.Items[0].ID)
		require.Len(t, snapshot.Memory.Topics, 1)
		require.Len(t, snapshot.Memory.Documents, 1)
		require.Len(t, snapshot.Links, 1)
		require.Equal(t, "link-"+fmt.Sprint(i), snapshot.Links[0].ID)
		require.Len(t, snapshot.Evidence, 1)
		require.Equal(t, "evidence-"+fmt.Sprint(i), snapshot.Evidence[0].ID)
		require.Len(t, snapshot.States, 1)
		require.Equal(t, "page-a", snapshot.States[0].WikiPageID)
		require.Equal(t, "page-a", snapshot.States[0].Title)
		require.Equal(t, "concept/page-a", snapshot.States[0].Slug)
		require.Equal(t, "kb-a", snapshot.States[0].KnowledgeBaseID)
	}
	empty, err := repo.ExportSnapshot(t.Context(), interfaces.MemoryScope{TenantID: 3, SubjectID: "web_user:empty"})
	require.NoError(t, err)
	require.Equal(t, &types.LearningProfileSnapshot{Memory: types.LearningProfileMemoryExport{Items: []*types.MemoryItem{}, Topics: []*types.MemoryTopicStat{}, Documents: []*types.MemoryDocAffinity{}}, Links: []*types.MemoryWikiLink{}, Evidence: []*types.LearningEvidence{}, States: []*types.KnowledgeStateExport{}}, empty)
}

func TestLearningProfileDataClearScopePreservesResourcesAndIsIdempotent(t *testing.T) {
	db, repo := newLearningProfileDataHarness(t)
	seedLearningProfilePage(t, db, 1, "page-a", "kb-a")
	scopes := []interfaces.MemoryScope{{TenantID: 1, SubjectID: "web_user:alice"}, {TenantID: 1, SubjectID: "web_user:bob"}, {TenantID: 2, SubjectID: "web_user:alice"}}
	for i, scope := range scopes { seedLearningProfileData(t, db, scope, fmt.Sprint(i), "page-a") }
	// The resource belongs to tenant 1 even for a stored tenant 2 projection.
	// Deletion must never reinterpret that resource tenant as profile scope.
	for _, scope := range []interfaces.MemoryScope{scopes[2], scopes[0]} {
		counts, err := repo.ClearProfile(t.Context(), scope)
		require.NoError(t, err)
		require.Equal(t, &types.LearningProfileDeleteResult{MemoryWikiLinksDeleted: 1, LearningEvidencesDeleted: 1, KnowledgeStatesDeleted: 1}, counts)
		snapshot, err := repo.ExportSnapshot(t.Context(), scope)
		require.NoError(t, err)
		require.Empty(t, snapshot.Links); require.Empty(t, snapshot.Evidence); require.Empty(t, snapshot.States)
		require.Len(t, snapshot.Memory.Items, 1); require.Len(t, snapshot.Memory.Topics, 1); require.Len(t, snapshot.Memory.Documents, 1)
		counts, err = repo.ClearProfile(t.Context(), scope)
		require.NoError(t, err)
		require.Equal(t, &types.LearningProfileDeleteResult{}, counts)
	}
	other, err := repo.ExportSnapshot(t.Context(), scopes[1])
	require.NoError(t, err)
	require.Len(t, other.Links, 1); require.Len(t, other.Evidence, 1); require.Len(t, other.States, 1)
	for table, expected := range map[string]int64{"wiki_pages": 1, "knowledge_bases": 1, "memory_items": 3, "memory_subjects": 3, "memory_item_embeddings": 3} {
		var count int64
		require.NoError(t, db.Table(table).Count(&count).Error)
		require.Equal(t, expected, count, table)
	}
}

func TestLearningProfileDataClearRollsBack(t *testing.T) {
	for _, table := range []string{"user_knowledge_states", "memory_wiki_links"} {
		t.Run(table, func(t *testing.T) {
			db, repo := newLearningProfileDataHarness(t)
			seedLearningProfilePage(t, db, 1, "page-a", "kb-a")
			scope := interfaces.MemoryScope{TenantID: 1, SubjectID: "web_user:alice"}
			seedLearningProfileData(t, db, scope, "a", "page-a")
			require.NoError(t, db.Exec("CREATE TRIGGER fail_profile_delete BEFORE DELETE ON "+table+" BEGIN SELECT RAISE(ABORT, 'injected deletion failure'); END").Error)
			counts, err := repo.ClearProfile(t.Context(), scope)
			require.Error(t, err); require.Nil(t, counts)
			snapshot, err := repo.ExportSnapshot(t.Context(), scope)
			require.NoError(t, err)
			require.Len(t, snapshot.Links, 1); require.Len(t, snapshot.Evidence, 1); require.Len(t, snapshot.States, 1)
		})
	}
}

func TestLearningProfileDataRejectsInvalidScope(t *testing.T) {
	_, repo := newLearningProfileDataHarness(t)
	for _, scope := range []interfaces.MemoryScope{{}, {TenantID: 1}, {SubjectID: "web_user:alice"}} {
		_, err := repo.ExportSnapshot(context.Background(), scope); require.Error(t, err)
		_, err = repo.ClearProfile(context.Background(), scope); require.Error(t, err)
	}
}
