package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/application/service/memory"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type memoryWikiHandlerServiceFake struct {
	findCalls        int
	findScope        interfaces.MemoryScope
	findScopeErr     error
	findMemoryItemID string
	findKBID         string
	findTopK         int
	findResult       []*types.MemoryWikiCandidate
	findErr          error

	upsertCalls        int
	upsertScope        interfaces.MemoryScope
	upsertScopeErr     error
	upsertMemoryItemID string
	upsertWikiPageID   string
	upsertScore        float64
	upsertMethod       string
	upsertResult       *types.MemoryWikiLinkView
	upsertErr          error

	listCalls    int
	listScope    interfaces.MemoryScope
	listScopeErr error
	listResult   []*types.MemoryWikiLinkView
	listErr      error

	deleteCalls    int
	deleteScope    interfaces.MemoryScope
	deleteScopeErr error
	deleteID       string
	deleteErr      error
}

func (s *memoryWikiHandlerServiceFake) FindCandidates(
	ctx context.Context, memoryItemID, knowledgeBaseID string, topK int,
) ([]*types.MemoryWikiCandidate, error) {
	s.findCalls++
	s.findScope, s.findScopeErr = memory.ResolveScope(ctx)
	s.findMemoryItemID = memoryItemID
	s.findKBID = knowledgeBaseID
	s.findTopK = topK
	return s.findResult, s.findErr
}

func (s *memoryWikiHandlerServiceFake) UpsertLink(
	ctx context.Context, memoryItemID, wikiPageID string, score float64, method string,
) (*types.MemoryWikiLinkView, error) {
	s.upsertCalls++
	s.upsertScope, s.upsertScopeErr = memory.ResolveScope(ctx)
	s.upsertMemoryItemID = memoryItemID
	s.upsertWikiPageID = wikiPageID
	s.upsertScore = score
	s.upsertMethod = method
	return s.upsertResult, s.upsertErr
}

func (s *memoryWikiHandlerServiceFake) ListLinks(
	ctx context.Context,
) ([]*types.MemoryWikiLinkView, error) {
	s.listCalls++
	s.listScope, s.listScopeErr = memory.ResolveScope(ctx)
	return s.listResult, s.listErr
}

func (s *memoryWikiHandlerServiceFake) DeleteLink(ctx context.Context, id string) error {
	s.deleteCalls++
	s.deleteScope, s.deleteScopeErr = memory.ResolveScope(ctx)
	s.deleteID = id
	return s.deleteErr
}

func newMemoryWikiHandlerTestRouter(svc interfaces.MemoryWikiService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.ErrorHandler())
	router.Use(func(c *gin.Context) {
		ctx := context.WithValue(c.Request.Context(), types.TenantIDContextKey, uint64(7))
		ctx = types.WithPrincipal(ctx, types.Principal{Type: types.PrincipalWebUser, ID: "alice"})
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})

	handler := NewMemoryHandler(nil, svc, nil)
	router.POST("/api/v1/memory/items/:id/wiki-candidates", handler.FindWikiCandidates)
	router.POST("/api/v1/memory/items/:id/wiki-links", handler.UpsertWikiLink)
	router.GET("/api/v1/memory/wiki-links", handler.ListWikiLinks)
	router.DELETE("/api/v1/memory/wiki-links/:id", handler.DeleteWikiLink)
	return router
}

func performMemoryWikiHandlerRequest(
	t *testing.T, router http.Handler, method, target, body string,
) *httptest.ResponseRecorder {
	t.Helper()
	response := httptest.NewRecorder()
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	router.ServeHTTP(response, request)
	return response
}

func requireMemoryWikiAuthenticatedScope(
	t *testing.T, scope interfaces.MemoryScope, scopeErr error,
) {
	t.Helper()
	require.NoError(t, scopeErr)
	require.Equal(t, uint64(7), scope.TenantID)
	require.Equal(t, "web_user:alice", scope.SubjectID)
}

