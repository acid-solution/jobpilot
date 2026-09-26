package httpapi

import (
	"net/http"

	"github.com/LeoninCS/jobpilot-next/backend/internal/identity"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func getProjectRecommendations(service ProjectRecommendationsService) gin.HandlerFunc {
	return func(c *gin.Context) {
		v, err := service.Get(c, identity.UserID(c))
		if err != nil {
			handleError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": v})
	}
}
func generateProjectRecommendations(service ProjectRecommendationsService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var body struct {
			Adjustment string `json:"adjustment"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			writeError(c, http.StatusBadRequest, "invalid_request", "请求格式不正确")
			return
		}
		v, err := service.Generate(c, identity.UserID(c), body.Adjustment)
		if err != nil {
			handleError(c, err)
			return
		}
		c.JSON(http.StatusAccepted, gin.H{"data": v})
	}
}
func selectProjectRecommendation(service ProjectRecommendationsService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var body struct {
			ReportID  string `json:"report_id"`
			ProjectID string `json:"project_id"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			writeError(c, http.StatusBadRequest, "invalid_request", "请求格式不正确")
			return
		}
		id, err := uuid.Parse(body.ReportID)
		if err != nil || body.ProjectID == "" {
			writeError(c, http.StatusBadRequest, "invalid_request", "报告或项目编号不正确")
			return
		}
		v, err := service.Select(c, identity.UserID(c), id, body.ProjectID)
		if err != nil {
			handleError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": v})
	}
}
