package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/LeoninCS/jobpilot-next/backend/internal/market"
	"github.com/LeoninCS/jobpilot-next/backend/internal/modelconfig"
	"github.com/LeoninCS/jobpilot-next/backend/internal/target"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/google/uuid"
)

// These tests run the real Eino Send -> interrupt -> Resolve -> resumed tool
// chain. Only storage, business services and the model provider are replaced.
func TestWriteToolExecutesOnlyAfterEinoResume(t *testing.T) {
	for _, tc := range []struct {
		name              string
		approve           bool
		stale             bool
		missingCheckpoint bool
		claimLost         bool
		businessErr       error
		status            string
		writes            int
		wantError         string
		toolReply         string
		errorCode         string
	}{
		{name: "approve", approve: true, status: "succeeded", writes: 1, toolReply: "操作成功"},
		{name: "cancel", status: "cancelled", toolReply: "用户取消"},
		{name: "stale", approve: true, stale: true, status: "stale", wantError: ErrStale.Error(), errorCode: "action_stale"},
		{name: "business failure", approve: true, businessErr: errors.New("business rejected"), status: "failed", writes: 1, toolReply: "状态：failed", errorCode: "business_operation_failed"},
		{name: "checkpoint missing", approve: true, missingCheckpoint: true, status: "pending", wantError: "checkpoint"},
		{name: "claim lost", approve: true, claimLost: true, status: "pending", wantError: ErrActionClosed.Error()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			user, conversation := uuid.New(), uuid.New()
			repo := &confirmationRepository{user: user, conversation: Conversation{ID: conversation, Status: "idle"}, claimLost: tc.claimLost}
			targets := &confirmationTargets{value: target.Target{ID: uuid.New(), Title: "后端开发", EmploymentType: "internship"}}
			business := &confirmationMarket{err: tc.businessErr}
			checkpoints := &memoryCheckpoint{values: map[string][]byte{}}
			var requests atomic.Int32
			var replyMu sync.Mutex
			var replies []string
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Messages []struct {
						Role    string `json:"role"`
						Content string `json:"content"`
					} `json:"messages"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Errorf("decode model request: %v", err)
				}
				replyMu.Lock()
				for _, message := range request.Messages {
					if message.Role == "tool" {
						replies = append(replies, message.Content)
					}
				}
				replyMu.Unlock()
				var delta any = map[string]any{"role": "assistant", "content": "操作结果已收到"}
				finish := "stop"
				if requests.Add(1) == 1 {
					delta = map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{
						"index": 0, "id": "write1", "type": "function", "function": map[string]any{
							"name": "propose_jobpilot_change", "arguments": `{"kind":"jd_add","arguments":{"raw_text":"测试公司招聘 Go 后端开发，负责接口设计，要求熟悉 Go。"}}`,
						},
					}}}
					finish = "tool_calls"
				}
				w.Header().Set("Content-Type", "text/event-stream")
				body, _ := json.Marshal(map[string]any{"id": "test", "object": "chat.completion.chunk", "created": 1, "model": "deepseek-chat", "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
				_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", body)
			}))
			defer provider.Close()
			service := &Service{Repo: repo, Targets: targets, Market: business, Credentials: confirmationCredentials{}, Checkpoints: checkpoints, DeepSeekBaseURL: provider.URL}
			var events []Event
			emit := func(event Event) { events = append(events, event) }
			if err := service.Send(ctx, user, conversation, "market", "保存这份 JD", emit); err != nil {
				t.Fatal(err)
			}
			if repo.action.Status != "pending" || repo.action.InterruptID == "" || business.writes != 0 || requests.Load() != 1 {
				t.Fatalf("initial call must only interrupt: action=%+v writes=%d requests=%d", repo.action, business.writes, requests.Load())
			}
			// Keep an old checkpoint to verify that replaying it cannot repeat a write.
			checkpoints.mu.Lock()
			oldCheckpoints := make(map[string][]byte, len(checkpoints.values))
			for key, value := range checkpoints.values {
				oldCheckpoints[key] = append([]byte(nil), value...)
			}
			checkpoints.mu.Unlock()
			if tc.stale {
				// Same goal scope, but the snapshot shown on the card is now outdated.
				targets.value.Title = "资料已修改"
			}
			if tc.missingCheckpoint {
				if err := checkpoints.Delete(ctx, "agent-"+conversation.String()); err != nil {
					t.Fatal(err)
				}
			}
			// Resolve constructs a new runner, like a fresh HTTP request or restart.
			err := service.Resolve(ctx, user, conversation, repo.action.ID, tc.approve, emit)
			if tc.wantError == "" && err != nil {
				t.Fatal(err)
			}
			if tc.wantError != "" && (err == nil || !strings.Contains(strings.ToLower(err.Error()), tc.wantError)) {
				t.Fatalf("expected error containing %q, got %v", tc.wantError, err)
			}
			if repo.action.Status != tc.status || business.writes != tc.writes {
				t.Fatalf("status=%s writes=%d, expected %s/%d", repo.action.Status, business.writes, tc.status, tc.writes)
			}
			if business.writes > 0 && !business.resumedWithApproval {
				t.Fatal("business write ran outside the resumed Eino tool")
			}
			replyMu.Lock()
			toolText := strings.Join(replies, "\n")
			replyMu.Unlock()
			if tc.toolReply != "" && !strings.Contains(toolText, tc.toolReply) {
				t.Fatalf("model did not receive the actual action outcome: %q", toolText)
			}
			if tc.status != "succeeded" && strings.Contains(toolText, "操作成功") {
				t.Fatal("failed or cancelled action was reported to the model as success")
			}
			if tc.errorCode != "" {
				found := false
				for _, event := range events {
					found = found || event.Code == tc.errorCode
				}
				if !found {
					t.Fatalf("missing error event %s", tc.errorCode)
				}
			}
			if tc.status == "succeeded" || tc.status == "cancelled" {
				if err := service.Resolve(ctx, user, conversation, repo.action.ID, true, emit); err == nil {
					t.Fatal("duplicate confirmation was accepted")
				}
				checkpoints.mu.Lock()
				checkpoints.values = oldCheckpoints
				checkpoints.mu.Unlock()
				runner, err := service.runner(ctx, user, conversation, repo.scope, "")
				if err != nil {
					t.Fatal(err)
				}
				iter, err := runner.ResumeWithParams(ctx, "agent-"+conversation.String(), &adk.ResumeParams{Targets: map[string]any{repo.action.InterruptID: true}})
				if err != nil {
					t.Fatal(err)
				}
				if err := service.consume(ctx, user, conversation, repo.scope, uuid.Nil, iter, emit); err != nil {
					t.Fatal(err)
				}
				if business.writes != tc.writes || repo.action.Status != tc.status {
					t.Fatal("replaying an old checkpoint repeated a write or reopened a cancelled action")
				}
			}
		})
	}
}

type confirmationTargets struct {
	TargetService
	value target.Target
}

func (s *confirmationTargets) Current(context.Context, uuid.UUID) (target.Target, error) {
	return s.value, nil
}

type confirmationCredentials struct{}

func (confirmationCredentials) Credentials(context.Context, uuid.UUID) (modelconfig.Credentials, error) {
	return modelconfig.Credentials{APIKey: "test-key", Model: "deepseek-chat"}, nil
}

type confirmationMarket struct {
	MarketService
	writes              int
	resumedWithApproval bool
	err                 error
}

func (s *confirmationMarket) Submit(ctx context.Context, _ uuid.UUID, text string) (market.JobDescription, error) {
	s.writes++
	interrupted, hasState, _ := tool.GetInterruptState[string](ctx)
	isTarget, hasDecision, approved := tool.GetResumeContext[bool](ctx)
	s.resumedWithApproval = interrupted && hasState && isTarget && hasDecision && approved
	return market.JobDescription{ID: uuid.New(), RawText: text}, s.err
}

type confirmationRepository struct {
	Repository
	user         uuid.UUID
	scope        string
	conversation Conversation
	action       Action
	claimLost    bool
}

func (r *confirmationRepository) Get(_ context.Context, user uuid.UUID, scope string, id uuid.UUID) (Conversation, error) {
	if user != r.user || id != r.conversation.ID || (r.scope != "" && scope != r.scope) {
		return Conversation{}, ErrNotFound
	}
	v := r.conversation
	v.PendingAction = nil
	if r.action.Status == "pending" {
		a := r.action
		v.PendingAction = &a
	}
	return v, nil
}

func (r *confirmationRepository) BeginRun(_ context.Context, _ uuid.UUID, scope string, _ uuid.UUID) (uuid.UUID, error) {
	r.scope = scope
	r.conversation.Status = "running"
	return uuid.New(), nil
}

func (r *confirmationRepository) BeginResume(context.Context, uuid.UUID, string, uuid.UUID) (uuid.UUID, error) {
	if r.conversation.Status != "awaiting_confirmation" {
		return uuid.Nil, ErrBusy
	}
	r.conversation.Status = "running"
	return uuid.New(), nil
}

func (r *confirmationRepository) EndRun(context.Context, uuid.UUID, uuid.UUID, bool) error {
	r.conversation.Status = "idle"
	if r.action.Status == "pending" {
		r.conversation.Status = "awaiting_confirmation"
	}
	return nil
}

func (r *confirmationRepository) AddMessage(_ context.Context, _ uuid.UUID, role, content string) (Message, error) {
	return Message{Role: role, Content: content}, nil
}

func (r *confirmationRepository) CreateAction(_ context.Context, _, _ uuid.UUID, scope, kind string, arguments json.RawMessage, summary, expected string) (Action, error) {
	r.action = Action{ID: uuid.New(), Kind: kind, Arguments: arguments, Summary: summary, ExpectedHash: expected, Status: "pending"}
	return r.action, nil
}

func (r *confirmationRepository) SetInterrupt(_ context.Context, _, _ uuid.UUID, interrupt string) error {
	r.action.InterruptID = interrupt
	return nil
}

func (r *confirmationRepository) GetAction(_ context.Context, user uuid.UUID, scope string, id uuid.UUID) (Action, error) {
	if user != r.user || scope != r.scope || id != r.action.ID {
		return Action{}, ErrNotFound
	}
	return r.action, nil
}

func (r *confirmationRepository) ClaimAction(_ context.Context, id, conversation uuid.UUID) (bool, error) {
	if r.claimLost || r.action.ID != id || r.conversation.ID != conversation || r.action.Status != "pending" {
		return false, nil
	}
	r.action.Status = "executing"
	return true, nil
}

func (r *confirmationRepository) FinishAction(_ context.Context, _ uuid.UUID, status string, _ json.RawMessage, _ string) error {
	r.action.Status = status
	return nil
}

func (r *confirmationRepository) CancelAction(context.Context, uuid.UUID) error {
	r.action.Status = "cancelled"
	return nil
}

func (r *confirmationRepository) StaleAction(context.Context, uuid.UUID) error {
	r.action.Status = "stale"
	return nil
}
