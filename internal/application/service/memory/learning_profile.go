package memory

import (
	"context"
	"math"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	memoryWikiEvidenceWeight = 1.0
	// Chat interaction only proves exposure. The fixed weight expresses the
	// reliability of that observation and is deliberately unrelated to the
	// retrieval score or mastery.
	chatInteractionEvidenceWeight = 0.7
)

type learningProfileService struct {
	repo     interfaces.LearningProfileRepository
	wikiRepo interfaces.MemoryWikiRepository
}

func NewLearningProfileService(
	repo interfaces.LearningProfileRepository,
	wikiRepo interfaces.MemoryWikiRepository,
) interfaces.LearningProfileService {
	return &learningProfileService{repo: repo, wikiRepo: wikiRepo}
}

func (s *learningProfileService) SyncMemoryWikiLink(
	ctx context.Context, link *types.MemoryWikiLink,
) error {
	scope, err := ResolveScope(ctx)
	if err != nil {
		return err
	}
	if link == nil || link.ID == "" || link.WikiPageID == "" ||
		link.TenantID != scope.TenantID || link.SubjectID != scope.SubjectID {
		return ErrMemoryWikiLinkNotFound
	}

	occurredAt := link.UpdatedAt
	if occurredAt.IsZero() {
		occurredAt = link.CreatedAt
	}
	if occurredAt.IsZero() {
		occurredAt = time.Now()
	}
	evidence := &types.LearningEvidence{
		WikiPageID:   link.WikiPageID,
		EvidenceType: types.LearningEvidenceTypeMemoryLink,
		Level:        types.LearningEvidenceLevelFamiliarity,
		SourceType:   types.LearningEvidenceSourceMemoryWikiLink,
		SourceID:     link.ID,
		Weight:       memoryWikiEvidenceWeight,
		Metadata: types.JSONMap{
			"memory_item_id":    link.MemoryItemID,
			"knowledge_base_id": link.KnowledgeBaseID,
			"mapping_score":     link.Score,
			"mapping_method":    link.Method,
		},
		OccurredAt: occurredAt,
	}

	return s.repo.InTransaction(ctx, func(txRepo interfaces.LearningProfileRepository) error {
		if _, err := txRepo.UpsertEvidence(ctx, scope, evidence); err != nil {
			return err
		}
		_, err := recomputeKnowledgeStateWithRepo(ctx, txRepo, scope, link.WikiPageID)
		return err
	})
}

