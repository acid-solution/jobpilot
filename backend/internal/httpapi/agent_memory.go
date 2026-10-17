package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/LeoninCS/jobpilot-next/backend/internal/agent"
	"github.com/LeoninCS/jobpilot-next/backend/internal/agentcontext"
	"github.com/LeoninCS/jobpilot-next/backend/internal/assistantmemory"
	"github.com/LeoninCS/jobpilot-next/backend/internal/identity"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func memoryError(c *gin.Context, e error) {
	switch {
	case errors.Is(e, assistantmemory.ErrConflict), errors.Is(e, agentcontext.ErrChanged):
		writeError(c, 409, "memory_stale", "记忆、目标或来源已变化，请刷新后重新确认")
	case errors.Is(e, assistantmemory.ErrInvalid):
		writeError(c, 400, "invalid_memory", "偏好类型、内容或来源不符合要求")
	case errors.Is(e, assistantmemory.ErrNotFound):
		writeError(c, 404, "memory_not_found", "记忆或建议不存在")
	default:
		agentError(c, e)
	}
}
func registerAgentMemory(api *gin.RouterGroup, s *agent.Service) {
	api.GET("/agent/memories", func(c *gin.Context) {
		v, e := s.MemorySnapshot(c.Request.Context(), identity.UserID(c))
		if e != nil {
			memoryError(c, e)
			return
		}
		c.JSON(200, gin.H{"data": v})
	})
	save := func(edit bool) gin.HandlerFunc {
		return func(c *gin.Context) {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 12000)
			var w assistantmemory.Write
			if c.ShouldBindJSON(&w) != nil {
				writeError(c, 400, "invalid_memory", "记忆内容无效")
				return
			}
			var id *uuid.UUID
			if edit {
				v, ok := agentID(c, "id")
				if !ok {
					return
				}
				id = &v
			}
			v, e := s.SaveMemory(c.Request.Context(), identity.UserID(c), id, w)
			if e != nil {
				memoryError(c, e)
				return
			}
			c.JSON(200, gin.H{"data": v})
		}
	}
	api.POST("/agent/memories", save(false))
	api.PATCH("/agent/memories/:id", save(true))
	api.DELETE("/agent/memories/:id", func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 12000)
		id, ok := agentID(c, "id")
		if !ok {
			return
		}
		var w assistantmemory.Write
		if c.ShouldBindJSON(&w) != nil {
			writeError(c, 400, "invalid_memory", "请提供记忆版本和内容哈希")
			return
		}
		if e := s.ForgetMemory(c.Request.Context(), identity.UserID(c), id, w); e != nil {
			memoryError(c, e)
			return
		}
		c.Status(204)
	})
	api.GET("/agent/memory-proposals", func(c *gin.Context) {
		v, e := s.MemoryProposals(c.Request.Context(), identity.UserID(c))
		if e != nil {
			memoryError(c, e)
			return
		}
		c.JSON(200, gin.H{"data": v})
	})
	api.POST("/agent/memory-proposals/:id/resolve", func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 12000)
		id, ok := agentID(c, "id")
		if !ok {
			return
		}
		var in struct {
			Value   json.RawMessage `json:"value,omitempty"`
			Approve *bool           `json:"approve"`
			Hash    string          `json:"content_hash"`
		}
		if c.ShouldBindJSON(&in) != nil || in.Approve == nil || in.Hash == "" {
			writeError(c, 400, "invalid_memory", "请明确确认或拒绝并提供建议哈希")
			return
		}
		v, e := s.ResolveMemoryEdited(c.Request.Context(), identity.UserID(c), id, *in.Approve, in.Hash, in.Value)
		if e != nil {
			memoryError(c, e)
			return
		}
		c.JSON(200, gin.H{"data": v})
	})
	api.GET("/agent/conversations/:id/messages", func(c *gin.Context) {
		id, ok := agentID(c, "id")
		if !ok {
			return
		}
		before, e := strconv.ParseInt(c.DefaultQuery("before_sequence", "0"), 10, 64)
		if e != nil || before < 0 {
			writeError(c, 400, "invalid_page", "消息序号无效")
			return
		}
		v, e := s.MessageHistory(c.Request.Context(), identity.UserID(c), id, before)
		if e != nil {
			memoryError(c, e)
			return
		}
		c.JSON(200, gin.H{"data": v})
	})
}
