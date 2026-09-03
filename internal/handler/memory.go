package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/application/service/memory"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// MemoryHandler exposes the caller's own long-term memory.
//
// Every route operates on the memory space derived from the request context,
// so no endpoint takes a subject id. That is deliberate: it removes the entire
// class of "can I read another user's memories by changing an id" bugs instead
// of relying on a per-route ownership check.
type MemoryHandler struct {
	memoryService     interfaces.MemoryService
	memoryWikiService interfaces.MemoryWikiService
	learningProfile   interfaces.LearningProfileService
	learningRecommendation interfaces.LearningRecommendationService
}

func NewMemoryHandler(
	memoryService interfaces.MemoryService,
	memoryWikiService interfaces.MemoryWikiService,
	learningProfile interfaces.LearningProfileService,
	learningRecommendation interfaces.LearningRecommendationService,
) *MemoryHandler {
	return &MemoryHandler{
		memoryService:     memoryService,
		memoryWikiService: memoryWikiService,
		learningProfile:   learningProfile,
		learningRecommendation: learningRecommendation,
	}
}

// GetSettings godoc
// @Summary      获取我的记忆设置
// @Description  返回合并后的记忆开关状态（空间级 + 个人级）与记忆条数
// @Tags         长期记忆
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "记忆设置"
// @Security     Bearer
// @Router       /memory/settings [get]
func (h *MemoryHandler) GetSettings(c *gin.Context) {
	ctx := c.Request.Context()
	settings, err := h.memoryService.GetSettings(ctx)
	if err != nil {
		h.fail(c, err, "Failed to load memory settings")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": settings})
}

type updateMemorySettingsRequest struct {
	Enabled *bool `json:"enabled"`
}

// UpdateSettings godoc
// @Summary      更新我的记忆设置
// @Description  开启或关闭当前用户自己的长期记忆
// @Tags         长期记忆
// @Accept       json
// @Produce      json
// @Param        request  body      object  true  "设置"
// @Success      200      {object}  map[string]interface{}  "更新后的设置"
// @Security     Bearer
// @Router       /memory/settings [put]
func (h *MemoryHandler) UpdateSettings(c *gin.Context) {
	ctx := c.Request.Context()
	var req updateMemorySettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewValidationError("Invalid request data").WithDetails(err.Error()))
		return
	}
	if req.Enabled == nil {
		c.Error(apperrors.NewBadRequestError("enabled is required"))
		return
	}
	if err := h.memoryService.SetEnabled(ctx, *req.Enabled); err != nil {
		h.fail(c, err, "Failed to update memory settings")
		return
	}
	settings, err := h.memoryService.GetSettings(ctx)
	if err != nil {
		h.fail(c, err, "Failed to load memory settings")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": settings})
}

// ListItems godoc
// @Summary      列出我的记忆
// @Description  分页返回当前用户的记忆条目，可按状态过滤
// @Tags         长期记忆
// @Produce      json
// @Param        status  query     string  false  "状态过滤"  Enums(active, superseded, archived, pending)
// @Param        limit   query     int     false  "每页条数"  default(50)
// @Param        offset  query     int     false  "偏移量"
// @Success      200     {object}  map[string]interface{}  "记忆列表"
// @Security     Bearer
// @Router       /memory/items [get]
func (h *MemoryHandler) ListItems(c *gin.Context) {
	ctx := c.Request.Context()
	status := c.Query("status")
	switch status {
	case "", types.MemoryStatusActive, types.MemoryStatusSuperseded,
		types.MemoryStatusArchived, types.MemoryStatusPending:
	default:
		c.Error(apperrors.NewBadRequestError("unsupported status"))
		return
	}
	limit, offset := memoryListPaging(c)

	items, total, err := h.memoryService.ListItems(ctx, status, limit, offset)
	if err != nil {
		h.fail(c, err, "Failed to list memories")
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    items,
		"total":   total,
	})
}

