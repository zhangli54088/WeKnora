package repository

import (
	"context"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

func (r *wikiPageRepository) ListLearningGraphPages(ctx context.Context, tenantID uint64, kbID string, limit int) ([]*types.WikiPage, error) {
	pages := []*types.WikiPage{}
	if tenantID == 0 || kbID == "" || limit <= 0 { return pages, nil }
	if limit > types.LearningRecommendationMaxGraphNodes+1 { limit = types.LearningRecommendationMaxGraphNodes+1 }
	err := r.db.WithContext(ctx).Select("id", "tenant_id", "knowledge_base_id", "slug", "title", "page_type", "status", "out_links").
		Where("tenant_id = ? AND knowledge_base_id = ? AND status <> ? AND page_type <> ?", tenantID, kbID, types.WikiPageStatusArchived, types.WikiPageTypeIndex).
		Order("id ASC").Limit(limit).Find(&pages).Error
	return pages, err
}

func (r *learningProfileRepository) ListRecommendationSignals(ctx context.Context, scope interfaces.MemoryScope, pageIDs []string) (*types.LearningRecommendationSignals, error) {
	result := &types.LearningRecommendationSignals{States: []*types.UserKnowledgeState{}, MemorySupportedPageIDs: []string{}}
	if !scope.Valid() || len(pageIDs) == 0 { return result, nil }
	// Defensive bound; the service passes only the already-bounded graph IDs.
	if len(pageIDs) > types.LearningRecommendationMaxGraphNodes { pageIDs = pageIDs[:types.LearningRecommendationMaxGraphNodes] }
	if err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND subject_id = ? AND wiki_page_id IN ?", scope.TenantID, scope.SubjectID, pageIDs).
		Order("wiki_page_id ASC").Find(&result.States).Error; err != nil { return nil, err }
	err := r.db.WithContext(ctx).Model(&types.LearningEvidence{}).
		Where("tenant_id = ? AND subject_id = ? AND wiki_page_id IN ?", scope.TenantID, scope.SubjectID, pageIDs).
		Where("evidence_type = ? AND level = ? AND source_type = ?", types.LearningEvidenceTypeMemoryLink, types.LearningEvidenceLevelFamiliarity, types.LearningEvidenceSourceMemoryWikiLink).
		Distinct("wiki_page_id").Order("wiki_page_id ASC").Pluck("wiki_page_id", &result.MemorySupportedPageIDs).Error
	return result, err
}
