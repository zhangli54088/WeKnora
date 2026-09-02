package memory

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

const (
	defaultMemoryWikiTopK = 5
	maxMemoryWikiTopK     = 20
	minMemoryWikiSearch   = 20
	maxMemoryWikiSearch   = 100
	// A document-level match is useful for summary pages, which deliberately
	// have no chunk_refs, but is weaker than an exact cited-chunk match.
	memoryWikiSourceRefDiscount = 0.8
)

var (
	// ErrMemoryWikiKnowledgeBaseNotFound hides cross-tenant KB existence.
	ErrMemoryWikiKnowledgeBaseNotFound = errors.New("memory wiki: knowledge base not found")
	// ErrMemoryWikiPageNotFound hides cross-tenant Wiki page existence.
	ErrMemoryWikiPageNotFound = errors.New("memory wiki: page not found")
	// ErrMemoryWikiLinkNotFound hides cross-tenant relation existence.
	ErrMemoryWikiLinkNotFound = errors.New("memory wiki: link not found")
	// ErrMemoryWikiDisabled means the requested KB has no Wiki pipeline.
	ErrMemoryWikiDisabled = errors.New("memory wiki: wiki is not enabled for knowledge base")
)

type memoryWikiService struct {
	memoryRepo interfaces.MemoryRepository
	linkRepo   interfaces.MemoryWikiRepository
	kbService  interfaces.KnowledgeBaseService
	profile    interfaces.LearningProfileService
}

type memoryWikiEvidence struct {
	chunkScores map[string]float64
	docScore    float64
}

// NewMemoryWikiService builds the minimal MemoryItem-to-WikiPage projection
// service from existing Memory, Retriever, and Wiki capabilities.
func NewMemoryWikiService(
	memoryRepo interfaces.MemoryRepository,
	linkRepo interfaces.MemoryWikiRepository,
	kbService interfaces.KnowledgeBaseService,
	profile interfaces.LearningProfileService,
) interfaces.MemoryWikiService {
	return &memoryWikiService{
		memoryRepo: memoryRepo,
		linkRepo:   linkRepo,
		kbService:  kbService,
		profile:    profile,
	}
}

func normalizeMemoryWikiTopK(topK int) int {
	if topK <= 0 {
		return defaultMemoryWikiTopK
	}
	if topK > maxMemoryWikiTopK {
		return maxMemoryWikiTopK
	}
	return topK
}

func memoryWikiSearchCount(topK int) int {
	count := topK * 5
	if count < minMemoryWikiSearch {
		count = minMemoryWikiSearch
	}
	if count > maxMemoryWikiSearch {
		count = maxMemoryWikiSearch
	}
	return count
}

func (s *memoryWikiService) scopedMemory(
	ctx context.Context, memoryItemID string,
) (interfaces.MemoryScope, *types.MemoryItem, error) {
	scope, err := ResolveScope(ctx)
	if err != nil {
		return interfaces.MemoryScope{}, nil, err
	}
	item, err := s.memoryRepo.GetItem(ctx, scope, memoryItemID)
	if err != nil {
		return scope, nil, err
	}
	if item == nil {
		return scope, nil, ErrItemNotFound
	}
	return scope, item, nil
}

func (s *memoryWikiService) scopedWikiKB(
	ctx context.Context, scope interfaces.MemoryScope, knowledgeBaseID string,
) (*types.KnowledgeBase, error) {
	if knowledgeBaseID == "" {
		return nil, ErrMemoryWikiKnowledgeBaseNotFound
	}
	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, knowledgeBaseID)
	if err != nil || kb == nil || kb.TenantID != scope.TenantID {
		return nil, ErrMemoryWikiKnowledgeBaseNotFound
	}
	if !kb.IsWikiEnabled() {
		return nil, ErrMemoryWikiDisabled
	}
	return kb, nil
}

// FindCandidates retrieves semantically relevant source chunks with the KB's
// existing HybridSearch, then projects that evidence through WikiPage refs.
func (s *memoryWikiService) FindCandidates(
	ctx context.Context, memoryItemID, knowledgeBaseID string, topK int,
) ([]*types.MemoryWikiCandidate, error) {
	_, item, err := s.scopedMemory(ctx, memoryItemID)
	if err != nil {
		return nil, err
	}
	return s.FindCandidatesForText(ctx, embeddableText(item, nil), knowledgeBaseID, topK)
}