const (
	// memoryExportPageSize is how many rows one export page reads.
	memoryExportPageSize = 500
	// memoryExportMaxItems bounds a single export so one enormous store cannot
	// turn a download into an unbounded read.
	memoryExportMaxItems = 20000
)

func memoryListPaging(c *gin.Context) (limit, offset int) {
	limit, _ = strconv.Atoi(c.DefaultQuery("limit", "50"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset, _ = strconv.Atoi(c.DefaultQuery("offset", "0"))
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

// ListTopics godoc
// @Summary      列出正在观察的主题
// @Description  返回已计数、尚未提升为长期关注的主题，以及距离阈值还差几次
// @Tags         长期记忆
// @Produce      json
// @Param        limit   query     int  false  "每页条数"  default(50)
// @Param        offset  query     int  false  "偏移量"
// @Success      200     {object}  map[string]interface{}  "主题列表"
// @Security     Bearer
// @Router       /memory/topics [get]
func (h *MemoryHandler) ListTopics(c *gin.Context) {
	ctx := c.Request.Context()
	limit, offset := memoryListPaging(c)
	topics, total, err := h.memoryService.ListTopics(ctx, limit, offset)
	if err != nil {
		h.fail(c, err, "Failed to list topics")
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    topics,
		"total":   total,
	})
}

// PromoteTopic godoc
// @Summary      立即记为长期关注
// @Description  不等待剩余次数，把正在观察的主题提升为一条长期关注记忆
// @Tags         长期记忆
// @Produce      json
// @Param        id   path      string  true  "主题 ID"
// @Success      200  {object}  map[string]interface{}  "新增的记忆"
// @Security     Bearer
// @Router       /memory/topics/{id}/promote [post]
func (h *MemoryHandler) PromoteTopic(c *gin.Context) {
	ctx := c.Request.Context()
	item, err := h.memoryService.PromoteTopic(ctx, c.Param("id"))
	if err != nil {
		h.fail(c, err, "Failed to promote topic")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": item})
}

// DeleteTopic godoc
// @Summary      停止跟踪一个主题
// @Description  删除尚未提升的主题计数，并记住这次拒绝，之后不会再自动记为长期关注
// @Tags         长期记忆
// @Produce      json
// @Param        id   path      string  true  "主题 ID"
// @Success      200  {object}  map[string]interface{}  "删除成功"
// @Security     Bearer
// @Router       /memory/topics/{id} [delete]
func (h *MemoryHandler) DeleteTopic(c *gin.Context) {
	ctx := c.Request.Context()
	if err := h.memoryService.DeleteTopic(ctx, c.Param("id")); err != nil {
		h.fail(c, err, "Failed to delete topic")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ListDocuments godoc
// @Summary      列出常用资料
// @Description  返回当前用户回答里反复引用的文档，次数未达习惯门槛的不展示
// @Tags         长期记忆
// @Produce      json
// @Param        limit   query     int  false  "每页条数"  default(50)
// @Param        offset  query     int  false  "偏移量"
// @Success      200     {object}  map[string]interface{}  "文档列表"
// @Security     Bearer
// @Router       /memory/documents [get]
func (h *MemoryHandler) ListDocuments(c *gin.Context) {
	ctx := c.Request.Context()
	limit, offset := memoryListPaging(c)
	docs, total, err := h.memoryService.ListDocuments(ctx, limit, offset)
	if err != nil {
		h.fail(c, err, "Failed to list documents")
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    docs,
		"total":   total,
	})
}

// DeleteDocument godoc
// @Summary      停止用某份文档做个性化检索
// @Description  删除一条文档亲和度计数，之后检索不再因为这份文档而加权
// @Tags         长期记忆
// @Produce      json
// @Param        id   path      string  true  "亲和度 ID"
// @Success      200  {object}  map[string]interface{}  "删除成功"
// @Security     Bearer
// @Router       /memory/documents/{id} [delete]
func (h *MemoryHandler) DeleteDocument(c *gin.Context) {
	ctx := c.Request.Context()
	if err := h.memoryService.DeleteDocument(ctx, c.Param("id")); err != nil {
		h.fail(c, err, "Failed to delete document affinity")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

type createMemoryItemRequest struct {
	Kind       string `json:"kind"`
	Content    string `json:"content"`
	Importance int    `json:"importance"`
}

// CreateItem godoc
// @Summary      新增一条记忆
// @Description  手动添加一条长期记忆
// @Tags         长期记忆
// @Accept       json
// @Produce      json
// @Param        request  body      object  true  "记忆内容"
// @Success      200      {object}  map[string]interface{}  "新增的记忆"
// @Security     Bearer
// @Router       /memory/items [post]
func (h *MemoryHandler) CreateItem(c *gin.Context) {
	ctx := c.Request.Context()
	var req createMemoryItemRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewValidationError("Invalid request data").WithDetails(err.Error()))
		return
	}
	item, err := h.memoryService.CreateItem(ctx, req.Kind, req.Content, req.Importance)
	if err != nil {
		h.fail(c, err, "Failed to create memory")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": item})
}

type updateMemoryItemRequest struct {
	Content    string `json:"content"`
	Importance int    `json:"importance"`
}

// UpdateItem godoc
// @Summary      修改一条记忆
// @Description  修改记忆内容与重要度，修改后该条记忆不会被后台抽取覆盖
// @Tags         长期记忆
// @Accept       json
// @Produce      json
// @Param        id       path      string  true  "记忆ID"
// @Param        request  body      object  true  "记忆内容"
// @Success      200      {object}  map[string]interface{}  "更新后的记忆"
// @Security     Bearer
// @Router       /memory/items/{id} [put]
func (h *MemoryHandler) UpdateItem(c *gin.Context) {
	ctx := c.Request.Context()
	var req updateMemoryItemRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewValidationError("Invalid request data").WithDetails(err.Error()))
		return
	}
	item, err := h.memoryService.UpdateItem(ctx, c.Param("id"), req.Content, req.Importance)
	if err != nil {
		h.fail(c, err, "Failed to update memory")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": item})
}

// DeleteItem godoc
// @Summary      删除一条记忆
// @Description  永久删除一条记忆
// @Tags         长期记忆
// @Produce      json
// @Param        id  path      string  true  "记忆ID"
// @Success      200  {object}  map[string]interface{}  "删除成功"
// @Security     Bearer
// @Router       /memory/items/{id} [delete]
func (h *MemoryHandler) DeleteItem(c *gin.Context) {
	ctx := c.Request.Context()
	if err := h.memoryService.DeleteItem(ctx, c.Param("id")); err != nil {
		h.fail(c, err, "Failed to delete memory")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ConfirmItem godoc
// @Summary      确认一条推断出的记忆
// @Description  接受系统推断的记忆，使其开始生效
// @Tags         长期记忆
// @Produce      json
// @Param        id   path      string  true  "记忆 ID"
// @Success      200  {object}  map[string]interface{}  "确认成功"
// @Security     Bearer
// @Router       /memory/items/{id}/confirm [post]
//
// Inferred memories are the ones worth having and the ones most likely to be
// wrong, so they wait here rather than taking effect silently.
func (h *MemoryHandler) ConfirmItem(c *gin.Context) {
	ctx := c.Request.Context()
	item, err := h.memoryService.ConfirmItem(ctx, c.Param("id"))
	if err != nil {
		h.fail(c, err, "Failed to confirm memory")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": item})
}

// RejectItem godoc
// @Summary      否决一条推断出的记忆
// @Description  拒绝系统推断的记忆，并记住这次拒绝
// @Tags         长期记忆
// @Produce      json
// @Param        id   path      string  true  "记忆 ID"
// @Success      200  {object}  map[string]interface{}  "否决成功"
// @Security     Bearer
// @Router       /memory/items/{id}/reject [post]
func (h *MemoryHandler) RejectItem(c *gin.Context) {
	ctx := c.Request.Context()
	if err := h.memoryService.RejectItem(ctx, c.Param("id")); err != nil {
		h.fail(c, err, "Failed to reject memory")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// Clear godoc
// @Summary      清空我的记忆
// @Description  永久删除当前用户的全部记忆
// @Tags         长期记忆
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "清空成功"
// @Security     Bearer
// @Router       /memory/items [delete]
func (h *MemoryHandler) Clear(c *gin.Context) {
	ctx := c.Request.Context()
	removed, err := h.memoryService.Clear(ctx)
	if err != nil {
		h.fail(c, err, "Failed to clear memories")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "removed": removed})
}

// Export godoc
// @Summary      导出我的记忆
// @Description  以 JSON 导出当前用户的全部记忆
// @Tags         长期记忆
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "记忆导出"
// @Security     Bearer
// @Router       /memory/export [get]
func (h *MemoryHandler) Export(c *gin.Context) {
	ctx := c.Request.Context()
	// Export is a snapshot, not a page, so it walks every status to the end.
	//
	// A single fixed page used to serve this on the grounds that it matched the
	// largest capacity a workspace can configure. It does not: max_items caps
	// active memories only, while superseded and archived rows accumulate
	// without limit, so a long-lived store holds far more than its capacity and
	// the export quietly returned a prefix of it.
	var items []*types.MemoryItem
	var total int64
	for {
		page, pageTotal, err := h.memoryService.ListItems(ctx, "", memoryExportPageSize, len(items))
		if err != nil {
			h.fail(c, err, "Failed to export memories")
			return
		}
		total = pageTotal
		items = append(items, page...)
		if len(page) < memoryExportPageSize || int64(len(items)) >= total {
			break
		}
		if len(items) >= memoryExportMaxItems {
			break
		}
	}
	c.Header("Content-Disposition", `attachment; filename="weknora-memories.json"`)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"total":   total,
		// Say so rather than letting a partial file look complete. Only the
		// safety ceiling can trigger this, so it stays false in practice.
		"truncated": int64(len(items)) < total,
		"data":      items,
	})
}

// Consolidate godoc
// @Summary      立刻整理我的记忆
// @Description  合并意思接近的条目、归档到期事项，不等待每日后台整理
// @Tags         长期记忆
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "整理结果"
// @Security     Bearer
// @Router       /memory/consolidate [post]
func (h *MemoryHandler) Consolidate(c *gin.Context) {
	ctx := c.Request.Context()
	result, err := h.memoryService.ConsolidateNow(ctx)
	if err != nil {
		h.fail(c, err, "Failed to consolidate memories")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}

type memoryWikiCandidatesRequest struct {
	KnowledgeBaseID string `json:"knowledge_base_id"`
	TopK            int    `json:"top_k"`
}

// FindWikiCandidates returns Wiki pages supported by semantic KB retrieval
// evidence for one of the caller's memories.
func (h *MemoryHandler) FindWikiCandidates(c *gin.Context) {
	var req memoryWikiCandidatesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewValidationError("Invalid request data").WithDetails(err.Error()))
		return
	}
	if req.KnowledgeBaseID == "" {
		c.Error(apperrors.NewBadRequestError("knowledge_base_id is required"))
		return
	}
	candidates, err := h.memoryWikiService.FindCandidates(
		c.Request.Context(), c.Param("id"), req.KnowledgeBaseID, req.TopK,
	)
	if err != nil {
		h.fail(c, err, "Failed to find Wiki candidates")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": candidates})
}

type upsertMemoryWikiLinkRequest struct {
	WikiPageID string  `json:"wiki_page_id"`
	Score      float64 `json:"score"`
	Method     string  `json:"method"`
}

// UpsertWikiLink confirms and persists one MemoryItem-to-WikiPage relation.
func (h *MemoryHandler) UpsertWikiLink(c *gin.Context) {
	var req upsertMemoryWikiLinkRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewValidationError("Invalid request data").WithDetails(err.Error()))
		return
	}
	if req.WikiPageID == "" {
		c.Error(apperrors.NewBadRequestError("wiki_page_id is required"))
		return
	}
	view, err := h.memoryWikiService.UpsertLink(
		c.Request.Context(), c.Param("id"), req.WikiPageID, req.Score, req.Method,
	)
	if err != nil {
		h.fail(c, err, "Failed to save Memory-Wiki link")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": view})
}

// ListWikiLinks returns every confirmed relation for the current principal in
// the current tenant. Neither scope component is accepted from the client.
func (h *MemoryHandler) ListWikiLinks(c *gin.Context) {
	views, err := h.memoryWikiService.ListLinks(c.Request.Context())
	if err != nil {
		h.fail(c, err, "Failed to list Memory-Wiki links")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": views})
}

// DeleteWikiLink removes one relation inside the caller's resolved scope.
func (h *MemoryHandler) DeleteWikiLink(c *gin.Context) {
	if err := h.memoryWikiService.DeleteLink(c.Request.Context(), c.Param("id")); err != nil {
		h.fail(c, err, "Failed to delete Memory-Wiki link")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ListLearningEvidence returns auditable evidence for the authenticated
// principal. An optional WikiPage filter never changes the caller's scope.
func (h *MemoryHandler) ListLearningEvidence(c *gin.Context) {
	wikiPageID, present := c.GetQuery("wiki_page_id")
	if present && strings.TrimSpace(wikiPageID) == "" {
		c.Error(apperrors.NewBadRequestError("wiki_page_id cannot be empty"))
		return
	}
	evidence, err := h.learningProfile.ListEvidence(
		c.Request.Context(), strings.TrimSpace(wikiPageID),
	)
	if err != nil {
		h.fail(c, err, "Failed to list learning evidence")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": evidence})
}

// ListKnowledgeStates returns only materialized states; WikiPages without
// evidence remain implicit unknowns and are not expanded into rows.
func (h *MemoryHandler) ListKnowledgeStates(c *gin.Context) {
	knowledgeBaseID, present := c.GetQuery("knowledge_base_id")
	if present && strings.TrimSpace(knowledgeBaseID) == "" {
		c.Error(apperrors.NewBadRequestError("knowledge_base_id cannot be empty"))
		return
	}
	states, err := h.learningProfile.ListKnowledgeStates(
		c.Request.Context(), strings.TrimSpace(knowledgeBaseID),
	)
	if err != nil {
		h.fail(c, err, "Failed to list knowledge states")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": states})
}

// fail maps service errors onto HTTP responses. A missing item and an item
// belonging to someone else produce the same 404 on purpose.
func (h *MemoryHandler) fail(c *gin.Context, err error, message string) {
	switch {
	case errors.Is(err, memory.ErrNoMemoryScope):
		c.Error(apperrors.NewUnauthorizedError("no principal in request"))
	case errors.Is(err, memory.ErrItemNotFound):
		c.Error(apperrors.NewNotFoundError("memory not found"))
	case errors.Is(err, memory.ErrMemoryWikiKnowledgeBaseNotFound):
		c.Error(apperrors.NewNotFoundError("knowledge base not found"))
	case errors.Is(err, memory.ErrMemoryWikiPageNotFound):
		c.Error(apperrors.NewNotFoundError("wiki page not found"))
	case errors.Is(err, memory.ErrMemoryWikiLinkNotFound):
		c.Error(apperrors.NewNotFoundError("memory wiki link not found"))
	case errors.Is(err, memory.ErrMemoryWikiDisabled):
		c.Error(apperrors.NewBadRequestError("wiki is not enabled for knowledge base"))
	case errors.Is(err, memory.ErrMemoryDisabled):
		c.Error(apperrors.NewBadRequestError("memory is disabled"))
	default:
		logger.ErrorWithFields(c.Request.Context(), err, nil)
		c.Error(apperrors.NewInternalServerError(message).WithDetails(err.Error()))
	}
}
