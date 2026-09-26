package httpapi

import (
	"errors"
	"io"
	"net/http"
	"unicode/utf8"

	"github.com/LeoninCS/jobpilot-next/backend/internal/identity"
	"github.com/LeoninCS/jobpilot-next/backend/internal/profile"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const maxProfileDocumentSize = 10 << 20

func profileOverview(service ProfileService) gin.HandlerFunc {
	return func(c *gin.Context) {
		value, err := service.GetOverview(c, identity.UserID(c))
		if err != nil {
			writeProfileError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": gin.H{"profile": value}})
	}
}
func getProfileSettings(service ProfileService) gin.HandlerFunc {
	return func(c *gin.Context) {
		value, err := service.GetSettings(c, identity.UserID(c))
		if err != nil {
			writeProfileError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": gin.H{"settings": value}})
	}
}
func saveProfileSettings(service ProfileService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var input profile.Settings
		if err := c.ShouldBindJSON(&input); err != nil {
			writeError(c, http.StatusBadRequest, "invalid_request", "必要信息格式不正确")
			return
		}
		value, err := service.SaveSettings(c, identity.UserID(c), input)
		if err != nil {
			writeProfileError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": gin.H{"settings": value}})
	}
}
func setProfileCapabilityLevel(service ProfileService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := profileID(c)
		if !ok {
			return
		}
		var input struct {
			Level *int `json:"level"`
		}
		if err := c.ShouldBindJSON(&input); err != nil || input.Level == nil {
			writeError(c, http.StatusBadRequest, "invalid_request", "请选择 L0–L5 等级")
			return
		}
		value, err := service.SetCapabilityLevel(c, identity.UserID(c), id, *input.Level)
		if err != nil {
			writeProfileError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": gin.H{"capability": value}})
	}
}
func listProfileMaterials(service ProfileService) gin.HandlerFunc {
	return func(c *gin.Context) {
		values, err := service.ListMaterials(c, identity.UserID(c))
		if err != nil {
			writeProfileError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": gin.H{"materials": values}})
	}
}
func createProfileMaterial(service ProfileService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var input profile.MaterialInput
		if err := c.ShouldBindJSON(&input); err != nil {
			writeError(c, http.StatusBadRequest, "invalid_request", "材料格式不正确")
			return
		}
		value, err := service.CreateMaterial(c, identity.UserID(c), input)
		if err != nil {
			writeProfileError(c, err)
			return
		}
		c.JSON(http.StatusCreated, gin.H{"data": gin.H{"material": value}})
	}
}

func extractProfileMaterialDocument() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxProfileDocumentSize+(1<<20))
		header, err := c.FormFile("file")
		if err != nil {
			writeError(c, http.StatusBadRequest, "profile_file_required", "请选择要导入的 PDF、DOCX、TXT 或 Markdown 文件")
			return
		}
		if header.Size <= 0 || header.Size > maxProfileDocumentSize {
			writeError(c, http.StatusRequestEntityTooLarge, "profile_file_too_large", "文件大小必须在 10 MB 以内")
			return
		}
		file, err := header.Open()
		if err != nil {
			writeError(c, http.StatusBadRequest, "profile_file_unreadable", "无法读取所选文件")
			return
		}
		defer file.Close()
		data, err := io.ReadAll(io.LimitReader(file, maxProfileDocumentSize+1))
		if err != nil || len(data) > maxProfileDocumentSize {
			writeError(c, http.StatusRequestEntityTooLarge, "profile_file_too_large", "文件大小必须在 10 MB 以内")
			return
		}
		title, text, err := profile.ExtractMaterialDocument(header.Filename, data)
		if err != nil {
			message := "文件文字提取失败；扫描版 PDF 请先进行 OCR，或直接粘贴文字"
			if errors.Is(err, profile.ErrUnsupportedDocument) {
				message = "仅支持 PDF、DOCX、TXT 和 Markdown 文件"
			}
			writeError(c, http.StatusUnprocessableEntity, "profile_file_extract_failed", message)
			return
		}
		if utf8.RuneCountInString(text) > 100000 {
			writeError(c, http.StatusUnprocessableEntity, "profile_file_extract_failed", "文件文字超过 100000 字，请精简后重新导入")
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": gin.H{"filename": header.Filename, "title": title, "text": text}})
	}
}
func updateProfileMaterial(service ProfileService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := profileID(c)
		if !ok {
			return
		}
		var input profile.MaterialInput
		if err := c.ShouldBindJSON(&input); err != nil {
			writeError(c, http.StatusBadRequest, "invalid_request", "材料格式不正确")
			return
		}
		value, err := service.UpdateMaterial(c, identity.UserID(c), id, input)
		if err != nil {
			writeProfileError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": gin.H{"material": value}})
	}
}
func deleteProfileMaterial(service ProfileService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := profileID(c)
		if !ok {
			return
		}
		if err := service.DeleteMaterial(c, identity.UserID(c), id); err != nil {
			writeProfileError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": gin.H{"deleted": true}})
	}
}
func confirmProfileMaterial(service ProfileService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := profileID(c)
		if !ok {
			return
		}
		value, err := service.ConfirmMaterial(c, identity.UserID(c), id)
		if err != nil {
			writeProfileError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": gin.H{"material": value}})
	}
}
func startProfileSession(service ProfileService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var input profile.StartSessionInput
		if err := c.ShouldBindJSON(&input); err != nil {
			writeError(c, http.StatusBadRequest, "invalid_request", "练习设置格式不正确")
			return
		}
		value, err := service.StartSession(c, identity.UserID(c), input)
		if err != nil {
			writeProfileError(c, err)
			return
		}
		c.JSON(http.StatusCreated, gin.H{"data": gin.H{"session": value}})
	}
}
func getProfileSession(service ProfileService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := profileID(c)
		if !ok {
			return
		}
		value, err := service.GetSession(c, identity.UserID(c), id)
		if err != nil {
			writeProfileError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": gin.H{"session": value}})
	}
}
func listProfileSessions(service ProfileService) gin.HandlerFunc {
	return func(c *gin.Context) {
		values, err := service.ListSessions(c, identity.UserID(c))
		if err != nil {
			writeProfileError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": gin.H{"sessions": values}})
	}
}
func confirmProfileSession(service ProfileService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := profileID(c)
		if !ok {
			return
		}
		value, err := service.ConfirmSession(c, identity.UserID(c), id)
		if err != nil {
			writeProfileError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": gin.H{"session": value}})
	}
}
func saveProfileAnswer(service ProfileService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := profileID(c)
		if !ok {
			return
		}
		var input profile.AnswerInput
		if err := c.ShouldBindJSON(&input); err != nil {
			writeError(c, http.StatusBadRequest, "invalid_request", "回答格式不正确")
			return
		}
		value, err := service.SaveAnswer(c, identity.UserID(c), id, input)
		if err != nil {
			writeProfileError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": gin.H{"session": value}})
	}
}
func submitProfileSession(service ProfileService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := profileID(c)
		if !ok {
			return
		}
		var input struct {
			Answers []profile.AnswerInput `json:"answers"`
		}
		if err := c.ShouldBindJSON(&input); err != nil {
			writeError(c, http.StatusBadRequest, "invalid_request", "回答格式不正确")
			return
		}
		value, err := service.SubmitSession(c, identity.UserID(c), id, input.Answers)
		if err != nil {
			writeProfileError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": gin.H{"session": value}})
	}
}

func profileID(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		writeError(c, http.StatusBadRequest, "invalid_profile_id", "编号格式不正确")
		return uuid.Nil, false
	}
	return id, true
}
func writeProfileError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, profile.ErrNotFound):
		writeError(c, http.StatusNotFound, "profile_not_found", "没有找到对应的用户画像数据")
	case errors.Is(err, profile.ErrInvalidInput):
		writeError(c, http.StatusUnprocessableEntity, "invalid_profile_input", err.Error())
	case errors.Is(err, profile.ErrConflict):
		writeError(c, http.StatusConflict, "profile_state_conflict", "数据状态已经变化，请刷新后重试")
	case errors.Is(err, profile.ErrPrecondition):
		writeError(c, http.StatusConflict, "profile_precondition_failed", err.Error())
	case errors.Is(err, profile.ErrModelUnavailable):
		writeError(c, http.StatusServiceUnavailable, "profile_model_unavailable", "模型暂不可用，请检查 DeepSeek 配置")
	default:
		writeError(c, http.StatusInternalServerError, "profile_operation_failed", "用户画像操作失败")
	}
}
