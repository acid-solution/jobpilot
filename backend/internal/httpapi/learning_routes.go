package httpapi

import (
	"context"
	"errors"
	"github.com/LeoninCS/jobpilot-next/backend/internal/identity"
	"github.com/LeoninCS/jobpilot-next/backend/internal/learningroutes"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"net/http"
	"strings"
)

type LearningRoutesService interface {
	Get(context.Context, uuid.UUID) (learningroutes.View, error)
	Generate(context.Context, uuid.UUID, learningroutes.GenerateInput) (learningroutes.View, error)
	Edit(context.Context, uuid.UUID, learningroutes.EditInput) (learningroutes.View, error)
	Import(context.Context, uuid.UUID, learningroutes.ConfirmInput, string) (learningroutes.View, error)
	RetryImport(context.Context, uuid.UUID, uuid.UUID, string) (learningroutes.View, error)
}

func routeResult(c *gin.Context, v learningroutes.View, err error) {
	if err == nil {
		c.JSON(http.StatusOK, gin.H{"data": v})
		return
	}
	switch {
	case errors.Is(err, learningroutes.ErrInvalid):
		writeError(c, 400, "invalid_learning_route", strings.TrimPrefix(err.Error(), "invalid learning route: "))
	case errors.Is(err, learningroutes.ErrConflict):
		writeError(c, 409, "learning_route_conflict", "路线版本已变化、任务正在运行或导入处理中，请刷新后重试")
	case errors.Is(err, learningroutes.ErrInputsChanged):
		writeError(c, 409, "learning_route_inputs_changed", "画像或知识准备目标已变化，请更新路线后重新确认")
	case errors.Is(err, learningroutes.ErrNotReady):
		writeError(c, 409, "learning_route_not_ready", "请完成画像、选择项目并检查 StudyFlow 导入配置")
	case errors.Is(err, learningroutes.ErrNotFound):
		writeError(c, 404, "learning_route_not_found", "路线或导入请求不存在")
	default:
		writeError(c, 503, "learning_route_unavailable", "学习路线操作暂时失败，请重试")
	}
}
func registerLearningRoutes(api *gin.RouterGroup, s LearningRoutesService) {
	api.GET("/learning-route", func(c *gin.Context) { v, e := s.Get(c.Request.Context(), identity.UserID(c)); routeResult(c, v, e) })
	api.POST("/learning-route/generate", func(c *gin.Context) {
		var in learningroutes.GenerateInput
		if e := c.ShouldBindJSON(&in); e != nil {
			routeResult(c, learningroutes.View{}, learningroutes.ErrInvalid)
			return
		}
		v, e := s.Generate(c.Request.Context(), identity.UserID(c), in)
		routeResult(c, v, e)
	})
	api.PATCH("/learning-route/draft", func(c *gin.Context) {
		var in learningroutes.EditInput
		if e := c.ShouldBindJSON(&in); e != nil {
			routeResult(c, learningroutes.View{}, learningroutes.ErrInvalid)
			return
		}
		v, e := s.Edit(c.Request.Context(), identity.UserID(c), in)
		routeResult(c, v, e)
	})
	api.POST("/learning-route/import", func(c *gin.Context) {
		var in learningroutes.ConfirmInput
		if e := c.ShouldBindJSON(&in); e != nil {
			routeResult(c, learningroutes.View{}, learningroutes.ErrInvalid)
			return
		}
		v, e := s.Import(c.Request.Context(), identity.UserID(c), in, sourceAccess(c))
		routeResult(c, v, e)
	})
	api.POST("/learning-route/imports/:id/retry", func(c *gin.Context) {
		id, e := uuid.Parse(c.Param("id"))
		if e != nil {
			routeResult(c, learningroutes.View{}, learningroutes.ErrInvalid)
			return
		}
		v, e := s.RetryImport(c.Request.Context(), identity.UserID(c), id, sourceAccess(c))
		routeResult(c, v, e)
	})
}
func sourceAccess(c *gin.Context) string {
	parts := strings.Fields(c.GetHeader("Authorization"))
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		return parts[1]
	}
	return ""
}
