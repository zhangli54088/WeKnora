package repository

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

func newLearningProfileRepositoryHarness(
	t *testing.T,
) (*gorm.DB, interfaces.LearningProfileRepository) {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())),
		&gorm.Config{Logger: logger.Discard},
	)
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&types.WikiPage{}, &types.LearningEvidence{}, &types.UserKnowledgeState{},
	))
	return db, NewLearningProfileRepository(db)
}

func seedLearningProfilePage(
	t *testing.T, db *gorm.DB, tenantID uint64, pageID, knowledgeBaseID string,
) {
	t.Helper()
	require.NoError(t, db.Create(&types.WikiPage{
		ID: pageID, TenantID: tenantID, KnowledgeBaseID: knowledgeBaseID,
		Slug: "concept/" + pageID, Title: pageID,
		PageType: types.WikiPageTypeConcept, Status: types.WikiPageStatusPublished,
	}).Error)
}

func TestLearningProfileRepositoryEvidenceScopeIsolation(t *testing.T) {
	db, repo := newLearningProfileRepositoryHarness(t)
	scopeA := interfaces.MemoryScope{TenantID: 1, SubjectID: "web_user:alice"}
	scopeOtherUser := interfaces.MemoryScope{TenantID: 1, SubjectID: "web_user:bob"}
	scopeOtherTenant := interfaces.MemoryScope{TenantID: 2, SubjectID: "web_user:alice"}
	seedLearningProfilePage(t, db, 1, "page-a", "kb-a")
	seedLearningProfilePage(t, db, 2, "page-b", "kb-b")

	_, err := repo.UpsertEvidence(t.Context(), scopeA, &types.LearningEvidence{
		WikiPageID: "page-a", EvidenceType: types.LearningEvidenceTypeMemoryLink,
		Level: types.LearningEvidenceLevelFamiliarity,
		SourceType: types.LearningEvidenceSourceMemoryWikiLink, SourceID: "link-a",
		Weight: 1, OccurredAt: time.Now(),
	})
	require.NoError(t, err)

	for _, scope := range []interfaces.MemoryScope{scopeOtherUser, scopeOtherTenant} {
		got, err := repo.ListEvidence(t.Context(), scope, "")
		require.NoError(t, err)
		require.Empty(t, got)
	}
	_, err = repo.UpsertEvidence(t.Context(), scopeA, &types.LearningEvidence{
		WikiPageID: "page-b", EvidenceType: types.LearningEvidenceTypeMemoryLink,
		Level: types.LearningEvidenceLevelFamiliarity,
		SourceType: types.LearningEvidenceSourceMemoryWikiLink, SourceID: "foreign-link",
		Weight: 1, OccurredAt: time.Now(),
	})
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestLearningProfileRepositoryEvidenceUpsertIsIdempotent(t *testing.T) {
	db, repo := newLearningProfileRepositoryHarness(t)
	scope := interfaces.MemoryScope{TenantID: 1, SubjectID: "web_user:alice"}
	seedLearningProfilePage(t, db, 1, "page-a", "kb-a")

	first, err := repo.UpsertEvidence(t.Context(), scope, &types.LearningEvidence{
		WikiPageID: "page-a", EvidenceType: types.LearningEvidenceTypeMemoryLink,
		Level: types.LearningEvidenceLevelFamiliarity,
		SourceType: types.LearningEvidenceSourceMemoryWikiLink, SourceID: "link-a",
		Weight: 1, Metadata: types.JSONMap{"mapping_score": 0.1}, OccurredAt: time.Now(),
	})
	require.NoError(t, err)
	second, err := repo.UpsertEvidence(t.Context(), scope, &types.LearningEvidence{
		WikiPageID: "page-a", EvidenceType: types.LearningEvidenceTypeMemoryLink,
		Level: types.LearningEvidenceLevelFamiliarity,
		SourceType: types.LearningEvidenceSourceMemoryWikiLink, SourceID: "link-a",
		Weight: 1, Metadata: types.JSONMap{"mapping_score": 0.9}, OccurredAt: time.Now(),
	})
	require.NoError(t, err)
	require.Equal(t, first.ID, second.ID)
	require.Equal(t, 0.9, second.Metadata["mapping_score"])

	var count int64
	require.NoError(t, db.Model(&types.LearningEvidence{}).Count(&count).Error)
	require.Equal(t, int64(1), count)
}

func TestLearningProfileRepositoryKnowledgeStateIsUniquePerScope(t *testing.T) {
	db, repo := newLearningProfileRepositoryHarness(t)
	scope := interfaces.MemoryScope{TenantID: 1, SubjectID: "web_user:alice"}
	seedLearningProfilePage(t, db, 1, "page-a", "kb-a")

	first, err := repo.UpsertKnowledgeState(t.Context(), scope, &types.UserKnowledgeState{
		WikiPageID: "page-a", Status: types.UserKnowledgeStatusExposed,
		Confidence: 0.5, EvidenceCount: 1, LastEvidenceAt: time.Now(),
	})
	require.NoError(t, err)
	second, err := repo.UpsertKnowledgeState(t.Context(), scope, &types.UserKnowledgeState{
		WikiPageID: "page-a", Status: types.UserKnowledgeStatusFamiliar,
		Confidence: 1, EvidenceCount: 2, LastEvidenceAt: time.Now(),
	})
	require.NoError(t, err)
	require.Equal(t, first.ID, second.ID)
	require.Equal(t, types.UserKnowledgeStatusFamiliar, second.Status)
	require.Equal(t, 2, second.EvidenceCount)

	var count int64
	require.NoError(t, db.Model(&types.UserKnowledgeState{}).Count(&count).Error)
	require.Equal(t, int64(1), count)

	for _, foreignScope := range []interfaces.MemoryScope{
		{TenantID: 1, SubjectID: "web_user:bob"},
		{TenantID: 2, SubjectID: "web_user:alice"},
	} {
		foreign, err := repo.GetKnowledgeState(t.Context(), foreignScope, "page-a")
		require.NoError(t, err)
		require.Nil(t, foreign)
	}
}