// FindCandidatesForText projects arbitrary caller-owned text through the same
// HybridSearch -> ChunkRefs/SourceRefs -> WikiPage path used by MemoryItem.
// The authenticated scope and Wiki-enabled KB check remain mandatory so this
// reusable entry point cannot become a cross-tenant lookup primitive.
func (s *memoryWikiService) FindCandidatesForText(
	ctx context.Context, text, knowledgeBaseID string, topK int,
) ([]*types.MemoryWikiCandidate, error) {
	scope, err := ResolveScope(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := s.scopedWikiKB(ctx, scope, knowledgeBaseID); err != nil {
		return nil, err
	}
	topK = normalizeMemoryWikiTopK(topK)

	query := strings.TrimSpace(text)
	if strings.TrimSpace(query) == "" {
		return []*types.MemoryWikiCandidate{}, nil
	}
	results, err := s.kbService.HybridSearch(ctx, knowledgeBaseID, types.SearchParams{
		QueryText:             query,
		MatchCount:            memoryWikiSearchCount(topK),
		SkipContextEnrichment: true,
	})
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return []*types.MemoryWikiCandidate{}, nil
	}

	byKnowledge := make(map[string]*memoryWikiEvidence)
	for _, result := range results {
		if result == nil || result.KnowledgeID == "" || result.KnowledgeBaseID != knowledgeBaseID {
			continue
		}
		ev := byKnowledge[result.KnowledgeID]
		if ev == nil {
			ev = &memoryWikiEvidence{chunkScores: make(map[string]float64)}
			byKnowledge[result.KnowledgeID] = ev
		}
		if result.Score > ev.docScore {
			ev.docScore = result.Score
		}
		if result.ID != "" && result.Score > ev.chunkScores[result.ID] {
			ev.chunkScores[result.ID] = result.Score
		}
	}

	knowledgeIDs := make([]string, 0, len(byKnowledge))
	allChunkScores := make(map[string]float64)
	for knowledgeID, ev := range byKnowledge {
		knowledgeIDs = append(knowledgeIDs, knowledgeID)
		for chunkID, score := range ev.chunkScores {
			if score > allChunkScores[chunkID] {
				allChunkScores[chunkID] = score
			}
		}
	}
	pages, err := s.linkRepo.ListWikiPagesBySourceRefs(
		ctx, scope.TenantID, knowledgeBaseID, knowledgeIDs,
	)
	if err != nil {
		return nil, err
	}

	candidates := make(map[string]*types.MemoryWikiCandidate)
	for _, page := range pages {
		if page == nil || page.TenantID != scope.TenantID ||
			page.KnowledgeBaseID != knowledgeBaseID || page.Status == types.WikiPageStatusArchived {
			continue
		}
		score, method := exactWikiChunkScore(page.ChunkRefs, allChunkScores)
		if method == "" && len(page.ChunkRefs) == 0 {
			if docScore := bestWikiSourceScore(page, byKnowledge); docScore > 0 {
				score = docScore * memoryWikiSourceRefDiscount
				method = types.MemoryWikiMethodSourceRef
			}
		}
		if method == "" {
			continue
		}
		current := candidates[page.ID]
		if current != nil && current.Score >= score {
			continue
		}
		candidates[page.ID] = &types.MemoryWikiCandidate{
			WikiPageID:      page.ID,
			Title:           page.Title,
			Slug:            page.Slug,
			KnowledgeBaseID: page.KnowledgeBaseID,
			Score:           score,
			Method:          method,
		}
	}

	out := make([]*types.MemoryWikiCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		out = append(out, candidate)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score == out[j].Score {
			if out[i].Title == out[j].Title {
				return out[i].WikiPageID < out[j].WikiPageID
			}
			return out[i].Title < out[j].Title
		}
		return out[i].Score > out[j].Score
	})
	if len(out) > topK {
		out = out[:topK]
	}
	return out, nil
}

func bestWikiSourceScore(
	page *types.WikiPage, byKnowledge map[string]*memoryWikiEvidence,
) float64 {
	best := 0.0
	for _, knowledgeID := range page.SourceKnowledgeIDs() {
		if ev := byKnowledge[knowledgeID]; ev != nil && ev.docScore > best {
			best = ev.docScore
		}
	}
	return best
}

