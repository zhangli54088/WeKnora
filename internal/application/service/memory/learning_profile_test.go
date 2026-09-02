package memory

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type learningProfileWikiRepoStub struct {
	interfaces.MemoryWikiRepository
	pages map[string]*types.WikiPage
}

func (s *learningProfileWikiRepoStub) GetWikiPage(
	_ context.Context, tenantID uint64, knowledgeBaseID, pageID string,
) (*types.WikiPage, error) {
	page := s.pages[pageID]
	if page == nil || page.TenantID != tenantID ||
		knowledgeBaseID != "" && page.KnowledgeBaseID != knowledgeBaseID {
		return nil, nil
	}
	return page, nil
}

func newLearningProfileServiceHarness(
	t *testing.T,
) (*learningProfileService, interfaces.LearningProfileRepository, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())),
		&gorm.Config{Logger: logger.Discard},
	)
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&types.WikiPage{}, &types.LearningEvidence{}, &types.UserKnowledgeState{},
	))
	page := &types.WikiPage{
		ID: "page-a", TenantID: 1, KnowledgeBaseID: "kb-a",
		Slug: "concept/page-a", Title: "Page A",
		PageType: types.WikiPageTypeConcept, Status: types.WikiPageStatusPublished,
	}
	otherTenantPage := &types.WikiPage{
		ID: "page-b", TenantID: 2, KnowledgeBaseID: "kb-b",
		Slug: "concept/page-b", Title: "Page B",
		PageType: types.WikiPageTypeConcept, Status: types.WikiPageStatusPublished,
	}
	require.NoError(t, db.Create([]*types.WikiPage{page, otherTenantPage}).Error)
	repo := repository.NewLearningProfileRepository(db)
	return &learningProfileService{
		repo: repo,
		wikiRepo: &learningProfileWikiRepoStub{pages: map[string]*types.WikiPage{
			page.ID: page, otherTenantPage.ID: otherTenantPage,
		}},
	}, repo, db
}

func learningProfileTestLink(id string, occurredAt time.Time) *types.MemoryWikiLink {
	return &types.MemoryWikiLink{
		ID: id, TenantID: 1, SubjectID: "web_user:alice",
		MemoryItemID: "memory-" + id, WikiPageID: "page-a", KnowledgeBaseID: "kb-a",
		Score: 0.0155, Method: types.MemoryWikiMethodChunkRef,
		CreatedAt: occurredAt, UpdatedAt: occurredAt,
	}
}

func TestLearningProfileMemoryWikiLinkCreatesFamiliarStateIdempotently(t *testing.T) {
	svc, repo, _ := newLearningProfileServiceHarness(t)
	ctx := memoryWikiTestContext(t, 1, "alice")
	link := learningProfileTestLink("link-1", time.Now())

	require.NoError(t, svc.SyncMemoryWikiLink(ctx, link))
	require.NoError(t, svc.SyncMemoryWikiLink(ctx, link))
	evidence, err := svc.ListEvidence(ctx, "page-a")
	require.NoError(t, err)
	require.Len(t, evidence, 1)
	require.Equal(t, types.LearningEvidenceTypeMemoryLink, evidence[0].EvidenceType)
	require.Equal(t, types.LearningEvidenceLevelFamiliarity, evidence[0].Level)
	require.Equal(t, memoryWikiEvidenceWeight, evidence[0].Weight)
	require.Equal(t, link.Score, evidence[0].Metadata["mapping_score"])

	state, err := repo.GetKnowledgeState(
		ctx, interfaces.MemoryScope{TenantID: 1, SubjectID: "web_user:alice"}, "page-a",
	)
	require.NoError(t, err)
	require.NotNil(t, state)
	require.Equal(t, types.UserKnowledgeStatusFamiliar, state.Status)
	require.Equal(t, 1, state.EvidenceCount)
	require.Equal(t, 1.0, state.Confidence)
}

func TestLearningProfileEvidenceDeletionRecomputesAndRemovesState(t *testing.T) {
	svc, repo, _ := newLearningProfileServiceHarness(t)
	ctx := memoryWikiTestContext(t, 1, "alice")
	first := learningProfileTestLink("link-1", time.Now().Add(-time.Minute))
	second := learningProfileTestLink("link-2", time.Now())
	require.NoError(t, svc.SyncMemoryWikiLink(ctx, first))
	require.NoError(t, svc.SyncMemoryWikiLink(ctx, second))

	state, err := svc.RecomputeKnowledgeState(ctx, "page-a")
	require.NoError(t, err)
	require.Equal(t, types.UserKnowledgeStatusFamiliar, state.Status)
	require.Equal(t, 2, state.EvidenceCount)
	require.WithinDuration(t, second.UpdatedAt, state.LastEvidenceAt, time.Millisecond)

	require.NoError(t, svc.RemoveMemoryWikiLinkEvidence(ctx, first))
	state, err = svc.RecomputeKnowledgeState(ctx, "page-a")
	require.NoError(t, err)
	require.Equal(t, types.UserKnowledgeStatusFamiliar, state.Status)
	require.Equal(t, 1, state.EvidenceCount)

	require.NoError(t, svc.RemoveMemoryWikiLinkEvidence(ctx, second))
	state, err = repo.GetKnowledgeState(
		ctx, interfaces.MemoryScope{TenantID: 1, SubjectID: "web_user:alice"}, "page-a",
	)
	require.NoError(t, err)
	require.Nil(t, state)
}