func TestMemoryWikiCandidatesSuccess(t *testing.T) {
	svc := &memoryWikiHandlerServiceFake{findResult: []*types.MemoryWikiCandidate{
		{
			WikiPageID:      "page-rag",
			Title:           "RAG",
			Slug:            "concept/rag",
			KnowledgeBaseID: "kb-1",
			Score:           0.91,
			Method:          types.MemoryWikiMethodChunkRef,
		},
	}}
	response := performMemoryWikiHandlerRequest(
		t,
		newMemoryWikiHandlerTestRouter(svc),
		http.MethodPost,
		"/api/v1/memory/items/memory-1/wiki-candidates",
		`{"knowledge_base_id":"kb-1","top_k":3}`,
	)

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, 1, svc.findCalls)
	requireMemoryWikiAuthenticatedScope(t, svc.findScope, svc.findScopeErr)
	require.Equal(t, "memory-1", svc.findMemoryItemID)
	require.Equal(t, "kb-1", svc.findKBID)
	require.Equal(t, 3, svc.findTopK)

	var envelope struct {
		Success bool                        `json:"success"`
		Data    []*types.MemoryWikiCandidate `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
	require.True(t, envelope.Success)
	require.Len(t, envelope.Data, 1)
	require.Equal(t, "page-rag", envelope.Data[0].WikiPageID)
	require.Equal(t, "RAG", envelope.Data[0].Title)
	require.Equal(t, "concept/rag", envelope.Data[0].Slug)
	require.Equal(t, "kb-1", envelope.Data[0].KnowledgeBaseID)
	require.InDelta(t, 0.91, envelope.Data[0].Score, 0.0001)
	require.Equal(t, types.MemoryWikiMethodChunkRef, envelope.Data[0].Method)
}

func TestMemoryWikiCandidatesEmpty(t *testing.T) {
	svc := &memoryWikiHandlerServiceFake{findResult: []*types.MemoryWikiCandidate{}}
	response := performMemoryWikiHandlerRequest(
		t,
		newMemoryWikiHandlerTestRouter(svc),
		http.MethodPost,
		"/api/v1/memory/items/memory-1/wiki-candidates",
		`{"knowledge_base_id":"kb-1"}`,
	)

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.JSONEq(t, `{"success":true,"data":[]}`, response.Body.String())
	require.Equal(t, 1, svc.findCalls)
}

func TestMemoryWikiCandidatesInvalidRequest(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "missing knowledge base", body: `{}`},
		{name: "invalid top k type", body: `{"knowledge_base_id":"kb-1","top_k":"five"}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			svc := &memoryWikiHandlerServiceFake{}
			response := performMemoryWikiHandlerRequest(
				t,
				newMemoryWikiHandlerTestRouter(svc),
				http.MethodPost,
				"/api/v1/memory/items/memory-1/wiki-candidates",
				test.body,
			)

			require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
			require.Equal(t, 0, svc.findCalls)
		})
	}
}

func TestMemoryWikiUpsertLinkSuccess(t *testing.T) {
	svc := &memoryWikiHandlerServiceFake{upsertResult: &types.MemoryWikiLinkView{
		Link: &types.MemoryWikiLink{
			ID:              "link-1",
			TenantID:        7,
			SubjectID:       "web_user:alice",
			MemoryItemID:    "memory-1",
			WikiPageID:      "page-rag",
			KnowledgeBaseID: "kb-1",
			Score:           0.91,
			Method:          types.MemoryWikiMethodChunkRef,
		},
		MemoryItem: &types.MemoryItem{ID: "memory-1", Content: "used RAG"},
		WikiPage: &types.MemoryWikiPageRef{
			WikiPageID: "page-rag", Title: "RAG", Slug: "concept/rag", KnowledgeBaseID: "kb-1",
		},
	}}
	response := performMemoryWikiHandlerRequest(
		t,
		newMemoryWikiHandlerTestRouter(svc),
		http.MethodPost,
		"/api/v1/memory/items/memory-1/wiki-links",
		`{"wiki_page_id":"page-rag","score":0.91,"method":"kb_retrieval_chunk_ref"}`,
	)

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, 1, svc.upsertCalls)
	requireMemoryWikiAuthenticatedScope(t, svc.upsertScope, svc.upsertScopeErr)
	require.Equal(t, "memory-1", svc.upsertMemoryItemID)
	require.Equal(t, "page-rag", svc.upsertWikiPageID)
	require.InDelta(t, 0.91, svc.upsertScore, 0.0001)
	require.Equal(t, types.MemoryWikiMethodChunkRef, svc.upsertMethod)
	require.Contains(t, response.Body.String(), `"id":"link-1"`)
	require.Contains(t, response.Body.String(), `"wiki_page_id":"page-rag"`)
}

