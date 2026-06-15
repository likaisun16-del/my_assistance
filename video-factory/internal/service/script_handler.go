package service

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/AGI-Core/AGI-saber/video-factory/internal/biz"
)

type ScriptHandler struct {
	uc *biz.ScriptUsecase
}

func NewScriptHandler(uc *biz.ScriptUsecase) *ScriptHandler {
	return &ScriptHandler{uc: uc}
}

type createScriptReq struct {
	Title    string `json:"title" binding:"required"`
	Topic    string `json:"topic" binding:"required"`
	Duration int    `json:"duration"`
	Style    string `json:"style"`
}

func (h *ScriptHandler) Create(c *gin.Context) {
	var req createScriptReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Duration == 0 {
		req.Duration = 120
	}
	if req.Style == "" {
		req.Style = "口播"
	}
	uid := c.GetInt64("uid")
	out, err := h.uc.Create(c.Request.Context(), uid, &biz.CreateScriptInput{
		Title:    req.Title,
		Topic:    req.Topic,
		Duration: req.Duration,
		Style:    req.Style,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"task_id":    out.TaskID,
		"project_id": out.ProjectID,
		"status":     out.Status,
		"result":     out.Result,
	})
}
