package httpapi

import (
	"github.com/LeoninCS/jobpilot-next/backend/internal/identity"
	"github.com/LeoninCS/jobpilot-next/backend/internal/modelusage"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func usageScope(r modelusage.Repository) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request = c.Request.WithContext(modelusage.WithRecorder(c.Request.Context(), r))
		path := c.FullPath()
		purpose := ""
		if c.Request.Method != "GET" {
			switch {
			case strings.Contains(path, "/agent/conversations"):
				purpose = "agent"
			case strings.Contains(path, "/model-configs/deepseek/test"):
				purpose = "connection_test"
			case strings.Contains(path, "/profile/materials") && !strings.HasSuffix(path, "/extract"):
				purpose = "material_analysis"
			}
		}
		if purpose != "" {
			resource, _ := uuid.Parse(c.Param("id"))
			ctx := modelusage.For(modelusage.Resource(c.Request.Context(), uuid.Nil, resource), identity.UserID(c), uuid.New(), purpose, "user")
			c.Request = c.Request.WithContext(ctx)
		}
		c.Next()
	}
}
func usageFilter(c *gin.Context) (modelusage.Filter, bool) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	now := time.Now().In(loc)
	end := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, loc)
	start := end.AddDate(0, 0, -30)
	var err error
	if value := c.Query("from"); value != "" {
		start, err = time.ParseInLocation("2006-01-02", value, loc)
		if err != nil {
			writeError(c, 400, "invalid_filter", "开始日期无效")
			return modelusage.Filter{}, false
		}
	}
	if value := c.Query("to"); value != "" {
		end, err = time.ParseInLocation("2006-01-02", value, loc)
		end = end.AddDate(0, 0, 1)
		if err != nil {
			writeError(c, 400, "invalid_filter", "结束日期无效")
			return modelusage.Filter{}, false
		}
	}
	if !start.Before(end) || end.Sub(start) > 366*24*time.Hour {
		writeError(c, 400, "invalid_filter", "请选择一年以内的有效日期范围")
		return modelusage.Filter{}, false
	}
	f := modelusage.Filter{From: start, To: end, Purpose: c.Query("purpose"), Payer: c.Query("payer"), Page: 1, PageSize: 20}
	if f.Payer != "" && f.Payer != "user" && f.Payer != "platform" {
		writeError(c, 400, "invalid_filter", "费用承担方无效")
		return f, false
	}
	if len(f.Purpose) > 80 {
		writeError(c, 400, "invalid_filter", "功能类型无效")
		return f, false
	}
	if v := c.Query("operation_id"); v != "" {
		f.OperationID, err = uuid.Parse(v)
		if err != nil {
			writeError(c, 400, "invalid_filter", "操作编号无效")
			return f, false
		}
	}
	for _, field := range []string{"page", "page_size"} {
		if v := c.Query(field); v != "" {
			n, e := strconv.Atoi(v)
			if e != nil || n < 1 || n > 10000 || (field == "page_size" && n > 100) {
				writeError(c, 400, "invalid_filter", "分页参数无效")
				return f, false
			}
			if field == "page" {
				f.Page = n
			} else {
				f.PageSize = n
			}
		}
	}
	return f, true
}
func registerModelUsage(api *gin.RouterGroup, r modelusage.Repository) {
	api.GET("/model-usage/summary", func(c *gin.Context) {
		f, ok := usageFilter(c)
		if !ok {
			return
		}
		v, e := r.Summary(c, identity.UserID(c), f)
		if e != nil {
			handleError(c, e)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": v})
	})
	api.GET("/model-usage/calls", func(c *gin.Context) {
		f, ok := usageFilter(c)
		if !ok {
			return
		}
		v, e := r.Calls(c, identity.UserID(c), f)
		if e != nil {
			handleError(c, e)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": v})
	})
	api.GET("/model-usage/operations", func(c *gin.Context) {
		f, ok := usageFilter(c)
		if !ok {
			return
		}
		v, e := r.Operations(c, identity.UserID(c), f)
		if e != nil {
			handleError(c, e)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": v})
	})
}
