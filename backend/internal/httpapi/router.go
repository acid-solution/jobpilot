package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/LeoninCS/jobpilot-next/backend/internal/identity"
	"github.com/LeoninCS/jobpilot-next/backend/internal/jdanalysis"
	"github.com/LeoninCS/jobpilot-next/backend/internal/knowledgegaps"
	"github.com/LeoninCS/jobpilot-next/backend/internal/market"
	"github.com/LeoninCS/jobpilot-next/backend/internal/modelconfig"
	"github.com/LeoninCS/jobpilot-next/backend/internal/mutationlock"
	"github.com/LeoninCS/jobpilot-next/backend/internal/profile"
	"github.com/LeoninCS/jobpilot-next/backend/internal/projectrecs"
	"github.com/LeoninCS/jobpilot-next/backend/internal/target"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type TargetService interface {
	Current(context.Context, uuid.UUID) (target.Target, error)
	UpsertCurrent(context.Context, uuid.UUID, target.UpsertInput) (target.Target, error)
	Catalog(context.Context) ([]target.Category, error)
}

type MarketService interface {
	Submit(context.Context, uuid.UUID, string) (market.JobDescription, error)
	SubmitBatch(context.Context, uuid.UUID, []string) (market.BatchResult, error)
	ListCurrent(context.Context, uuid.UUID, market.ListFilter) ([]market.JobDescription, error)
	Detail(context.Context, uuid.UUID, uuid.UUID) (market.JobDescription, error)
	Update(context.Context, uuid.UUID, uuid.UUID, string) (market.JobDescription, error)
	Delete(context.Context, uuid.UUID, uuid.UUID) error
	Retry(context.Context, uuid.UUID, uuid.UUID) (market.JobDescription, error)
	RetryAbilityReviews(context.Context, uuid.UUID, uuid.UUID) (market.JobDescription, error)
	RetryAbilityGrading(context.Context, uuid.UUID, uuid.UUID) (market.JobDescription, error)
	ProfileCurrent(context.Context, uuid.UUID) (market.Profile, error)
}

type ModelConfigService interface {
	Get(context.Context, uuid.UUID) (modelconfig.Metadata, error)
	Save(context.Context, uuid.UUID, string, string) (modelconfig.Metadata, error)
	Test(context.Context, uuid.UUID, string, string) error
	Delete(context.Context, uuid.UUID) error
}

type ProfileService interface {
	CreateMaterial(context.Context, uuid.UUID, profile.MaterialInput) (profile.Material, error)
	UpdateMaterial(context.Context, uuid.UUID, uuid.UUID, profile.MaterialInput) (profile.Material, error)
	DeleteMaterial(context.Context, uuid.UUID, uuid.UUID) error
	ListMaterials(context.Context, uuid.UUID) ([]profile.Material, error)
	ConfirmMaterial(context.Context, uuid.UUID, uuid.UUID) (profile.Material, error)
	GetOverview(context.Context, uuid.UUID) (profile.Overview, error)
	GetSettings(context.Context, uuid.UUID) (profile.Settings, error)
	SaveSettings(context.Context, uuid.UUID, profile.Settings) (profile.Settings, error)
	SetCapabilityLevel(context.Context, uuid.UUID, uuid.UUID, int) (profile.Capability, error)
	StartSession(context.Context, uuid.UUID, profile.StartSessionInput) (profile.Session, error)
	ListSessions(context.Context, uuid.UUID) ([]profile.Session, error)
	SaveAnswer(context.Context, uuid.UUID, uuid.UUID, profile.AnswerInput) (profile.Session, error)
	GetSession(context.Context, uuid.UUID, uuid.UUID) (profile.Session, error)
	SubmitSession(context.Context, uuid.UUID, uuid.UUID, []profile.AnswerInput) (profile.Session, error)
	ConfirmSession(context.Context, uuid.UUID, uuid.UUID) (profile.Session, error)
}
type KnowledgeGapsService interface {
	Get(context.Context, uuid.UUID) (knowledgegaps.View, error)
	Analyze(context.Context, uuid.UUID) (knowledgegaps.View, error)
}
type ProjectRecommendationsService interface {
	Get(context.Context, uuid.UUID) (projectrecs.View, error)
	Generate(context.Context, uuid.UUID, string) (projectrecs.View, error)
	Select(context.Context, uuid.UUID, uuid.UUID, string) (projectrecs.View, error)
}

