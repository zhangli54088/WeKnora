package types

import "time"

const (
	LearningRecommendationDefaultLimit = 5
	LearningRecommendationMaxLimit = 20
	LearningRecommendationMaxGraphNodes = 1000
)

// Recommendation is a derived overlay, never a knowledge status or mastery probability.
type RecommendationScoreComponents struct {
	Structural float64 `json:"structural"`
	AnchorStrength float64 `json:"anchor_strength"`
	MultiAnchor float64 `json:"multi_anchor"`
	Recency float64 `json:"recency"`
	LongTermMemory float64 `json:"long_term_memory"`
}

type SupportingKnowledgeNode struct {
	WikiPageID string `json:"wiki_page_id"`
	Title string `json:"title"`
	Slug string `json:"slug"`
	Status string `json:"status"`
	EvidenceCount int `json:"evidence_count"`
	LastEvidenceAt time.Time `json:"last_evidence_at"`
	MemorySupported bool `json:"memory_supported"`
	// Path is a real Wiki path (page IDs), interpreted as adjacency, not prerequisites.
	Path []string `json:"path"`
}

type LearningRecommendation struct {
	WikiPageID string `json:"wiki_page_id"`
	KnowledgeBaseID string `json:"knowledge_base_id"`
	Title string `json:"title"`
	Slug string `json:"slug"`
	Status string `json:"status"`
	Score float64 `json:"score"`
	Rank int `json:"rank"`
	Hop int `json:"hop"`
	ReasonCodes []string `json:"reason_codes"`
	SupportingNodes []SupportingKnowledgeNode `json:"supporting_nodes"`
	ScoreComponents RecommendationScoreComponents `json:"score_components"`
}

type LearningRecommendationView struct {
	KnowledgeBaseID string `json:"knowledge_base_id"`
	GeneratedAt time.Time `json:"generated_at"`
	ScoringAt time.Time `json:"scoring_at"`
	WikiEnabled bool `json:"wiki_enabled"`
	Truncated bool `json:"truncated"`
	Recommendations []LearningRecommendation `json:"recommendations"`
	// A bounded projection of existing Wiki nodes/edges lets the UI show supporting
	// paths even when they are outside the degree-ranked overview. No new graph is stored.
	ContextGraph WikiGraphData `json:"context_graph"`
}

// Lightweight, scope-filtered inputs. Neither content nor mapping scores are needed.
type LearningRecommendationSignals struct {
	States []*UserKnowledgeState
	MemorySupportedPageIDs []string
}
