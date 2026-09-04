package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/application/service/memory"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type learningProfileDataHandlerFake struct {
	interfaces.LearningProfileService
	counts map[interfaces.MemoryScope]int64
	scopes []interfaces.MemoryScope
	err error
}

var _ interfaces.LearningProfileService = (*learningProfileDataHandlerFake)(nil)

type learningProfileLegacyMemoryFake struct { interfaces.MemoryService }

func (f *learningProfileLegacyMemoryFake) ListItems(context.Context, string, int, int) ([]*types.MemoryItem, int64, error) {
	return []*types.MemoryItem{{ID: "legacy-item"}}, 1, nil
}

func TestLearningProfileDataHandlerLegacyExportUnchanged(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	h := NewMemoryHandler(&learningProfileLegacyMemoryFake{}, nil, nil, nil)
	router.GET("/api/v1/memory/export", h.Export)
	response := performMemoryWikiHandlerRequest(t, router, http.MethodGet, "/api/v1/memory/export", "")
	require.Equal(t, http.StatusOK, response.Code)
	require.Contains(t, response.Body.String(), `"data":[{"id":"legacy-item"`)
	require.Contains(t, response.Body.String(), `"total":1`)
	require.Contains(t, response.Body.String(), `"truncated":false`)
	require.NotContains(t, response.Body.String(), `"learning_profile"`)
}

func (f *learningProfileDataHandlerFake) ExportProfile(ctx context.Context) (*types.LearningProfileExport, error) {
	scope, err := memory.ResolveScope(ctx)
	if err != nil { return nil, err }
	f.scopes = append(f.scopes, scope)
	if f.err != nil { return nil, f.err }
	links := []*types.MemoryWikiLink{}
	if f.counts[scope] > 0 { links = append(links, &types.MemoryWikiLink{ID: scope.SubjectID, TenantID: scope.TenantID, SubjectID: scope.SubjectID}) }
	return &types.LearningProfileExport{
		Version: 1, Scope: types.LearningProfileExportScope{TenantID: scope.TenantID, SubjectID: scope.SubjectID},
		Memory: types.LearningProfileMemoryExport{Items: []*types.MemoryItem{}, Topics: []*types.MemoryTopicStat{}, Documents: []*types.MemoryDocAffinity{}},
		LearningProfile: types.LearningProfileDataExport{MemoryWikiLinks: links, LearningEvidences: []*types.LearningEvidenceExport{}, KnowledgeStates: []*types.KnowledgeStateExport{}},
	}, nil
}

func (f *learningProfileDataHandlerFake) ClearProfile(ctx context.Context) (*types.LearningProfileDeleteResult, error) {
	scope, err := memory.ResolveScope(ctx)
	if err != nil { return nil, err }
	f.scopes = append(f.scopes, scope)
	if f.err != nil { return nil, f.err }
	count := f.counts[scope]
	delete(f.counts, scope)
	return &types.LearningProfileDeleteResult{MemoryWikiLinksDeleted: count, LearningEvidencesDeleted: count, KnowledgeStatesDeleted: count}, nil
}

