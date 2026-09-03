package interfaces

import (
	"context"
	"github.com/Tencent/WeKnora/internal/types"
)

type LearningRecommendationService interface {
	ListRecommendations(ctx context.Context, knowledgeBaseID string, limit int) (*types.LearningRecommendationView, error)
}
