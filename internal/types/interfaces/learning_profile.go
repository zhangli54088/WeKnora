package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// LearningProfileRepository persists evidence and materialized states. Every
// data method takes an explicit MemoryScope; InTransaction supplies a
// transaction-bound repository for atomic evidence/state recomputation.
type LearningProfileRepository interface {
	InTransaction(ctx context.Context, fn func(LearningProfileRepository) error) error
	UpsertEvidence(ctx context.Context, scope MemoryScope, evidence *types.LearningEvidence) (*types.LearningEvidence, error)
	ListEvidence(ctx context.Context, scope MemoryScope, wikiPageID string) ([]*types.LearningEvidence, error)
	DeleteEvidenceBySource(ctx context.Context, scope MemoryScope, sourceType, sourceID, wikiPageID string) (int64, error)
	UpsertKnowledgeState(ctx context.Context, scope MemoryScope, state *types.UserKnowledgeState) (*types.UserKnowledgeState, error)
	DeleteKnowledgeState(ctx context.Context, scope MemoryScope, wikiPageID string) (bool, error)
	GetKnowledgeState(ctx context.Context, scope MemoryScope, wikiPageID string) (*types.UserKnowledgeState, error)
	ListKnowledgeStates(ctx context.Context, scope MemoryScope) ([]*types.UserKnowledgeState, error)
}

// LearningProfileService converts source events into evidence, recomputes
// materialized state, and exposes the authenticated caller's profile.
type LearningProfileService interface {
	SyncMemoryWikiLink(ctx context.Context, link *types.MemoryWikiLink) error
	RecordChatInteractions(ctx context.Context, sessionID, messageID, knowledgeBaseID string, candidates []*types.MemoryWikiCandidate) error
	RemoveMemoryWikiLinkEvidence(ctx context.Context, link *types.MemoryWikiLink) error
	RecomputeKnowledgeState(ctx context.Context, wikiPageID string) (*types.UserKnowledgeState, error)
	ListEvidence(ctx context.Context, wikiPageID string) ([]*types.LearningEvidence, error)
	ListKnowledgeStates(ctx context.Context, knowledgeBaseID string) ([]*types.UserKnowledgeStateView, error)
}