func TestMemoryWikiUpsertLinkInvalidRequest(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "missing wiki page", body: `{}`},
		{name: "invalid score type", body: `{"wiki_page_id":"page-rag","score":"high"}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			svc := &memoryWikiHandlerServiceFake{}
			response := performMemoryWikiHandlerRequest(
				t,
				newMemoryWikiHandlerTestRouter(svc),
				http.MethodPost,
				"/api/v1/memory/items/memory-1/wiki-links",
				test.body,
			)

			require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
			require.Equal(t, 0, svc.upsertCalls)
		})
	}
}

func TestMemoryWikiListLinksSuccess(t *testing.T) {
	svc := &memoryWikiHandlerServiceFake{listResult: []*types.MemoryWikiLinkView{
		{
			Link:       &types.MemoryWikiLink{ID: "link-1", MemoryItemID: "memory-1", WikiPageID: "page-rag"},
			MemoryItem: &types.MemoryItem{ID: "memory-1"},
			WikiPage:   &types.MemoryWikiPageRef{WikiPageID: "page-rag", Title: "RAG"},
		},
	}}
	response := performMemoryWikiHandlerRequest(
		t,
		newMemoryWikiHandlerTestRouter(svc),
		http.MethodGet,
		"/api/v1/memory/wiki-links?tenant_id=99&user_id=mallory&subject_id=web_user%3Amallory",
		"",
	)

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, 1, svc.listCalls)
	requireMemoryWikiAuthenticatedScope(t, svc.listScope, svc.listScopeErr)
	require.Contains(t, response.Body.String(), `"id":"link-1"`)
}

func TestMemoryWikiDeleteLinkSuccess(t *testing.T) {
	svc := &memoryWikiHandlerServiceFake{}
	response := performMemoryWikiHandlerRequest(
		t,
		newMemoryWikiHandlerTestRouter(svc),
		http.MethodDelete,
		"/api/v1/memory/wiki-links/link-1",
		"",
	)

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, 1, svc.deleteCalls)
	requireMemoryWikiAuthenticatedScope(t, svc.deleteScope, svc.deleteScopeErr)
	require.Equal(t, "link-1", svc.deleteID)
	require.JSONEq(t, `{"success":true}`, response.Body.String())
}

func TestMemoryWikiDeleteLinkNotFoundOrForbidden(t *testing.T) {
	// Cross-scope link IDs are deliberately indistinguishable from missing
	// IDs, so both cases use the existing not-found sentinel and response.
	svc := &memoryWikiHandlerServiceFake{deleteErr: memory.ErrMemoryWikiLinkNotFound}
	response := performMemoryWikiHandlerRequest(
		t,
		newMemoryWikiHandlerTestRouter(svc),
		http.MethodDelete,
		"/api/v1/memory/wiki-links/foreign-link",
		"",
	)

	require.Equal(t, http.StatusNotFound, response.Code, response.Body.String())
	require.Equal(t, 1, svc.deleteCalls)
	requireMemoryWikiAuthenticatedScope(t, svc.deleteScope, svc.deleteScopeErr)
	require.Equal(t, "foreign-link", svc.deleteID)
	require.Contains(t, response.Body.String(), `"code":1003`)
}

func TestMemoryWikiIgnoresForgedScope(t *testing.T) {
	svc := &memoryWikiHandlerServiceFake{findResult: []*types.MemoryWikiCandidate{}}
	response := performMemoryWikiHandlerRequest(
		t,
		newMemoryWikiHandlerTestRouter(svc),
		http.MethodPost,
		"/api/v1/memory/items/memory-1/wiki-candidates?tenant_id=99&user_id=mallory&subject_id=web_user%3Amallory",
		`{"knowledge_base_id":"kb-1","top_k":5,"tenant_id":99,"user_id":"mallory","subject_id":"web_user:mallory"}`,
	)

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, 1, svc.findCalls)
	requireMemoryWikiAuthenticatedScope(t, svc.findScope, svc.findScopeErr)
	require.NotEqual(t, uint64(99), svc.findScope.TenantID)
	require.NotEqual(t, "web_user:mallory", svc.findScope.SubjectID)
}
