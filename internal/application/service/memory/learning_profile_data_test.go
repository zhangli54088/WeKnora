package memory

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

func TestLearningProfileDataExportSafeMetadata(t *testing.T) {
	svc, _, db := newLearningProfileServiceHarness(t)
	require.NoError(t, db.AutoMigrate(&types.MemoryItem{}, &types.MemoryTopicStat{}, &types.MemoryDocAffinity{}, &types.MemoryWikiLink{}))
	ctx := memoryWikiTestContext(t, 1, "alice")
	link := learningProfileTestLink("safe", time.Now())
	require.NoError(t, db.Create(link).Error)
	require.NoError(t, svc.SyncMemoryWikiLink(ctx, link))
	require.NoError(t, db.Model(&types.LearningEvidence{}).Where("source_id = ?", link.ID).Update("metadata", types.JSONMap{
		"memory_item_id": "memory-safe", "message_id": "message-safe", "session_id": "session-safe", "knowledge_base_id": "kb-a",
		"mapping_score": 0.8, "mapping_method": types.MemoryWikiMethodChunkRef, "rank": 1,
		"prompt": "SECRET_PROMPT", "Authorization": "SECRET_AUTH", "Cookie": "SECRET_COOKIE", "jwt": "SECRET_JWT", "api_key": "SECRET_KEY",
		"extra": map[string]interface{}{"secret": "SECRET_NESTED"},
	}).Error)
	result, err := svc.ExportProfile(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, result.Version)
	require.Equal(t, types.LearningProfileExportScope{TenantID: 1, SubjectID: "web_user:alice"}, result.Scope)
	require.False(t, result.ExportedAt.IsZero())
	require.Len(t, result.LearningProfile.MemoryWikiLinks, 1)
	require.Len(t, result.LearningProfile.LearningEvidences, 1)
	evidence := result.LearningProfile.LearningEvidences[0]
	require.Len(t, evidence.Metadata, 7)
	require.Equal(t, "memory-safe", evidence.Metadata["memory_item_id"])
	require.Equal(t, 0.8, evidence.Metadata["mapping_score"])
	require.Equal(t, "kb-a", result.LearningProfile.KnowledgeStates[0].KnowledgeBaseID)
	require.Equal(t, "Page A", result.LearningProfile.KnowledgeStates[0].Title)
	var storedState types.UserKnowledgeState
	require.NoError(t, db.Where("wiki_page_id = ?", "page-a").First(&storedState).Error)
	require.False(t, result.LearningProfile.KnowledgeStates[0].CreatedAt.IsZero())
	require.True(t, storedState.CreatedAt.Equal(result.LearningProfile.KnowledgeStates[0].CreatedAt))
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "SECRET_")
	require.NotContains(t, string(encoded), "Authorization")
	require.Contains(t, string(encoded), `"items":[]`)
	require.Contains(t, string(encoded), `"topics":[]`)
	require.Contains(t, string(encoded), `"documents":[]`)
	var document map[string]interface{}
	require.NoError(t, json.Unmarshal(encoded, &document))
	states := document["learning_profile"].(map[string]interface{})["knowledge_states"].([]interface{})
	require.Contains(t, states[0].(map[string]interface{}), "created_at")
	require.Contains(t, states[0].(map[string]interface{}), "updated_at")
	require.Empty(t, exportEvidenceMetadata(types.JSONMap{"message_id": map[string]interface{}{"prompt": "SECRET"}, "mapping_method": "SECRET", "mapping_score": "SECRET", "rank": []int{1}}))
}

func TestLearningProfileDataClearRecommendationAndRebuild(t *testing.T) {
	svc, repo, db := newLearningProfileServiceHarness(t)
	require.NoError(t, db.AutoMigrate(&types.MemoryWikiLink{}))
	ctx := memoryWikiTestContext(t, 1, "alice")
	link := learningProfileTestLink("link-a", time.Now())
	require.NoError(t, db.Create(link).Error)
	require.NoError(t, svc.SyncMemoryWikiLink(ctx, link))
	require.NoError(t, db.Create(recommendationPage("candidate")).Error)
	require.NoError(t, db.Model(&types.WikiPage{}).Where("id = ?", "page-a").Update("out_links", types.StringArray{"candidate"}).Error)
	recommender := NewLearningRecommendationService(&recommendationKBRepoFake{kb: &types.KnowledgeBase{ID: "kb-a", TenantID: 1, IndexingStrategy: types.IndexingStrategy{WikiEnabled: true}}}, repository.NewWikiPageRepository(db), repo)
	before, err := recommender.ListRecommendations(ctx, "kb-a", 5)
	require.NoError(t, err); require.Len(t, before.Recommendations, 1)
	_, err = svc.ClearProfile(ctx); require.NoError(t, err)
	after, err := recommender.ListRecommendations(ctx, "kb-a", 5)
	require.NoError(t, err); require.NotNil(t, after.Recommendations); require.Empty(t, after.Recommendations)
	candidate := &types.MemoryWikiCandidate{WikiPageID: "page-a", KnowledgeBaseID: "kb-a", Method: types.MemoryWikiMethodChunkRef}
	require.NoError(t, svc.RecordChatInteractions(ctx, "session", "message", "kb-a", []*types.MemoryWikiCandidate{candidate}))
	require.NoError(t, svc.RecordChatInteractions(ctx, "session", "message", "kb-a", []*types.MemoryWikiCandidate{candidate}))
	state, err := repo.GetKnowledgeState(ctx, interfaces.MemoryScope{TenantID: 1, SubjectID: "web_user:alice"}, "page-a")
	require.NoError(t, err); require.NotNil(t, state)
	require.Equal(t, types.UserKnowledgeStatusExposed, state.Status); require.Equal(t, 1, state.EvidenceCount)
}

func TestLearningProfileDataServiceRequiresAuthenticatedScope(t *testing.T) {
	svc := &learningProfileService{} // A rejected request must not access a repository.
	_, err := svc.ExportProfile(context.Background()); require.ErrorIs(t, err, ErrNoMemoryScope)
	_, err = svc.ClearProfile(context.Background()); require.ErrorIs(t, err, ErrNoMemoryScope)
}
