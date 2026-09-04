package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/application/service/memory"
)

func (h *MemoryHandler) ExportLearningProfile(c *gin.Context) {
	ctx := c.Request.Context()
	if _, err := memory.ResolveScope(ctx); err != nil {
		h.fail(c, err, "Failed to export learning profile")
		return
	}
	data, err := h.learningProfile.ExportProfile(ctx)
	if err != nil {
		h.fail(c, err, "Failed to export learning profile")
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Header("Content-Disposition", `attachment; filename="weknora-learning-profile.json"`)
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}

func (h *MemoryHandler) ClearLearningProfile(c *gin.Context) {
	ctx := c.Request.Context()
	if _, err := memory.ResolveScope(ctx); err != nil {
		h.fail(c, err, "Failed to delete learning profile")
		return
	}
	data, err := h.learningProfile.ClearProfile(ctx)
	if err != nil {
		h.fail(c, err, "Failed to delete learning profile")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}
