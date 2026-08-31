package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// MemoryWikiRepository stores confirmed memory-to-wiki relations. Every
// operation receives the already-resolved memory scope explicitly.
type MemoryWikiRepository interface {
	UpsertLink(ctx context.Context, scope MemoryScope, link *types.MemoryWikiLink) (*types.MemoryWikiLink, error)
	ListLinks(ctx context.Context, scope MemoryScope) ([]*types.MemoryWikiLink, error)
	DeleteLink(ctx context.Context, scope MemoryScope, id string) (bool, error)
	GetWikiPage(ctx context.Context, tenantID uint64, knowledgeBaseID, pageID string) (*types.WikiPage, error)
	ListWikiPagesBySourceRefs(ctx context.Context, tenantID uint64, knowledgeBaseID string, knowledgeIDs []string) ([]*types.WikiPage, error)
}

// MemoryWikiService projects the caller's existing memories into Wiki pages
// and manages the links they confirm.
type MemoryWikiService interface {
	FindCandidates(ctx context.Context, memoryItemID, knowledgeBaseID string, topK int) ([]*types.MemoryWikiCandidate, error)
	UpsertLink(ctx context.Context, memoryItemID, wikiPageID string, score float64, method string) (*types.MemoryWikiLinkView, error)
	ListLinks(ctx context.Context) ([]*types.MemoryWikiLinkView, error)
	DeleteLink(ctx context.Context, id string) error
}
