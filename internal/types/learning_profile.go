package types

import "time"

const (
	LearningEvidenceTypeMemoryLink      = "memory_link"
	LearningEvidenceTypeChatInteraction = "chat_interaction"

	LearningEvidenceLevelExposure    = "exposure"
	LearningEvidenceLevelFamiliarity = "familiarity"
	LearningEvidenceLevelMastery     = "mastery"

	LearningEvidenceSourceMemoryWikiLink = "memory_wiki_link"
	LearningEvidenceSourceChatMessage    = "chat_message"

	UserKnowledgeStatusExposed  = "exposed"
	UserKnowledgeStatusFamiliar = "familiar"
	UserKnowledgeStatusMastered = "mastered"
)

// LearningEvidence is one auditable observation supporting a user's state on
// a WikiPage. Weight describes evidence reliability; it is never a mastery
// score. Mapping relevance remains only in Metadata for auditability.
type LearningEvidence struct {
	ID           string    `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID     uint64    `json:"tenant_id" gorm:"not null;uniqueIndex:idx_learning_evidence_source,priority:1"`
	SubjectID    string    `json:"subject_id" gorm:"type:varchar(512);not null;uniqueIndex:idx_learning_evidence_source,priority:2"`
	WikiPageID   string    `json:"wiki_page_id" gorm:"type:varchar(36);not null;uniqueIndex:idx_learning_evidence_source,priority:5"`
	EvidenceType string    `json:"evidence_type" gorm:"type:varchar(64);not null"`
	Level        string    `json:"level" gorm:"type:varchar(32);not null"`
	SourceType   string    `json:"source_type" gorm:"type:varchar(64);not null;uniqueIndex:idx_learning_evidence_source,priority:3"`
	SourceID     string    `json:"source_id" gorm:"type:varchar(64);not null;uniqueIndex:idx_learning_evidence_source,priority:4"`
	Weight       float64   `json:"weight" gorm:"not null;default:1"`
	Metadata     JSONMap   `json:"metadata" gorm:"type:jsonb;not null;default:'{}'"`
	OccurredAt   time.Time `json:"occurred_at" gorm:"not null"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func (LearningEvidence) TableName() string { return "learning_evidences" }

// UserKnowledgeState is the materialized aggregate for one user and WikiPage.
// Missing rows are intentionally interpreted as unknown.
type UserKnowledgeState struct {
	ID             string    `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID       uint64    `json:"tenant_id" gorm:"not null;uniqueIndex:idx_user_knowledge_state_scope,priority:1"`
	SubjectID      string    `json:"subject_id" gorm:"type:varchar(512);not null;uniqueIndex:idx_user_knowledge_state_scope,priority:2"`
	WikiPageID     string    `json:"wiki_page_id" gorm:"type:varchar(36);not null;uniqueIndex:idx_user_knowledge_state_scope,priority:3"`
	Status         string    `json:"status" gorm:"type:varchar(32);not null"`
	// Confidence is the reliability of the winning evidence level, not a
	// probability that the user has mastered the page.
	Confidence     float64   `json:"confidence" gorm:"not null;default:0"`
	EvidenceCount  int       `json:"evidence_count" gorm:"not null;default:0"`
	LastEvidenceAt time.Time `json:"last_evidence_at" gorm:"not null"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func (UserKnowledgeState) TableName() string { return "user_knowledge_states" }

// UserKnowledgeStateView adds the current lightweight WikiPage identity to a
// state without copying Wiki content into the learning profile tables.
type UserKnowledgeStateView struct {
	ID              string    `json:"id"`
	WikiPageID      string    `json:"wiki_page_id"`
	Title           string    `json:"title"`
	Slug            string    `json:"slug"`
	KnowledgeBaseID string    `json:"knowledge_base_id"`
	Status          string    `json:"status"`
	Confidence      float64   `json:"confidence"`
	EvidenceCount   int       `json:"evidence_count"`
	LastEvidenceAt  time.Time `json:"last_evidence_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}