func TestLearningProfileAggregationPriority(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name       string
		evidence   []*types.LearningEvidence
		wantStatus string
	}{
		{
			name: "exposure only",
			evidence: []*types.LearningEvidence{
				{WikiPageID: "page-a", Level: types.LearningEvidenceLevelExposure, Weight: 0.4, OccurredAt: now},
			},
			wantStatus: types.UserKnowledgeStatusExposed,
		},
		{
			name: "familiarity beats exposure",
			evidence: []*types.LearningEvidence{
				{WikiPageID: "page-a", Level: types.LearningEvidenceLevelExposure, Weight: 1, OccurredAt: now},
				{WikiPageID: "page-a", Level: types.LearningEvidenceLevelFamiliarity, Weight: 0.6, OccurredAt: now},
			},
			wantStatus: types.UserKnowledgeStatusFamiliar,
		},
		{
			name: "mastery beats all lower levels",
			evidence: []*types.LearningEvidence{
				{WikiPageID: "page-a", Level: types.LearningEvidenceLevelExposure, Weight: 1, OccurredAt: now},
				{WikiPageID: "page-a", Level: types.LearningEvidenceLevelFamiliarity, Weight: 1, OccurredAt: now},
				{WikiPageID: "page-a", Level: types.LearningEvidenceLevelMastery, Weight: 0.7, OccurredAt: now},
			},
			wantStatus: types.UserKnowledgeStatusMastered,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := aggregateKnowledgeEvidence("page-a", test.evidence)
			require.NotNil(t, state)
			require.Equal(t, test.wantStatus, state.Status)
			require.Equal(t, len(test.evidence), state.EvidenceCount)
		})
	}
}

func TestLearningProfileKnowledgeStateViewsIncludeWikiIdentity(t *testing.T) {
	svc, _, _ := newLearningProfileServiceHarness(t)
	ctx := memoryWikiTestContext(t, 1, "alice")
	require.NoError(t, svc.SyncMemoryWikiLink(
		ctx, learningProfileTestLink("link-1", time.Now()),
	))

	views, err := svc.ListKnowledgeStates(ctx, "kb-a")
	require.NoError(t, err)
	require.Len(t, views, 1)
	require.Equal(t, "page-a", views[0].WikiPageID)
	require.Equal(t, "Page A", views[0].Title)
	require.Equal(t, "concept/page-a", views[0].Slug)
	require.Equal(t, "kb-a", views[0].KnowledgeBaseID)

	filtered, err := svc.ListKnowledgeStates(ctx, "kb-other")
	require.NoError(t, err)
	require.Empty(t, filtered)
}

func TestLearningProfileChatInteractionIsExposureAndIdempotent(t *testing.T) {
	svc, _, _ := newLearningProfileServiceHarness(t)
	ctx := memoryWikiTestContext(t, 1, "alice")
	candidate := &types.MemoryWikiCandidate{
		WikiPageID: "page-a", KnowledgeBaseID: "kb-a",
		Method: types.MemoryWikiMethodChunkRef, Score: 0.42,
	}

	require.NoError(t, svc.RecordChatInteractions(ctx, "session-1", "message-1", "kb-a", []*types.MemoryWikiCandidate{candidate}))
	require.NoError(t, svc.RecordChatInteractions(ctx, "session-1", "message-1", "kb-a", []*types.MemoryWikiCandidate{candidate}))
	evidence, err := svc.ListEvidence(ctx, "page-a")
	require.NoError(t, err)
	require.Len(t, evidence, 1)
	require.Equal(t, types.LearningEvidenceTypeChatInteraction, evidence[0].EvidenceType)
	require.Equal(t, types.LearningEvidenceLevelExposure, evidence[0].Level)
	require.Equal(t, types.LearningEvidenceSourceChatMessage, evidence[0].SourceType)
	require.Equal(t, "message-1", evidence[0].SourceID)
	require.Equal(t, chatInteractionEvidenceWeight, evidence[0].Weight)
	require.Equal(t, "session-1", evidence[0].Metadata["session_id"])
	require.Equal(t, types.MemoryWikiMethodChunkRef, evidence[0].Metadata["mapping_method"])

	states, err := svc.ListKnowledgeStates(ctx, "kb-a")
	require.NoError(t, err)
	require.Len(t, states, 1)
	require.Equal(t, types.UserKnowledgeStatusExposed, states[0].Status)
	require.Equal(t, 1, states[0].EvidenceCount)
}

