package httpapi

import (
	"errors"
	"net/http"

	"github.com/LeoninCS/jobpilot-next/backend/internal/identity"
	"github.com/LeoninCS/jobpilot-next/backend/internal/knowledgegaps"
	"github.com/LeoninCS/jobpilot-next/backend/internal/target"
	"github.com/gin-gonic/gin"
)

func getKnowledgeGaps(service KnowledgeGapsService) gin.HandlerFunc {
	return func(c *gin.Context) {
		value, err := service.Get(c, identity.UserID(c))
		if err != nil {
			writeGapsError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": value})
	}
}
func analyzeKnowledgeGaps(service KnowledgeGapsService) gin.HandlerFunc {
	return func(c *gin.Context) {
		value, err := service.Analyze(c, identity.UserID(c))
		if errors.Is(err, knowledgegaps.ErrNotReady) {
			c.JSON(http.StatusConflict, gin.H{"error": gin.H{"code": value.Readiness.Code, "message": value.Readiness.Message}, "data": value})
			return
		}
		if err != nil {
			writeGapsError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": value})
	}
}
func writeGapsError(c *gin.Context, err error) {
	if errors.Is(err, target.ErrNotFound) {
		writeError(c, http.StatusConflict, "target_required", "请先设置求职目标")
		return
	}
	writeError(c, http.StatusInternalServerError, "knowledge_gaps_failed", "知识短板暂时无法读取，请稍后重试")
}
