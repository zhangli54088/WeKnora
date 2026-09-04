package handler

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/application/service/memory"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type learningProfileHandlerServiceFake struct {
	evidenceCalls    int
	evidenceScope    interfaces.MemoryScope
	evidenceScopeErr error
	wikiPageID       string
	evidenceResult   []*types.LearningEvidence
	evidenceErr      error

	stateCalls      int
	stateScope      interfaces.MemoryScope
	stateScopeErr   error
	knowledgeBaseID string
	stateResult     []*types.UserKnowledgeStateView
	stateErr        error
}

func (s *learningProfileHandlerServiceFake) ExportProfile(context.Context) (*types.LearningProfileExport, error) {
	return nil, nil
}

func (s *learningProfileHandlerServiceFake) ClearProfile(context.Context) (*types.LearningProfileDeleteResult, error) {
	return nil, nil
}

func (s *learningProfileHandlerServiceFake) SyncMemoryWikiLink(
	context.Context, *types.MemoryWikiLink,
) error {
	return nil
}

func (s *learningProfileHandlerServiceFake) RecordChatInteractions(
	context.Context, string, string, string, []*types.MemoryWikiCandidate,
) error {
	return nil
}

func (s *learningProfileHandlerServiceFake) RemoveMemoryWikiLinkEvidence(
	context.Context, *types.MemoryWikiLink,
) error {
	return nil
}

func (s *learningProfileHandlerServiceFake) RecomputeKnowledgeState(
	context.Context, string,
) (*types.UserKnowledgeState, error) {
	return nil, nil
}

func (s *learningProfileHandlerServiceFake) ListEvidence(
	ctx context.Context, wikiPageID string,
) ([]*types.LearningEvidence, error) {
	s.evidenceCalls++
	s.evidenceScope, s.evidenceScopeErr = memory.ResolveScope(ctx)
	s.wikiPageID = wikiPageID
	return s.evidenceResult, s.evidenceErr
}

func (s *learningProfileHandlerServiceFake) ListKnowledgeStates(
	ctx context.Context, knowledgeBaseID string,
) ([]*types.UserKnowledgeStateView, error) {
	s.stateCalls++
	s.stateScope, s.stateScopeErr = memory.ResolveScope(ctx)
	s.knowledgeBaseID = knowledgeBaseID
	return s.stateResult, s.stateErr
}

func newLearningProfileHandlerTestRouter(
	svc interfaces.LearningProfileService,
) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.ErrorHandler())
	router.Use(func(c *gin.Context) {
		ctx := context.WithValue(c.Request.Context(), types.TenantIDContextKey, uint64(7))
		ctx = types.WithPrincipal(ctx, types.Principal{Type: types.PrincipalWebUser, ID: "alice"})
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	handler := NewMemoryHandler(nil, nil, svc, nil)
	router.GET("/api/v1/memory/learning-evidence", handler.ListLearningEvidence)
	router.GET("/api/v1/memory/knowledge-states", handler.ListKnowledgeStates)
	return router
}

func TestLearningProfileListEvidenceSuccess(t *testing.T) {
	now := time.Now()
	svc := &learningProfileHandlerServiceFake{evidenceResult: []*types.LearningEvidence{
		{
			ID: "evidence-1", WikiPageID: "page-a",
			EvidenceType: types.LearningEvidenceTypeMemoryLink,
			Level: types.LearningEvidenceLevelFamiliarity,
			SourceType: types.LearningEvidenceSourceMemoryWikiLink,
			SourceID: "link-1", Weight: 1, OccurredAt: now,
		},
	}}
	response := performMemoryWikiHandlerRequest(
		t,
		newLearningProfileHandlerTestRouter(svc),
		http.MethodGet,
		"/api/v1/memory/learning-evidence?wiki_page_id=page-a",
		"",
	)

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, 1, svc.evidenceCalls)
	requireMemoryWikiAuthenticatedScope(t, svc.evidenceScope, svc.evidenceScopeErr)
	require.Equal(t, "page-a", svc.wikiPageID)
	require.Contains(t, response.Body.String(), `"id":"evidence-1"`)
	require.Contains(t, response.Body.String(), `"level":"familiarity"`)
}

func TestLearningProfileListKnowledgeStatesSuccess(t *testing.T) {
	svc := &learningProfileHandlerServiceFake{stateResult: []*types.UserKnowledgeStateView{
		{
			ID: "state-1", WikiPageID: "page-a", Title: "Page A",
			Slug: "concept/page-a", KnowledgeBaseID: "kb-a",
			Status: types.UserKnowledgeStatusFamiliar, Confidence: 1, EvidenceCount: 2,
		},
	}}
	response := performMemoryWikiHandlerRequest(
		t,
		newLearningProfileHandlerTestRouter(svc),
		http.MethodGet,
		"/api/v1/memory/knowledge-states?knowledge_base_id=kb-a",
		"",
	)

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, 1, svc.stateCalls)
	requireMemoryWikiAuthenticatedScope(t, svc.stateScope, svc.stateScopeErr)
	require.Equal(t, "kb-a", svc.knowledgeBaseID)
	require.Contains(t, response.Body.String(), `"wiki_page_id":"page-a"`)
	require.Contains(t, response.Body.String(), `"status":"familiar"`)
}

func TestLearningProfileRejectsInvalidQuery(t *testing.T) {
	tests := []struct {
		name   string
		target string
	}{
		{
			name:   "empty wiki page id",
			target: "/api/v1/memory/learning-evidence?wiki_page_id=%20%20",
		},
		{
			name:   "empty knowledge base id",
			target: "/api/v1/memory/knowledge-states?knowledge_base_id=%20%20",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			svc := &learningProfileHandlerServiceFake{}
			response := performMemoryWikiHandlerRequest(
				t,
				newLearningProfileHandlerTestRouter(svc),
				http.MethodGet,
				test.target,
				"",
			)
			require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
			require.Equal(t, 0, svc.evidenceCalls+svc.stateCalls)
		})
	}
}

func TestLearningProfileIgnoresForgedScopeAndDoesNotLeak(t *testing.T) {
	svc := &learningProfileHandlerServiceFake{evidenceResult: []*types.LearningEvidence{}}
	response := performMemoryWikiHandlerRequest(
		t,
		newLearningProfileHandlerTestRouter(svc),
		http.MethodGet,
		"/api/v1/memory/learning-evidence?wiki_page_id=foreign-page&tenant_id=99&subject_id=web_user%3Amallory&user_id=mallory",
		"",
	)

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.JSONEq(t, `{"success":true,"data":[]}`, response.Body.String())
	require.Equal(t, 1, svc.evidenceCalls)
	requireMemoryWikiAuthenticatedScope(t, svc.evidenceScope, svc.evidenceScopeErr)
	require.Equal(t, "foreign-page", svc.wikiPageID)
}
