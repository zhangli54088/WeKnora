package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/service/memory"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type learningRecommendationHandlerFake struct { calls int; scope interfaces.MemoryScope; kb string; limit int; err error; recommendations []types.LearningRecommendation }
func (f *learningRecommendationHandlerFake) ListRecommendations(ctx context.Context,kb string,limit int)(*types.LearningRecommendationView,error){
	f.calls++;f.kb=kb;f.limit=limit
	var err error;f.scope,err=memory.ResolveScope(ctx);if err!=nil{return nil,err};if f.err!=nil{return nil,f.err}
	if f.recommendations==nil{f.recommendations=[]types.LearningRecommendation{}}
	return &types.LearningRecommendationView{KnowledgeBaseID:kb,GeneratedAt:time.Date(2026,9,3,0,0,0,0,time.UTC),WikiEnabled:true,Recommendations:f.recommendations},nil
}

func recommendationHandlerRouter(svc interfaces.LearningRecommendationService)*gin.Engine{
	gin.SetMode(gin.TestMode);router:=gin.New();router.Use(middleware.ErrorHandler())
	router.Use(func(c *gin.Context){ctx:=context.WithValue(c.Request.Context(),types.TenantIDContextKey,uint64(7));ctx=types.WithPrincipal(ctx,types.Principal{Type:types.PrincipalWebUser,ID:"alice"});c.Request=c.Request.WithContext(ctx);c.Next()})
	h:=NewMemoryHandler(nil,nil,nil,svc);router.GET("/api/v1/memory/learning-recommendations",h.ListLearningRecommendations);return router
}

func TestLearningRecommendationHandlerSuccessAndForgedScopeIgnored(t *testing.T){
	fake:=&learningRecommendationHandlerFake{recommendations:[]types.LearningRecommendation{{WikiPageID:"page-1",KnowledgeBaseID:"kb-a",Title:"Page",Status:"unknown",Rank:1,Score:0.8,Hop:1,ReasonCodes:[]string{"adjacent_to_familiar"},SupportingNodes:[]types.SupportingKnowledgeNode{{WikiPageID:"anchor",Status:"familiar"}}}}}
	r:=performMemoryWikiHandlerRequest(t,recommendationHandlerRouter(fake),http.MethodGet,"/api/v1/memory/learning-recommendations?knowledge_base_id=kb-a&limit=3&tenant_id=99&subject_id=web_user:bob&user_id=bob","")
	require.Equal(t,200,r.Code,r.Body.String());require.Equal(t,1,fake.calls);require.Equal(t,3,fake.limit);require.Equal(t,"kb-a",fake.kb)
	require.Equal(t,interfaces.MemoryScope{TenantID:7,SubjectID:"web_user:alice"},fake.scope)
	var response struct{Success bool `json:"success"`; Data types.LearningRecommendationView `json:"data"`};require.NoError(t,json.Unmarshal(r.Body.Bytes(),&response));require.True(t,response.Success);require.Len(t,response.Data.Recommendations,1)
	for _,field:=range []string{"wiki_page_id","status","score","rank","hop","reason_codes","supporting_nodes","score_components","knowledge_base_id","generated_at"}{require.Contains(t,r.Body.String(),`"`+field+`"`)}
	require.NotContains(t,r.Body.String(),"bob");require.NotContains(t,r.Body.String(),"subject_id")
}

func TestLearningRecommendationHandlerInvalidRequests(t *testing.T){
	for _,query:=range []string{"","?knowledge_base_id=%20","?knowledge_base_id=kb&limit=0","?knowledge_base_id=kb&limit=-1","?knowledge_base_id=kb&limit=21","?knowledge_base_id=kb&limit=abc","?knowledge_base_id=kb&limit="}{
		t.Run(query,func(t *testing.T){fake:=&learningRecommendationHandlerFake{};r:=performMemoryWikiHandlerRequest(t,recommendationHandlerRouter(fake),http.MethodGet,"/api/v1/memory/learning-recommendations"+query,"");require.Equal(t,400,r.Code);require.Zero(t,fake.calls)})
	}
}

func TestLearningRecommendationHandlerEmptyAndCrossTenant(t *testing.T){
	fake:=&learningRecommendationHandlerFake{}
	r:=performMemoryWikiHandlerRequest(t,recommendationHandlerRouter(fake),http.MethodGet,"/api/v1/memory/learning-recommendations?knowledge_base_id=kb","")
	require.Equal(t,200,r.Code);require.Equal(t,5,fake.limit);require.Contains(t,r.Body.String(),`"recommendations":[]`)
	fake.err=memory.ErrMemoryWikiKnowledgeBaseNotFound
	r=performMemoryWikiHandlerRequest(t,recommendationHandlerRouter(fake),http.MethodGet,"/api/v1/memory/learning-recommendations?knowledge_base_id=foreign","")
	require.Equal(t,404,r.Code);require.NotContains(t,r.Body.String(),"supporting_nodes")
}
