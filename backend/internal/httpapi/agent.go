package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/LeoninCS/jobpilot-next/backend/internal/agent"
	"github.com/LeoninCS/jobpilot-next/backend/internal/identity"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type AgentService interface {
	List(context.Context, uuid.UUID) ([]agent.Conversation, error)
	Get(context.Context, uuid.UUID, uuid.UUID) (agent.Conversation, error)
	Create(context.Context, uuid.UUID, string) (agent.Conversation, error)
	Delete(context.Context, uuid.UUID, uuid.UUID) error
	Send(context.Context, uuid.UUID, uuid.UUID, string, string, func(agent.Event)) error
	Resolve(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, bool, func(agent.Event)) error
}

func agentID(c *gin.Context, param string) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param(param))
	if err != nil {
		writeError(c, http.StatusBadRequest, "invalid_id", "无效的对话或操作 ID")
		return uuid.Nil, false
	}
	return id, true
}
func agentError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, agent.ErrNotFound):
		writeError(c, http.StatusNotFound, "not_found", "对话或操作不存在")
	case errors.Is(err, agent.ErrBusy):
		writeError(c, http.StatusConflict, "conversation_busy", "当前对话有运行中的请求或待确认操作")
	case errors.Is(err, agent.ErrStale):
		writeError(c, http.StatusConflict, "action_stale", "资料已经变化，请重新提出操作")
	case errors.Is(err, agent.ErrActionClosed):
		writeError(c, http.StatusConflict, "action_closed", "该操作已经处理")
	default:
		handleError(c, err)
	}
}
func listAgentConversations(s AgentService) gin.HandlerFunc {
	return func(c *gin.Context) {
		v, e := s.List(c, identity.UserID(c))
		if e != nil {
			agentError(c, e)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": v})
	}
}
func getAgentConversation(s AgentService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := agentID(c, "id")
		if !ok {
			return
		}
		v, e := s.Get(c, identity.UserID(c), id)
		if e != nil {
			agentError(c, e)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": v})
	}
}
func createAgentConversation(s AgentService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var in struct {
			Page string `json:"page"`
		}
		if e := c.ShouldBindJSON(&in); e != nil {
			writeError(c, http.StatusBadRequest, "invalid_request", "新建对话参数错误")
			return
		}
		v, e := s.Create(c, identity.UserID(c), in.Page)
		if e != nil {
			agentError(c, e)
			return
		}
		c.JSON(http.StatusCreated, gin.H{"data": v})
	}
}
func deleteAgentConversation(s AgentService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := agentID(c, "id")
		if !ok {
			return
		}
		if e := s.Delete(c, identity.UserID(c), id); e != nil {
			agentError(c, e)
			return
		}
		c.Status(http.StatusNoContent)
	}
}
func sendAgentMessage(s AgentService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := agentID(c, "id")
		if !ok {
			return
		}
		var in struct {
			Content string `json:"content"`
			Page    string `json:"page"`
		}
		if e := c.ShouldBindJSON(&in); e != nil || strings.TrimSpace(in.Content) == "" {
			writeError(c, http.StatusBadRequest, "invalid_request", "消息不能为空")
			return
		}
		// A browser may disconnect after receiving a streamed tool event. Keep
		// the bounded run alive so its persisted result is not left half-written.
		runCtx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 4*time.Minute)
		defer cancel()
		userID := identity.UserID(c)
		streamAgent(c, func(emit func(agent.Event)) error {
			return s.Send(runCtx, userID, id, in.Page, in.Content, emit)
		})
	}
}
func resolveAgentAction(s AgentService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := agentID(c, "id")
		if !ok {
			return
		}
		actionID, ok := agentID(c, "action_id")
		if !ok {
			return
		}
		var in struct {
			Approve *bool `json:"approve"`
		}
		if e := c.ShouldBindJSON(&in); e != nil || in.Approve == nil {
			writeError(c, http.StatusBadRequest, "invalid_request", "请明确确认或取消")
			return
		}
		runCtx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 4*time.Minute)
		defer cancel()
		userID := identity.UserID(c)
		streamAgent(c, func(emit func(agent.Event)) error {
			return s.Resolve(runCtx, userID, id, actionID, *in.Approve, emit)
		})
	}
}
func streamAgent(c *gin.Context, run func(func(agent.Event)) error) {
	c.Header("Content-Type", "text/event-stream; charset=utf-8")
	c.Header("Cache-Control", "no-cache")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		writeError(c, http.StatusInternalServerError, "stream_unavailable", "当前连接不支持流式响应")
		return
	}
	var writerMu sync.Mutex
	outputClosed := false
	emit := func(v agent.Event) {
		body, e := json.Marshal(v)
		if e != nil {
			return
		}
		writerMu.Lock()
		defer writerMu.Unlock()
		if outputClosed {
			return
		}
		if _, e = fmt.Fprintf(c.Writer, "data: %s\n\n", body); e != nil {
			outputClosed = true
			return
		}
		flusher.Flush()
	}
	if e := run(emit); e != nil {
		slog.Error("agent stream failed", "error", e)
		emit(agent.Event{Type: "error", Code: "agent_failed", Text: e.Error()})
	}
}