// RecordChatInteractions atomically upserts every safe candidate produced for
// one persisted user message and recomputes the affected materialized states.
// The evidence unique key makes task retries idempotent.
func (s *learningProfileService) RecordChatInteractions(
	ctx context.Context,
	sessionID, messageID, knowledgeBaseID string,
	candidates []*types.MemoryWikiCandidate,
) error {
	scope, err := ResolveScope(ctx)
	if err != nil {
		return err
	}
	messageID = strings.TrimSpace(messageID)
	knowledgeBaseID = strings.TrimSpace(knowledgeBaseID)
	if messageID == "" || knowledgeBaseID == "" || len(candidates) == 0 {
		return nil
	}

	return s.repo.InTransaction(ctx, func(txRepo interfaces.LearningProfileRepository) error {
		affected := make(map[string]struct{}, len(candidates))
		for rank, candidate := range candidates {
			if candidate == nil || candidate.WikiPageID == "" ||
				candidate.KnowledgeBaseID != knowledgeBaseID ||
				candidate.Method != types.MemoryWikiMethodChunkRef {
				continue
			}
			evidence := &types.LearningEvidence{
				WikiPageID:   candidate.WikiPageID,
				EvidenceType: types.LearningEvidenceTypeChatInteraction,
				Level:        types.LearningEvidenceLevelExposure,
				SourceType:   types.LearningEvidenceSourceChatMessage,
				SourceID:     messageID,
				Weight:       chatInteractionEvidenceWeight,
				Metadata: types.JSONMap{
					"message_id":         messageID,
					"session_id":         strings.TrimSpace(sessionID),
					"knowledge_base_id":  knowledgeBaseID,
					"mapping_score":      candidate.Score,
					"mapping_method":     candidate.Method,
					"rank":               rank + 1,
				},
				OccurredAt: time.Now(),
			}
			if _, err := txRepo.UpsertEvidence(ctx, scope, evidence); err != nil {
				return err
			}
			affected[candidate.WikiPageID] = struct{}{}
		}
		for wikiPageID := range affected {
			if _, err := recomputeKnowledgeStateWithRepo(ctx, txRepo, scope, wikiPageID); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *learningProfileService) RemoveMemoryWikiLinkEvidence(
	ctx context.Context, link *types.MemoryWikiLink,
) error {
	scope, err := ResolveScope(ctx)
	if err != nil {
		return err
	}
	if link == nil || link.ID == "" || link.WikiPageID == "" ||
		link.TenantID != scope.TenantID || link.SubjectID != scope.SubjectID {
		return ErrMemoryWikiLinkNotFound
	}

	return s.repo.InTransaction(ctx, func(txRepo interfaces.LearningProfileRepository) error {
		if _, err := txRepo.DeleteEvidenceBySource(
			ctx,
			scope,
			types.LearningEvidenceSourceMemoryWikiLink,
			link.ID,
			link.WikiPageID,
		); err != nil {
			return err
		}
		_, err := recomputeKnowledgeStateWithRepo(ctx, txRepo, scope, link.WikiPageID)
		return err
	})
}

func (s *learningProfileService) RecomputeKnowledgeState(
	ctx context.Context, wikiPageID string,
) (*types.UserKnowledgeState, error) {
	scope, err := ResolveScope(ctx)
	if err != nil {
		return nil, err
	}
	var state *types.UserKnowledgeState
	err = s.repo.InTransaction(ctx, func(txRepo interfaces.LearningProfileRepository) error {
		var recomputeErr error
		state, recomputeErr = recomputeKnowledgeStateWithRepo(
			ctx, txRepo, scope, strings.TrimSpace(wikiPageID),
		)
		return recomputeErr
	})
	return state, err
}

func recomputeKnowledgeStateWithRepo(
	ctx context.Context,
	repo interfaces.LearningProfileRepository,
	scope interfaces.MemoryScope,
	wikiPageID string,
) (*types.UserKnowledgeState, error) {
	evidence, err := repo.ListEvidence(ctx, scope, wikiPageID)
	if err != nil {
		return nil, err
	}
	state := aggregateKnowledgeEvidence(wikiPageID, evidence)
	if state == nil {
		_, err := repo.DeleteKnowledgeState(ctx, scope, wikiPageID)
		return nil, err
	}
	return repo.UpsertKnowledgeState(ctx, scope, state)
}

func aggregateKnowledgeEvidence(
	wikiPageID string, evidence []*types.LearningEvidence,
) *types.UserKnowledgeState {
	winningRank := 0
	winningWeight := 0.0
	evidenceCount := 0
	var lastEvidenceAt time.Time

	for _, item := range evidence {
		if item == nil || item.WikiPageID != wikiPageID {
			continue
		}
		rank := learningEvidenceLevelRank(item.Level)
		if rank == 0 {
			continue
		}
		evidenceCount++
		occurredAt := item.OccurredAt
		if occurredAt.IsZero() {
			occurredAt = item.CreatedAt
		}
		if occurredAt.After(lastEvidenceAt) {
			lastEvidenceAt = occurredAt
		}
		weight := normalizeEvidenceWeight(item.Weight)
		if rank > winningRank {
			winningRank = rank
			winningWeight = weight
		} else if rank == winningRank && weight > winningWeight {
			winningWeight = weight
		}
	}
	if winningRank == 0 {
		return nil
	}

	return &types.UserKnowledgeState{
		WikiPageID:     wikiPageID,
		Status:         knowledgeStatusForRank(winningRank),
		Confidence:     winningWeight,
		EvidenceCount:  evidenceCount,
		LastEvidenceAt: lastEvidenceAt,
	}
}

func learningEvidenceLevelRank(level string) int {
	switch strings.TrimSpace(level) {
	case types.LearningEvidenceLevelExposure:
		return 1
	case types.LearningEvidenceLevelFamiliarity:
		return 2
	case types.LearningEvidenceLevelMastery:
		return 3
	default:
		return 0
	}
}

func knowledgeStatusForRank(rank int) string {
	switch rank {
	case 3:
		return types.UserKnowledgeStatusMastered
	case 2:
		return types.UserKnowledgeStatusFamiliar
	default:
		return types.UserKnowledgeStatusExposed
	}
}

func normalizeEvidenceWeight(weight float64) float64 {
	if math.IsNaN(weight) || math.IsInf(weight, 0) || weight < 0 {
		return 0
	}
	if weight > 1 {
		return 1
	}
	return weight
}

func (s *learningProfileService) ListEvidence(
	ctx context.Context, wikiPageID string,
) ([]*types.LearningEvidence, error) {
	scope, err := ResolveScope(ctx)
	if err != nil {
		return nil, err
	}
	return s.repo.ListEvidence(ctx, scope, strings.TrimSpace(wikiPageID))
}

func (s *learningProfileService) ListKnowledgeStates(
	ctx context.Context, knowledgeBaseID string,
) ([]*types.UserKnowledgeStateView, error) {
	scope, err := ResolveScope(ctx)
	if err != nil {
		return nil, err
	}
	states, err := s.repo.ListKnowledgeStates(ctx, scope)
	if err != nil {
		return nil, err
	}
	knowledgeBaseID = strings.TrimSpace(knowledgeBaseID)
	views := make([]*types.UserKnowledgeStateView, 0, len(states))
	for _, state := range states {
		if state == nil {
			continue
		}
		page, err := s.wikiRepo.GetWikiPage(ctx, scope.TenantID, "", state.WikiPageID)
		if err != nil {
			return nil, err
		}
		if page == nil || knowledgeBaseID != "" && page.KnowledgeBaseID != knowledgeBaseID {
			continue
		}
		views = append(views, &types.UserKnowledgeStateView{
			ID:              state.ID,
			WikiPageID:      state.WikiPageID,
			Title:           page.Title,
			Slug:            page.Slug,
			KnowledgeBaseID: page.KnowledgeBaseID,
			Status:          state.Status,
			Confidence:      state.Confidence,
			EvidenceCount:   state.EvidenceCount,
			LastEvidenceAt:  state.LastEvidenceAt,
			UpdatedAt:       state.UpdatedAt,
		})
	}
	return views, nil
}
