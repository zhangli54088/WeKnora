package repository

import (
	"testing"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

func TestLearningRecommendationRepositoryBatchScope(t *testing.T) {
	db,repo:=newLearningProfileRepositoryHarness(t)
	seedLearningProfilePage(t,db,1,"a","kb-a")
	seedLearningProfilePage(t,db,1,"b","kb-other")
	seedLearningProfilePage(t,db,2,"c","kb-a")
	ctx:=t.Context()
	alice:=interfaces.MemoryScope{TenantID:1,SubjectID:"web_user:alice"}
	bob:=interfaces.MemoryScope{TenantID:1,SubjectID:"web_user:bob"}
	for _,scope:=range []interfaces.MemoryScope{alice,bob} {
		_,err:=repo.UpsertKnowledgeState(ctx,scope,&types.UserKnowledgeState{WikiPageID:"a",Status:"familiar",EvidenceCount:1});require.NoError(t,err)
	}
	_,err:=repo.UpsertEvidence(ctx,bob,&types.LearningEvidence{WikiPageID:"a",EvidenceType:types.LearningEvidenceTypeMemoryLink,Level:types.LearningEvidenceLevelFamiliarity,SourceType:types.LearningEvidenceSourceMemoryWikiLink,SourceID:"bob-link"});require.NoError(t,err)
	signals,err:=repo.ListRecommendationSignals(ctx,alice,[]string{"a"});require.NoError(t,err)
	require.Len(t,signals.States,1);require.Equal(t,alice.SubjectID,signals.States[0].SubjectID);require.Empty(t,signals.MemorySupportedPageIDs)
	_,err=repo.UpsertEvidence(ctx,alice,&types.LearningEvidence{WikiPageID:"a",EvidenceType:types.LearningEvidenceTypeChatInteraction,Level:types.LearningEvidenceLevelExposure,SourceType:types.LearningEvidenceSourceChatMessage,SourceID:"message"});require.NoError(t,err)
	signals,err=repo.ListRecommendationSignals(ctx,alice,[]string{"a"});require.NoError(t,err);require.Empty(t,signals.MemorySupportedPageIDs)
	for _,source:=range []string{"link-1","link-2"} {
		_,err=repo.UpsertEvidence(ctx,alice,&types.LearningEvidence{WikiPageID:"a",EvidenceType:types.LearningEvidenceTypeMemoryLink,Level:types.LearningEvidenceLevelFamiliarity,SourceType:types.LearningEvidenceSourceMemoryWikiLink,SourceID:source});require.NoError(t,err)
	}
	signals,err=repo.ListRecommendationSignals(ctx,alice,[]string{"a"});require.NoError(t,err);require.Equal(t,[]string{"a"},signals.MemorySupportedPageIDs)
	signals,err=repo.ListRecommendationSignals(ctx,interfaces.MemoryScope{TenantID:2,SubjectID:alice.SubjectID},[]string{"a","c"});require.NoError(t,err);require.Empty(t,signals.States);require.Empty(t,signals.MemorySupportedPageIDs)
	signals,err=repo.ListRecommendationSignals(ctx,alice,nil);require.NoError(t,err);require.Empty(t,signals.States)
	// Graph projection excludes other tenants/KBs and does not read content.
	require.NoError(t,db.Model(&types.WikiPage{}).Where("id = ?","a").Update("content","private wiki body").Error)
	wiki:=NewWikiPageRepository(db)
	pages,err:=wiki.ListLearningGraphPages(ctx,1,"kb-a",1);require.NoError(t,err);require.Len(t,pages,1);require.Equal(t,"a",pages[0].ID);require.Empty(t,pages[0].Content)
	pages,err=wiki.ListLearningGraphPages(ctx,2,"kb-a",10);require.NoError(t,err);require.Len(t,pages,1);require.Equal(t,"c",pages[0].ID)
}
