package httpapi

import (
	"errors"
	"net/http"

	"github.com/LeoninCS/jobpilot-next/backend/internal/identity"
	"github.com/LeoninCS/jobpilot-next/backend/internal/jdimport"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func importError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, jdimport.ErrNotFound):
		writeError(c, 404, "import_not_found", "找不到这份导入材料")
	case errors.Is(err, jdimport.ErrConflict):
		writeError(c, 409, "import_selection_invalid", "请选择这份材料中有效的岗位候选；更新已有 JD 时只能选择一份")
	default:
		handleError(c, err)
	}
}
func registerJDImports(api *gin.RouterGroup, service *jdimport.Service) {
	api.POST("/jd-imports", func(c *gin.Context) {
		var input struct {
			Raw         string     `json:"raw_text"`
			Replacement *uuid.UUID `json:"replacement_jd_id"`
		}
		if c.ShouldBindJSON(&input) != nil {
			writeError(c, 400, "invalid_request", "请输入待识别的文本")
			return
		}
		out, err := service.Start(c, identity.UserID(c), input.Raw, input.Replacement)
		if err != nil {
			importError(c, err)
			return
		}
		c.JSON(202, gin.H{"data": out})
	})
	api.GET("/jd-imports", func(c *gin.Context) {
		out, err := service.List(c, identity.UserID(c))
		if err != nil {
			importError(c, err)
			return
		}
		c.JSON(200, gin.H{"data": out})
	})
	api.GET("/jd-imports/:id", func(c *gin.Context) {
		id, e := uuid.Parse(c.Param("id"))
		if e != nil {
			writeError(c, 400, "invalid_id", "导入编号无效")
			return
		}
		out, err := service.Get(c, identity.UserID(c), id)
		if err != nil {
			importError(c, err)
			return
		}
		c.JSON(200, gin.H{"data": out})
	})
	api.POST("/jd-imports/:id/confirm", func(c *gin.Context) {
		id, e := uuid.Parse(c.Param("id"))
		var in struct {
			IDs []uuid.UUID `json:"candidate_ids"`
		}
		if e != nil || c.ShouldBindJSON(&in) != nil {
			writeError(c, 400, "invalid_request", "请选择要导入的岗位")
			return
		}
		out, err := service.Confirm(c, identity.UserID(c), id, in.IDs)
		if err != nil {
			importError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": out})
	})
	api.POST("/jd-imports/:id/retry", func(c *gin.Context) {
		id, e := uuid.Parse(c.Param("id"))
		if e != nil {
			writeError(c, 400, "invalid_id", "导入编号无效")
			return
		}
		out, err := service.Retry(c, identity.UserID(c), id)
		if err != nil {
			importError(c, err)
			return
		}
		c.JSON(202, gin.H{"data": out})
	})
}