type Dependencies struct {
	AgentService                  AgentService
	MutationLocker                mutationlock.Locker
	IdentityResolver              identity.Resolver
	TargetService                 TargetService
	MarketService                 MarketService
	ModelConfigService            ModelConfigService
	ProfileService                ProfileService
	KnowledgeGapsService          KnowledgeGapsService
	ProjectRecommendationsService ProjectRecommendationsService
	Ready                         func() error
}

func NewRouter(dependencies Dependencies) *gin.Engine {
	router := gin.New()
	router.Use(gin.Logger(), gin.Recovery())

	router.GET("/health/live", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	router.GET("/health/ready", func(c *gin.Context) {
		if dependencies.Ready != nil {
			if err := dependencies.Ready(); err != nil {
				writeError(c, http.StatusServiceUnavailable, "not_ready", "数据库暂不可用")
				return
			}
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	api := router.Group("/api/v1")
	api.Use(identity.Middleware(dependencies.IdentityResolver))
	if dependencies.MutationLocker != nil {
		api.Use(serializeUserMutations(dependencies.MutationLocker))
	}
	{
		if dependencies.AgentService != nil {
			api.GET("/agent/conversations", listAgentConversations(dependencies.AgentService))
			api.POST("/agent/conversations", createAgentConversation(dependencies.AgentService))
			api.GET("/agent/conversations/:id", getAgentConversation(dependencies.AgentService))
			api.DELETE("/agent/conversations/:id", deleteAgentConversation(dependencies.AgentService))
			api.POST("/agent/conversations/:id/messages", sendAgentMessage(dependencies.AgentService))
			api.POST("/agent/conversations/:id/actions/:action_id", resolveAgentAction(dependencies.AgentService))
		}
		api.GET("/targets/current", currentTarget(dependencies.TargetService))
		api.PUT("/targets/current", upsertCurrentTarget(dependencies.TargetService))
		api.GET("/catalog/job-directions", jobCatalog(dependencies.TargetService))
		api.POST("/jds", submitJD(dependencies.MarketService))
		api.POST("/jds/batch", submitJDBatch(dependencies.MarketService))
		api.GET("/jds", listJDs(dependencies.MarketService))
		api.GET("/market-profile", marketProfile(dependencies.MarketService))
		api.GET("/jds/:id", jdDetail(dependencies.MarketService))
		api.PUT("/jds/:id", updateJD(dependencies.MarketService))
		api.DELETE("/jds/:id", deleteJD(dependencies.MarketService))
		api.POST("/jds/:id/retry", retryJD(dependencies.MarketService))
		api.POST("/jds/:id/ability-reviews/retry", retryAbilityReviews(dependencies.MarketService))
		api.POST("/jds/:id/ability-grading/retry", retryAbilityGrading(dependencies.MarketService))
		api.GET("/model-configs/deepseek", getDeepSeekConfig(dependencies.ModelConfigService))
		api.PUT("/model-configs/deepseek", saveDeepSeekConfig(dependencies.ModelConfigService))
		api.POST("/model-configs/deepseek/test", testDeepSeekConfig(dependencies.ModelConfigService))
		api.DELETE("/model-configs/deepseek", deleteDeepSeekConfig(dependencies.ModelConfigService))
		if dependencies.ProfileService != nil {
			api.GET("/profile", profileOverview(dependencies.ProfileService))
			api.GET("/profile/settings", getProfileSettings(dependencies.ProfileService))
			api.PUT("/profile/settings", saveProfileSettings(dependencies.ProfileService))
			api.PUT("/profile/capabilities/:id/level", setProfileCapabilityLevel(dependencies.ProfileService))
			api.GET("/profile/materials", listProfileMaterials(dependencies.ProfileService))
			api.POST("/profile/materials/extract", extractProfileMaterialDocument())
			api.POST("/profile/materials", createProfileMaterial(dependencies.ProfileService))
			api.PUT("/profile/materials/:id", updateProfileMaterial(dependencies.ProfileService))
			api.DELETE("/profile/materials/:id", deleteProfileMaterial(dependencies.ProfileService))
			api.POST("/profile/materials/:id/confirm", confirmProfileMaterial(dependencies.ProfileService))
			api.POST("/profile/sessions", startProfileSession(dependencies.ProfileService))
			api.GET("/profile/sessions", listProfileSessions(dependencies.ProfileService))
			api.GET("/profile/sessions/:id", getProfileSession(dependencies.ProfileService))
			api.PUT("/profile/sessions/:id/answers", saveProfileAnswer(dependencies.ProfileService))
			api.POST("/profile/sessions/:id/submit", submitProfileSession(dependencies.ProfileService))
			api.POST("/profile/sessions/:id/confirm", confirmProfileSession(dependencies.ProfileService))
		}
		if dependencies.KnowledgeGapsService != nil {
			api.GET("/knowledge-gaps", getKnowledgeGaps(dependencies.KnowledgeGapsService))
			api.POST("/knowledge-gaps/analyze", analyzeKnowledgeGaps(dependencies.KnowledgeGapsService))
		}
		if dependencies.ProjectRecommendationsService != nil {
			api.GET("/project-recommendations", getProjectRecommendations(dependencies.ProjectRecommendationsService))
			api.POST("/project-recommendations", generateProjectRecommendations(dependencies.ProjectRecommendationsService))
			api.PUT("/project-recommendations/selection", selectProjectRecommendation(dependencies.ProjectRecommendationsService))
		}
	}

	return router
}

func serializeUserMutations(locker mutationlock.Locker) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead || c.Request.Method == http.MethodOptions ||
			strings.HasPrefix(c.Request.URL.Path, "/api/v1/agent/") {
			c.Next()
			return
		}
		release, err := locker.Lock(c.Request.Context(), identity.UserID(c))
		if err != nil {
			writeError(c, http.StatusServiceUnavailable, "mutation_lock_unavailable", "当前操作暂时无法执行，请重试")
			c.Abort()
			return
		}
		defer release()
		c.Next()
	}
}

func submitJDBatch(service MarketService) gin.HandlerFunc {
	type request struct {
		RawTexts []string `json:"raw_texts"`
	}
	return func(c *gin.Context) {
		var input request
		if err := c.ShouldBindJSON(&input); err != nil {
			writeError(c, http.StatusBadRequest, "invalid_request", "批量 JD 格式不正确")
			return
		}
		result, err := service.SubmitBatch(c, identity.UserID(c), input.RawTexts)
		if err != nil {
			handleError(c, err)
			return
		}
		c.JSON(http.StatusAccepted, gin.H{"data": result})
	}
}

func marketProfile(service MarketService) gin.HandlerFunc {
	return func(c *gin.Context) {
		result, err := service.ProfileCurrent(c, identity.UserID(c))
		if err != nil {
			handleError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": result})
	}
}

func jobCatalog(service TargetService) gin.HandlerFunc {
	return func(c *gin.Context) {
		result, err := service.Catalog(c)
		if err != nil {
			handleError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": result})
	}
}

func currentTarget(service TargetService) gin.HandlerFunc {
	return func(c *gin.Context) {
		result, err := service.Current(c, identity.UserID(c))
		if err != nil {
			handleError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": result})
	}
}

func upsertCurrentTarget(service TargetService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var input target.UpsertInput
		if err := c.ShouldBindJSON(&input); err != nil {
			writeError(c, http.StatusBadRequest, "invalid_request", "请求内容不是有效的目标岗位")
			return
		}
		result, err := service.UpsertCurrent(c, identity.UserID(c), input)
		if err != nil {
			handleError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": result})
	}
}

func submitJD(service MarketService) gin.HandlerFunc {
	type request struct {
		RawText string `json:"raw_text"`
	}
	return func(c *gin.Context) {
		var input request
		if err := c.ShouldBindJSON(&input); err != nil {
			writeError(c, http.StatusBadRequest, "invalid_request", "请求内容不是有效的 JD")
			return
		}
		result, err := service.Submit(c, identity.UserID(c), input.RawText)
		if err != nil {
			handleError(c, err)
			return
		}
		c.JSON(http.StatusAccepted, gin.H{"data": result})
	}
}

func listJDs(service MarketService) gin.HandlerFunc {
	return func(c *gin.Context) {
		statuses := make([]market.Status, 0)
		for _, value := range c.QueryArray("status") {
			for _, item := range strings.Split(value, ",") {
				if item = strings.TrimSpace(item); item != "" {
					statuses = append(statuses, market.Status(item))
				}
			}
		}
		result, err := service.ListCurrent(c, identity.UserID(c), market.ListFilter{
			Statuses: statuses,
			Query:    c.Query("q"), Company: c.Query("company"),
			Ability: c.Query("ability"), Category: c.Query("category"),
		})
		if err != nil {
			handleError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": result})
	}
}

func updateJD(service MarketService) gin.HandlerFunc {
	type request struct {
		RawText string `json:"raw_text"`
	}
	return func(c *gin.Context) {
		jdID, err := uuid.Parse(c.Param("id"))
		if err != nil {
			writeError(c, http.StatusBadRequest, "invalid_jd_id", "JD 编号格式不正确")
			return
		}
		var input request
		if err := c.ShouldBindJSON(&input); err != nil {
			writeError(c, http.StatusBadRequest, "invalid_request", "请求内容不是有效的 JD")
			return
		}
		result, err := service.Update(c, identity.UserID(c), jdID, input.RawText)
		if err != nil {
			handleError(c, err)
			return
		}
		c.JSON(http.StatusAccepted, gin.H{"data": result})
	}
}

func deleteJD(service MarketService) gin.HandlerFunc {
	return func(c *gin.Context) {
		jdID, err := uuid.Parse(c.Param("id"))
		if err != nil {
			writeError(c, http.StatusBadRequest, "invalid_jd_id", "JD 编号格式不正确")
			return
		}
		if err := service.Delete(c, identity.UserID(c), jdID); err != nil {
			handleError(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	}
}

func jdDetail(service MarketService) gin.HandlerFunc {
	return func(c *gin.Context) {
		jdID, err := uuid.Parse(c.Param("id"))
		if err != nil {
			writeError(c, http.StatusBadRequest, "invalid_jd_id", "JD 编号格式不正确")
			return
		}
		result, err := service.Detail(c, identity.UserID(c), jdID)
		if err != nil {
			handleError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": result})
	}
}

func retryJD(service MarketService) gin.HandlerFunc {
	return func(c *gin.Context) {
		jdID, err := uuid.Parse(c.Param("id"))
		if err != nil {
			writeError(c, http.StatusBadRequest, "invalid_jd_id", "JD 编号格式不正确")
			return
		}
		result, err := service.Retry(c, identity.UserID(c), jdID)
		if err != nil {
			handleError(c, err)
			return
		}
		c.JSON(http.StatusAccepted, gin.H{"data": result})
	}
}

func retryAbilityReviews(service MarketService) gin.HandlerFunc {
	return func(c *gin.Context) {
		jdID, err := uuid.Parse(c.Param("id"))
		if err != nil {
			writeError(c, http.StatusBadRequest, "invalid_jd_id", "JD 编号格式不正确")
			return
		}
		result, err := service.RetryAbilityReviews(c, identity.UserID(c), jdID)
		if err != nil {
			var quota *market.AbilityReviewQuotaError
			if errors.As(err, &quota) {
				c.JSON(http.StatusTooManyRequests, gin.H{"error": gin.H{"code": "ability_review_quota_exceeded", "message": "今日能力目录审核额度已用完", "next_available_at": quota.NextAvailableAt}})
				return
			}
			handleError(c, err)
			return
		}
		c.JSON(http.StatusAccepted, gin.H{"data": result})
	}
}

func retryAbilityGrading(service MarketService) gin.HandlerFunc {
	return func(c *gin.Context) {
		jdID, err := uuid.Parse(c.Param("id"))
		if err != nil {
			writeError(c, http.StatusBadRequest, "invalid_jd_id", "JD 编号格式不正确")
			return
		}
		result, err := service.RetryAbilityGrading(c, identity.UserID(c), jdID)
		if err != nil {
			handleError(c, err)
			return
		}
		c.JSON(http.StatusAccepted, gin.H{"data": result})
	}
}

type modelConfigRequest struct {
	Model  string `json:"model"`
	APIKey string `json:"api_key"`
}

func getDeepSeekConfig(service ModelConfigService) gin.HandlerFunc {
	return func(c *gin.Context) {
		result, err := service.Get(c, identity.UserID(c))
		if err != nil {
			handleError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": result})
	}
}

func saveDeepSeekConfig(service ModelConfigService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var input modelConfigRequest
		if err := c.ShouldBindJSON(&input); err != nil {
			writeError(c, http.StatusBadRequest, "invalid_request", "模型配置格式不正确")
			return
		}
		result, err := service.Save(c, identity.UserID(c), input.Model, input.APIKey)
		if err != nil {
			handleError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": result})
	}
}

func testDeepSeekConfig(service ModelConfigService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var input modelConfigRequest
		if err := c.ShouldBindJSON(&input); err != nil {
			writeError(c, http.StatusBadRequest, "invalid_request", "模型配置格式不正确")
			return
		}
		if err := service.Test(c, identity.UserID(c), input.Model, input.APIKey); err != nil {
			handleError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": gin.H{"connected": true}})
	}
}

func deleteDeepSeekConfig(service ModelConfigService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := service.Delete(c, identity.UserID(c)); err != nil {
			handleError(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	}
}

func handleError(c *gin.Context, err error) {
	var modelError *jdanalysis.ClassifiedError
	if errors.As(err, &modelError) {
		message := "DeepSeek 暂时无法完成请求，请稍后重试"
		status := http.StatusBadGateway
		if modelError.FailureCode == "model_auth_failed" {
			message = "DeepSeek API Key 无效或没有访问权限"
			status = http.StatusUnprocessableEntity
		}
		writeError(c, status, modelError.FailureCode, message)
		return
	}
	switch {
	case errors.Is(err, market.ErrInvalidJDText):
		writeError(c, http.StatusBadRequest, "invalid_jd_text", "请重新粘贴包含岗位名称、职责和要求的完整 JD")
	case errors.Is(err, target.ErrValidation), errors.Is(err, market.ErrValidation):
		writeError(c, http.StatusBadRequest, "validation_failed", "提交的内容不完整或格式不正确")
	case errors.Is(err, target.ErrNotFound):
		writeError(c, http.StatusConflict, "target_required", "请先设置当前求职目标")
	case errors.Is(err, target.ErrNeedsReselection):
		writeError(c, http.StatusConflict, "target_reselection_required", "原有岗位方向无法完整匹配岗位目录，请重新选择")
	case errors.Is(err, market.ErrNotFound):
		writeError(c, http.StatusNotFound, "jd_not_found", "没有找到这份 JD")
	case errors.Is(err, market.ErrPrecondition):
		writeError(c, http.StatusConflict, "jd_grading_not_retryable", "这份 JD 当前没有可重试的判级任务")
	case errors.Is(err, market.ErrDuplicateJD):
		var duplicate *market.DuplicateError
		if errors.As(err, &duplicate) {
			c.JSON(http.StatusConflict, gin.H{"error": gin.H{"code": "duplicate_jd", "message": "这份 JD 已经导入过", "existing_jd_id": duplicate.ExistingID}})
			return
		}
		writeError(c, http.StatusConflict, "duplicate_jd", "这份 JD 已经导入过")
	case errors.Is(err, modelconfig.ErrValidation):
		writeError(c, http.StatusBadRequest, "invalid_model_config", "DeepSeek 模型或 API Key 格式不正确")
	case errors.Is(err, modelconfig.ErrNotConfigured):
		writeError(c, http.StatusConflict, "model_not_configured", "请先配置 DeepSeek API Key")
	case errors.Is(err, projectrecs.ErrNotReady):
		writeError(c, http.StatusConflict, "recommendation_not_ready", "请先完成市场画像和用户画像")
	case errors.Is(err, projectrecs.ErrConflict):
		writeError(c, http.StatusConflict, "recommendation_conflict", "已有推荐任务正在运行，或报告已经更新")
	case errors.Is(err, projectrecs.ErrNotFound):
		writeError(c, http.StatusNotFound, "recommendation_not_found", "没有找到这个推荐项目")
	case errors.Is(err, projectrecs.ErrInvalid):
		writeError(c, http.StatusBadRequest, "invalid_recommendation_request", "推荐条件或项目选择无效")
	default:
		writeError(c, http.StatusInternalServerError, "internal_error", "服务暂时无法完成请求")
	}
}

func writeError(c *gin.Context, status int, code, message string) {
	c.JSON(status, gin.H{"error": gin.H{"code": code, "message": message}})
}