func learningProfileDataRouter(svc interfaces.LearningProfileService, tenant uint64, user string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.ErrorHandler())
	router.Use(func(c *gin.Context) {
		ctx := c.Request.Context()
		if tenant != 0 { ctx = context.WithValue(ctx, types.TenantIDContextKey, tenant) }
		if user != "" { ctx = types.WithPrincipal(ctx, types.Principal{Type: types.PrincipalWebUser, ID: user}) }
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	h := NewMemoryHandler(nil, nil, svc, nil)
	router.GET("/api/v1/memory/export", h.Export)
	router.DELETE("/api/v1/memory/learning-profile", h.ClearLearningProfile)
	return router
}

func TestLearningProfileDataHandlerExportSuccessAndEmpty(t *testing.T) {
	for _, count := range []int64{0, 1} {
		scope := interfaces.MemoryScope{TenantID: 7, SubjectID: "web_user:alice"}
		f := &learningProfileDataHandlerFake{counts: map[interfaces.MemoryScope]int64{scope: count}}
		response := performMemoryWikiHandlerRequest(t, learningProfileDataRouter(f, 7, "alice"), http.MethodGet,
			"/api/v1/memory/export?include_learning_profile=true&tenant_id=99&user_id=bob&subject_id=web_user:bob", "")
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
		require.Equal(t, []interfaces.MemoryScope{scope}, f.scopes)
		var result struct { Success bool `json:"success"`; Data types.LearningProfileExport `json:"data"` }
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
		require.True(t, result.Success); require.Equal(t, 1, result.Data.Version)
		require.Equal(t, int(count), len(result.Data.LearningProfile.MemoryWikiLinks))
		require.Contains(t, response.Body.String(), `"learning_evidences":[]`)
		require.Contains(t, response.Body.String(), `"knowledge_states":[]`)
		require.NotContains(t, response.Body.String(), "bob")
	}
}

func TestLearningProfileDataHandlerDeleteRepeatedAndScopeIsolation(t *testing.T) {
	alice := interfaces.MemoryScope{TenantID: 7, SubjectID: "web_user:alice"}
	bob := interfaces.MemoryScope{TenantID: 7, SubjectID: "web_user:bob"}
	otherTenant := interfaces.MemoryScope{TenantID: 8, SubjectID: "web_user:alice"}
	f := &learningProfileDataHandlerFake{counts: map[interfaces.MemoryScope]int64{alice: 1, bob: 2, otherTenant: 3}}
	for _, expected := range []int64{1, 0} {
		response := performMemoryWikiHandlerRequest(t, learningProfileDataRouter(f, 7, "alice"), http.MethodDelete,
			"/api/v1/memory/learning-profile?tenant_id=8&user_id=bob&subject_id=web_user:bob",
			`{"tenant_id":8,"user_id":"bob","subject_id":"web_user:bob"}`)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		var result struct { Success bool `json:"success"`; Data types.LearningProfileDeleteResult `json:"data"` }
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
		require.True(t, result.Success)
		require.Equal(t, types.LearningProfileDeleteResult{MemoryWikiLinksDeleted: expected, LearningEvidencesDeleted: expected, KnowledgeStatesDeleted: expected}, result.Data)
	}
	require.Equal(t, []interfaces.MemoryScope{alice, alice}, f.scopes)
	require.Equal(t, int64(2), f.counts[bob]); require.Equal(t, int64(3), f.counts[otherTenant])
	response := performMemoryWikiHandlerRequest(t, learningProfileDataRouter(f, 7, "bob"), http.MethodGet, "/api/v1/memory/export?include_learning_profile=true&subject_id=web_user:alice", "")
	require.Equal(t, http.StatusOK, response.Code)
	require.Contains(t, response.Body.String(), "web_user:bob"); require.NotContains(t, response.Body.String(), "alice")
}

func TestLearningProfileDataHandlerUnauthenticatedAndFailures(t *testing.T) {
	for method, path := range map[string]string{http.MethodGet: "/api/v1/memory/export?include_learning_profile=true", http.MethodDelete: "/api/v1/memory/learning-profile"} {
		for _, identity := range []struct{ tenant uint64; user string }{{0, "alice"}, {7, ""}} {
			f := &learningProfileDataHandlerFake{}
			response := performMemoryWikiHandlerRequest(t, learningProfileDataRouter(f, identity.tenant, identity.user), method, path, "")
			require.Equal(t, http.StatusUnauthorized, response.Code, response.Body.String())
			require.Empty(t, f.scopes)
		}
		f := &learningProfileDataHandlerFake{err: errors.New("storage unavailable")}
		response := performMemoryWikiHandlerRequest(t, learningProfileDataRouter(f, 7, "alice"), method, path, "")
		require.Equal(t, http.StatusInternalServerError, response.Code, response.Body.String())
		require.NotContains(t, response.Body.String(), `"success":true`)
	}
}