func exactWikiChunkScore(refs types.StringArray, scores map[string]float64) (float64, string) {
	best := 0.0
	matched := false
	for _, ref := range refs {
		if score, ok := scores[ref]; ok {
			matched = true
			if score > best {
				best = score
			}
		}
	}
	if !matched {
		return 0, ""
	}
	return best, types.MemoryWikiMethodChunkRef
}

func normalizeMemoryWikiScore(score float64) float64 {
	if math.IsNaN(score) || math.IsInf(score, 0) || score < 0 {
		return 0
	}
	return score
}

func normalizeMemoryWikiMethod(method string) string {
	switch strings.TrimSpace(method) {
	case types.MemoryWikiMethodChunkRef:
		return types.MemoryWikiMethodChunkRef
	case types.MemoryWikiMethodSourceRef:
		return types.MemoryWikiMethodSourceRef
	default:
		return types.MemoryWikiMethodManual
	}
}

func (s *memoryWikiService) UpsertLink(
	ctx context.Context, memoryItemID, wikiPageID string, score float64, method string,
) (*types.MemoryWikiLinkView, error) {
	scope, item, err := s.scopedMemory(ctx, memoryItemID)
	if err != nil {
		return nil, err
	}
	page, err := s.linkRepo.GetWikiPage(ctx, scope.TenantID, "", wikiPageID)
	if err != nil {
		return nil, err
	}
	if page == nil {
		return nil, ErrMemoryWikiPageNotFound
	}
	if _, err := s.scopedWikiKB(ctx, scope, page.KnowledgeBaseID); err != nil {
		return nil, err
	}
	link, err := s.linkRepo.UpsertLink(ctx, scope, &types.MemoryWikiLink{
		MemoryItemID:    item.ID,
		WikiPageID:      page.ID,
		KnowledgeBaseID: page.KnowledgeBaseID,
		Score:           normalizeMemoryWikiScore(score),
		Method:          normalizeMemoryWikiMethod(method),
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrMemoryWikiPageNotFound
		}
		return nil, err
	}
	if err := s.profile.SyncMemoryWikiLink(ctx, link); err != nil {
		return nil, fmt.Errorf("sync memory wiki learning evidence: %w", err)
	}
	return &types.MemoryWikiLinkView{
		Link: link, MemoryItem: item, WikiPage: memoryWikiPageRef(page),
	}, nil
}

func (s *memoryWikiService) ListLinks(ctx context.Context) ([]*types.MemoryWikiLinkView, error) {
	scope, err := ResolveScope(ctx)
	if err != nil {
		return nil, err
	}
	links, err := s.linkRepo.ListLinks(ctx, scope)
	if err != nil {
		return nil, err
	}
	views := make([]*types.MemoryWikiLinkView, 0, len(links))
	for _, link := range links {
		if link == nil {
			continue
		}
		item, err := s.memoryRepo.GetItem(ctx, scope, link.MemoryItemID)
		if err != nil {
			return nil, err
		}
		page, err := s.linkRepo.GetWikiPage(ctx, scope.TenantID, link.KnowledgeBaseID, link.WikiPageID)
		if err != nil {
			return nil, err
		}
		if item == nil || page == nil {
			continue
		}
		views = append(views, &types.MemoryWikiLinkView{
			Link: link, MemoryItem: item, WikiPage: memoryWikiPageRef(page),
		})
	}
	return views, nil
}

func memoryWikiPageRef(page *types.WikiPage) *types.MemoryWikiPageRef {
	if page == nil {
		return nil
	}
	return &types.MemoryWikiPageRef{
		WikiPageID: page.ID, Title: page.Title, Slug: page.Slug,
		KnowledgeBaseID: page.KnowledgeBaseID,
	}
}

func (s *memoryWikiService) DeleteLink(ctx context.Context, id string) error {
	scope, err := ResolveScope(ctx)
	if err != nil {
		return err
	}
	link, err := s.linkRepo.GetLink(ctx, scope, id)
	if err != nil {
		return err
	}
	if link == nil {
		return ErrMemoryWikiLinkNotFound
	}
	// Evidence/state are cleaned first so a link-delete failure remains fully
	// retryable: the still-present scoped link lets the next call recompute.
	if err := s.profile.RemoveMemoryWikiLinkEvidence(ctx, link); err != nil {
		return fmt.Errorf("remove memory wiki learning evidence: %w", err)
	}
	deleted, err := s.linkRepo.DeleteLink(ctx, scope, id)
	if err != nil {
		return err
	}
	if !deleted {
		return ErrMemoryWikiLinkNotFound
	}
	return nil
}
