package types

import "time"

const (
	// MemoryWikiMethodChunkRef means retrieval hit a source chunk explicitly
	// cited by the wiki page.
	MemoryWikiMethodChunkRef = "kb_retrieval_chunk_ref"
	// MemoryWikiMethodSourceRef is the document-level fallback used for wiki
	// summary pages, which intentionally have no chunk_refs.
	MemoryWikiMethodSourceRef = "kb_retrieval_source_ref"
	// MemoryWikiMethodManual records a user-confirmed link that did not carry
	// candidate provenance.
	MemoryWikiMethodManual = "manual"
)

// MemoryWikiLink persists one confirmed projection from a user's memory into
// an existing WikiPage. Score is retrieval relevance, never mastery.
type MemoryWikiLink struct {
	ID              string    `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID        uint64    `json:"tenant_id" gorm:"not null;uniqueIndex:idx_memory_wiki_link_scope,priority:1"`
	SubjectID       string    `json:"subject_id" gorm:"type:varchar(512);not null;uniqueIndex:idx_memory_wiki_link_scope,priority:2"`
	MemoryItemID    string    `json:"memory_item_id" gorm:"type:varchar(36);not null;uniqueIndex:idx_memory_wiki_link_scope,priority:3"`
	WikiPageID      string    `json:"wiki_page_id" gorm:"type:varchar(36);not null;uniqueIndex:idx_memory_wiki_link_scope,priority:4"`
	KnowledgeBaseID string    `json:"knowledge_base_id" gorm:"type:varchar(36);not null;index"`
	Score           float64   `json:"score" gorm:"not null;default:0"`
	Method          string    `json:"method" gorm:"type:varchar(64);not null;default:'manual'"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// TableName returns the durable relation table.
func (MemoryWikiLink) TableName() string { return "memory_wiki_links" }

// MemoryWikiCandidate is a semantic projection candidate backed by existing
// knowledge-base retrieval evidence.
type MemoryWikiCandidate struct {
	WikiPageID      string  `json:"wiki_page_id"`
	Title           string  `json:"title"`
	Slug            string  `json:"slug"`
	KnowledgeBaseID string  `json:"knowledge_base_id"`
	Score           float64 `json:"score"`
	Method          string  `json:"method"`
}

// MemoryWikiPageRef is the lightweight Wiki identity returned with a saved
// link; page content remains available from the existing Wiki API.
type MemoryWikiPageRef struct {
	WikiPageID      string `json:"wiki_page_id"`
	Title           string `json:"title"`
	Slug            string `json:"slug"`
	KnowledgeBaseID string `json:"knowledge_base_id"`
}

// MemoryWikiLinkView keeps the relation as the source of truth while exposing
// the current memory and wiki page needed by the prototype UI/API consumer.
type MemoryWikiLinkView struct {
	Link       *MemoryWikiLink    `json:"link"`
	MemoryItem *MemoryItem        `json:"memory_item"`
	WikiPage   *MemoryWikiPageRef `json:"wiki_page"`
}
