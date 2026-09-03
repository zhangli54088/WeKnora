package memory

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type learningRecommendationService struct {
	kbRepo interfaces.KnowledgeBaseRepository
	wikiRepo interfaces.WikiPageRepository
	profileRepo interfaces.LearningProfileRepository
}

func NewLearningRecommendationService(kbRepo interfaces.KnowledgeBaseRepository, wikiRepo interfaces.WikiPageRepository, profileRepo interfaces.LearningProfileRepository) interfaces.LearningRecommendationService {
	return &learningRecommendationService{kbRepo: kbRepo, wikiRepo: wikiRepo, profileRepo: profileRepo}
}

func (s *learningRecommendationService) ListRecommendations(ctx context.Context, knowledgeBaseID string, limit int) (*types.LearningRecommendationView, error) {
	started := time.Now()
	scope, err := ResolveScope(ctx); if err != nil { return nil, err }
	knowledgeBaseID = strings.TrimSpace(knowledgeBaseID)
	if knowledgeBaseID == "" { return nil, ErrMemoryWikiKnowledgeBaseNotFound }
	if limit <= 0 { limit = types.LearningRecommendationDefaultLimit }
	if limit > types.LearningRecommendationMaxLimit { limit = types.LearningRecommendationMaxLimit }
	logCtx := logger.WithFields(ctx, logger.Fields{"tenant_id": scope.TenantID, "subject_id": scope.SubjectID, "knowledge_base_id": knowledgeBaseID})
	fail := func(stage string, err error) (*types.LearningRecommendationView, error) {
		logger.WarnWithFields(logCtx, logger.Fields{"stage": stage, "error": err.Error()}, "[learning-recommendation] recommendation_failed")
		return nil, err
	}
	kb, err := s.kbRepo.GetKnowledgeBaseByID(ctx, knowledgeBaseID)
	if err != nil || kb == nil || kb.TenantID != scope.TenantID { return fail("knowledge_base", ErrMemoryWikiKnowledgeBaseNotFound) }
	view := &types.LearningRecommendationView{
		KnowledgeBaseID: knowledgeBaseID, GeneratedAt: started.UTC(), ScoringAt: started.UTC().Truncate(time.Hour), WikiEnabled: kb.IsWikiEnabled(),
		Recommendations: []types.LearningRecommendation{}, ContextGraph: types.WikiGraphData{Nodes: []types.WikiGraphNode{}, Edges: []types.WikiGraphEdge{}},
	}
	if !view.WikiEnabled { return view, nil }
	pages, err := s.wikiRepo.ListLearningGraphPages(ctx, scope.TenantID, knowledgeBaseID, types.LearningRecommendationMaxGraphNodes+1)
	if err != nil { return fail("graph", fmt.Errorf("load recommendation graph: %w", err)) }
	if len(pages) > types.LearningRecommendationMaxGraphNodes { pages = pages[:types.LearningRecommendationMaxGraphNodes]; view.Truncated = true }
	safePages := make([]*types.WikiPage, 0, len(pages))
	for _, p := range pages {
		if p != nil && p.ID != "" && p.Slug != "" && p.TenantID == scope.TenantID && p.KnowledgeBaseID == knowledgeBaseID && p.Status != types.WikiPageStatusArchived && p.PageType != types.WikiPageTypeIndex { safePages = append(safePages, p) }
	}
	graph := buildRecommendationGraph(safePages)
	signals, err := s.profileRepo.ListRecommendationSignals(ctx, scope, graph.ids)
	if err != nil { return fail("profile", fmt.Errorf("load recommendation signals: %w", err)) }
	states := map[string]*types.UserKnowledgeState{}
	memory := map[string]bool{}
	anchorCount := 0
	if signals != nil {
		for _, state := range signals.States {
			if state != nil && state.TenantID == scope.TenantID && state.SubjectID == scope.SubjectID && graph.pages[state.WikiPageID] != nil {
				states[state.WikiPageID] = state
				if state.EvidenceCount > 0 && recommendationAnchorStrength(state.Status) > 0 { anchorCount++ }
			}
		}
		for _, id := range signals.MemorySupportedPageIDs { if states[id] != nil { memory[id] = true } }
	}
	logger.Info(logger.WithField(logCtx, "anchor_count", anchorCount), "[learning-recommendation] recommendation_start")
	candidates, truncated := generateLearningCandidates(graph, states, limit)
	view.Truncated = view.Truncated || truncated
	oneHop := 0; for _, c := range candidates { if c.hop == 1 { oneHop++ } }
	logger.Info(logger.WithFields(logCtx, logger.Fields{"candidate_count": len(candidates), "one_hop_count": oneHop, "two_hop_count": len(candidates)-oneHop}), "[learning-recommendation] candidate_generated")
	view.Recommendations = rankLearningCandidates(candidates, graph, states, memory, view.ScoringAt)
	if len(view.Recommendations) > limit { view.Recommendations = view.Recommendations[:limit] }
	view.ContextGraph = recommendationContextGraph(graph, view.Recommendations)
	logger.Info(logger.WithFields(logCtx, logger.Fields{"returned_count": len(view.Recommendations), "duration_ms": time.Since(started).Milliseconds(), "truncated": view.Truncated}), "[learning-recommendation] recommendation_done")
	return view, nil
}
