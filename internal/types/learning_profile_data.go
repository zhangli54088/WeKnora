package types

import "time"

// LearningProfileExport is a versioned personal-data document, not a database
// backup. It deliberately excludes tenant configuration and chat content.
type LearningProfileExport struct {
	Version         int                         `json:"version"`
	ExportedAt      time.Time                   `json:"exported_at"`
	Scope           LearningProfileExportScope  `json:"scope"`
	Memory          LearningProfileMemoryExport `json:"memory"`
	LearningProfile LearningProfileDataExport   `json:"learning_profile"`
}

type LearningProfileExportScope struct {
	TenantID  uint64 `json:"tenant_id"`
	SubjectID string `json:"subject_id"`
}

type LearningProfileMemoryExport struct {
	Items     []*MemoryItem       `json:"items"`
	Topics    []*MemoryTopicStat  `json:"topics"`
	Documents []*MemoryDocAffinity `json:"documents"`
}

type LearningProfileDataExport struct {
	MemoryWikiLinks   []*MemoryWikiLink         `json:"memory_wiki_links"`
	LearningEvidences []*LearningEvidenceExport `json:"learning_evidences"`
	KnowledgeStates  []*KnowledgeStateExport `json:"knowledge_states"`
}

// LearningEvidenceExport is an explicit allow-list. Never embed the model:
// future model fields must not silently become part of the download contract.
type LearningEvidenceExport struct {
	ID           string    `json:"id"`
	WikiPageID   string    `json:"wiki_page_id"`
	EvidenceType string    `json:"evidence_type"`
	Level        string    `json:"level"`
	SourceType   string    `json:"source_type"`
	SourceID     string    `json:"source_id"`
	Weight       float64   `json:"weight"`
	OccurredAt   time.Time `json:"occurred_at"`
	Metadata     JSONMap   `json:"metadata"`
}

// LearningProfileSnapshot is repository-internal data; it must be projected
// into LearningProfileExport before being serialized by a handler.
type LearningProfileSnapshot struct {
	Memory   LearningProfileMemoryExport
	Links    []*MemoryWikiLink
	Evidence []*LearningEvidence
	States   []*KnowledgeStateExport
}

// KnowledgeStateExport adds creation time without changing the existing
// knowledge-states endpoint's view contract.
type KnowledgeStateExport struct {
	UserKnowledgeStateView `gorm:"embedded"`
	CreatedAt time.Time `json:"created_at"`
}

type LearningProfileDeleteResult struct {
	MemoryWikiLinksDeleted   int64 `json:"memory_wiki_links_deleted"`
	LearningEvidencesDeleted int64 `json:"learning_evidences_deleted"`
	KnowledgeStatesDeleted  int64 `json:"knowledge_states_deleted"`
}