func TestLearningProfileDifferentChatMessagesIncrementEvidenceCount(t *testing.T) {
	svc, _, _ := newLearningProfileServiceHarness(t)
	ctx := memoryWikiTestContext(t, 1, "alice")
	candidates := []*types.MemoryWikiCandidate{{
		WikiPageID: "page-a", KnowledgeBaseID: "kb-a", Method: types.MemoryWikiMethodChunkRef,
	}}
	require.NoError(t, svc.RecordChatInteractions(ctx, "session-1", "message-1", "kb-a", candidates))
	require.NoError(t, svc.RecordChatInteractions(ctx, "session-1", "message-2", "kb-a", candidates))
	states, err := svc.ListKnowledgeStates(ctx, "kb-a")
	require.NoError(t, err)
	require.Len(t, states, 1)
	require.Equal(t, 2, states[0].EvidenceCount)
}

func TestLearningProfileChatExposureDoesNotDowngradeFamiliarOrMastered(t *testing.T) {
	for _, test := range []struct {
		name, level, want string
	}{
		{name: "familiar", level: types.LearningEvidenceLevelFamiliarity, want: types.UserKnowledgeStatusFamiliar},
		{name: "mastered", level: types.LearningEvidenceLevelMastery, want: types.UserKnowledgeStatusMastered},
	} {
		t.Run(test.name, func(t *testing.T) {
			svc, repo, _ := newLearningProfileServiceHarness(t)
			ctx := memoryWikiTestContext(t, 1, "alice")
			scope := interfaces.MemoryScope{TenantID: 1, SubjectID: "web_user:alice"}
			_, err := repo.UpsertEvidence(ctx, scope, &types.LearningEvidence{
				WikiPageID: "page-a", EvidenceType: "test", Level: test.level,
				SourceType: "test", SourceID: "strong-1", Weight: 1, OccurredAt: time.Now(),
			})
			require.NoError(t, err)
			_, err = svc.RecomputeKnowledgeState(ctx, "page-a")
			require.NoError(t, err)
			require.NoError(t, svc.RecordChatInteractions(ctx, "session-1", "message-1", "kb-a", []*types.MemoryWikiCandidate{{
				WikiPageID: "page-a", KnowledgeBaseID: "kb-a", Method: types.MemoryWikiMethodChunkRef,
			}}))
			state, err := repo.GetKnowledgeState(ctx, scope, "page-a")
			require.NoError(t, err)
			require.Equal(t, test.want, state.Status)
			require.Equal(t, 2, state.EvidenceCount)
		})
	}
}

func TestLearningProfileChatEvidenceIsTenantAndSubjectScoped(t *testing.T) {
	svc, _, db := newLearningProfileServiceHarness(t)
	candidate := []*types.MemoryWikiCandidate{{
		WikiPageID: "page-a", KnowledgeBaseID: "kb-a", Method: types.MemoryWikiMethodChunkRef,
	}}
	require.NoError(t, svc.RecordChatInteractions(
		memoryWikiTestContext(t, 1, "alice"), "session-a", "message-a", "kb-a", candidate,
	))
	require.NoError(t, svc.RecordChatInteractions(
		memoryWikiTestContext(t, 1, "bob"), "session-b", "message-b", "kb-a", candidate,
	))
	require.NoError(t, svc.RecordChatInteractions(
		memoryWikiTestContext(t, 2, "alice"), "session-c", "message-c", "kb-b",
		[]*types.MemoryWikiCandidate{{
			WikiPageID: "page-b", KnowledgeBaseID: "kb-b", Method: types.MemoryWikiMethodChunkRef,
		}},
	))
	var count int64
	require.NoError(t, db.Model(&types.LearningEvidence{}).Count(&count).Error)
	require.EqualValues(t, 3, count)

	alice, err := svc.ListEvidence(memoryWikiTestContext(t, 1, "alice"), "page-a")
	require.NoError(t, err)
	require.Len(t, alice, 1)
	require.Equal(t, "message-a", alice[0].SourceID)
	tenantTwo, err := svc.ListEvidence(memoryWikiTestContext(t, 2, "alice"), "page-b")
	require.NoError(t, err)
	require.Len(t, tenantTwo, 1)
	require.Equal(t, "message-c", tenantTwo[0].SourceID)
}
