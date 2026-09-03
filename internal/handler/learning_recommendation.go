package handler

import (
	"net/http"
	"strconv"
	"strings"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

// ListLearningRecommendations derives scope exclusively from authenticated context.
func (h *MemoryHandler) ListLearningRecommendations(c *gin.Context) {
	kbID := strings.TrimSpace(c.Query("knowledge_base_id"))
	if kbID == "" { c.Error(apperrors.NewBadRequestError("knowledge_base_id is required")); return }
	limit := types.LearningRecommendationDefaultLimit
	if values, present := c.Request.URL.Query()["limit"]; present {
		if len(values) != 1 { c.Error(apperrors.NewBadRequestError("limit must be a single integer")); return }
		value, err := strconv.Atoi(values[0])
		if err != nil || value < 1 || value > types.LearningRecommendationMaxLimit {
			c.Error(apperrors.NewBadRequestError("limit must be between 1 and 20")); return
		}
		limit = value
	}
	view, err := h.learningRecommendation.ListRecommendations(c.Request.Context(), kbID, limit)
	if err != nil { h.fail(c, err, "Failed to list learning recommendations"); return }
	c.JSON(http.StatusOK, gin.H{"success": true, "data": view})
}
