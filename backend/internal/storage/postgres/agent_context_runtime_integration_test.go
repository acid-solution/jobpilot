package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/LeoninCS/jobpilot-next/backend/internal/agent"
	"github.com/LeoninCS/jobpilot-next/backend/internal/agentcontext"
	"github.com/LeoninCS/jobpilot-next/backend/internal/assistantmemory"
	"github.com/LeoninCS/jobpilot-next/backend/internal/modelusage"
	"github.com/LeoninCS/jobpilot-next/backend/internal/target"
	"github.com/google/uuid"
)

func TestAgentContextRuntimeMemoryToolAndUsage(t *testing.T) {
	db := agentTransactionDatabase(t)
	repo := NewAgentRepository(db)
	mem := NewAssistantMemoryRepository(db)
	user := uuid.New()
	var requests atomic.Int64
	var inherited atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Messages []struct{ Role, Content string } `json:"messages"`
		}
		if e := json.NewDecoder(r.Body).Decode(&payload); e != nil {
			t.Error(e)
		}
		n := requests.Add(1)
		if n >= 3 {
			for _, m := range payload.Messages {
				if strings.Contains(m.Content, "confirmed_preferences") && strings.Contains(m.Content, "project.languages") && strings.Contains(m.Content, "Go") {
					inherited.Store(true)
				}
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		var delta any = map[string]any{"role": "assistant", "content": "偏好与当前资料已核对。"}
		if n == 1 {
			args, _ := json.Marshal(map[string]any{"key": "project.languages", "value": []string{"Go"}, "source_quote": "以后项目优先 Go"})
			delta = map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": 0, "id": "memory_call_1", "type": "function", "function": map[string]any{"name": "propose_memory", "arguments": string(args)}}}}
		}
		chunk, _ := json.Marshal(map[string]any{"id": fmt.Sprint("test-", n), "object": "chat.completion.chunk", "model": "deepseek-chat", "choices": []any{map[string]any{"index": 0, "delta": delta}}})
		fmt.Fprintf(w, "data: %s\n\n", chunk)
		fmt.Fprintf(w, "data: {\"id\":\"test-%d\",\"model\":\"deepseek-chat\",\"choices\":[],\"usage\":{\"prompt_tokens\":45,\"completion_tokens\":6,\"total_tokens\":51,\"prompt_cache_hit_tokens\":12,\"prompt_cache_miss_tokens\":33}}\n\ndata: [DONE]\n\n", n)
	}))
	defer server.Close()
	usage := NewModelUsageRepository(db)
	s := &agent.Service{Repo: repo, ContextRepo: NewAgentContextRepository(db), Memories: mem, ContextConfig: agentcontext.DefaultConfig(), Transactions: repo, Checkpoints: NewAgentCheckpointStore(db), Targets: target.NewService(NewTargetRepository(db)), Credentials: transactionTestCredentials{}, DeepSeekBaseURL: server.URL, ModelHTTP: &http.Client{Transport: &modelusage.Transport{Repo: usage, Provider: "deepseek"}}}
	conv, e := s.Create(context.Background(), user, "dashboard")
	if e != nil {
		t.Fatal(e)
	}
	ctx := modelusage.For(context.Background(), user, uuid.New(), "agent", "user")
	if e = s.Send(ctx, user, conv.ID, "dashboard", "以后项目优先 Go", func(agent.Event) {}); e != nil {
		t.Fatal(e)
	}
	pending, e := mem.Proposals(ctx, user, "no-target")
	if e != nil || len(pending) != 1 {
		t.Fatal("proposal missing", pending, e)
	}
	visible, e := s.Get(ctx, user, conv.ID)
	if e != nil || visible.Status != "idle" || visible.PendingAction != nil {
		t.Fatal("memory proposal blocked conversation", visible, e)
	}
	edited, e := mem.ResolveEdited(ctx, user, "no-target", pending[0].ID, true, pending[0].ContentHash, json.RawMessage(`["Go","Python"]`))
	if e != nil || edited.Result == nil {
		t.Fatal(e)
	}
	saved, e := mem.Snapshot(ctx, user, "no-target")
	if e != nil || len(saved.Memories) != 1 || !strings.Contains(string(saved.Memories[0].Value), "Python") {
		t.Fatal("edited preference not saved", e)
	}
	next, e := s.Create(ctx, user, "dashboard")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Send(modelusage.For(ctx, user, uuid.New(), "agent", "user"), user, next.ID, "dashboard", "按我的偏好给个建议", func(agent.Event) {}); e != nil {
		t.Fatal(e)
	}
	if !inherited.Load() {
		t.Fatal("new conversation did not load confirmed memory")
	}
	var calls, input, cache int
	if e = db.QueryRow(`SELECT count(*),COALESCE(sum(input_tokens),0),COALESCE(sum(cache_hit_tokens),0) FROM model_usage_calls WHERE purpose='agent'`).Scan(&calls, &input, &cache); e != nil {
		t.Fatal(e)
	}
	if calls != 3 || input != 135 || cache != 36 {
		t.Fatalf("usage lost or doubled: calls=%d input=%d cache=%d", calls, input, cache)
	}
	// Changing a preference is a new generation dependency, not a report rewrite.
	before := assistantmemory.Fingerprint(assistantmemory.Relevant(saved, "recommendations"))
	saved.Memories[0].Key = assistantmemory.Answer
	if before == assistantmemory.Fingerprint(assistantmemory.Relevant(saved, "recommendations")) {
		t.Fatal("generation relevance filter failed")
	}
}
