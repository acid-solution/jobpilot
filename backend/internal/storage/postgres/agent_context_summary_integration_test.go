package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LeoninCS/jobpilot-next/backend/internal/agent"
	"github.com/LeoninCS/jobpilot-next/backend/internal/agentcontext"
	"github.com/LeoninCS/jobpilot-next/backend/internal/target"
	"github.com/google/uuid"
)

type summaryFixture struct {
	t       *testing.T
	fail    bool
	invalid bool
	calls   int
}

func (f *summaryFixture) GenerateJSON(_ context.Context, _, _, system, input string, out any) error {
	f.calls++
	if !strings.Contains(strings.ToLower(system), "json") {
		f.t.Error("JSON mode prompt must explicitly mention JSON")
	}
	if f.fail {
		return errors.New("simulated model failure")
	}
	var payload struct {
		Messages []agentcontext.Message `json:"messages"`
	}
	if e := json.Unmarshal([]byte(input), &payload); e != nil {
		return e
	}
	if len(payload.Messages) == 0 {
		return errors.New("empty summary source")
	}
	source := payload.Messages[0].ID
	if f.invalid {
		source = uuid.New()
	}
	out.(*agentcontext.Summary).Items = []agentcontext.Item{{Kind: "constraint", Text: "本会话项目优先 Go，B 仍在讨论", State: "discussed", Sources: []uuid.UUID{source}}}
	return nil
}
func TestAgentContextCompactionAndSafeFallback(t *testing.T) {
	db := agentTransactionDatabase(t)
	repo := NewAgentRepository(db)
	for _, mode := range []string{"valid", "failure", "invalid_source"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"id\":\"summary-test\",\"model\":\"deepseek-chat\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"已核对讨论状态。\"}}]}\n\ndata: [DONE]\n\n")
			}))
			defer server.Close()
			user := uuid.New()
			conversation, e := repo.Create(t.Context(), user, "no-target", "新对话")
			if e != nil {
				t.Fatal(e)
			}
			_, e = repo.AddMessage(t.Context(), conversation.ID, "user", "本会话项目优先 Go，B 只是讨论候选")
			if e != nil {
				t.Fatal(e)
			}
			filler := strings.Repeat("这里只讨论技术原理，没有选择项目。", 10)
			if _, e = db.Exec(`INSERT INTO agent_messages(conversation_id,role,content) SELECT $1,'user',$2 FROM generate_series(1,100)`, conversation.ID, filler); e != nil {
				t.Fatal(e)
			}
			model := &summaryFixture{t: t, fail: mode == "failure", invalid: mode == "invalid_source"}
			s := &agent.Service{Repo: repo, ContextRepo: NewAgentContextRepository(db), Memories: NewAssistantMemoryRepository(db), ContextConfig: agentcontext.DefaultConfig(), Transactions: repo, Checkpoints: NewAgentCheckpointStore(db), Targets: target.NewService(NewTargetRepository(db)), Credentials: transactionTestCredentials{}, SummaryModel: model, DeepSeekBaseURL: server.URL, ModelHTTP: server.Client()}
			fallback := false
			if e = s.Send(t.Context(), user, conversation.ID, "dashboard", "继续讨论", func(v agent.Event) {
				if v.Type == "context" && strings.Contains(v.Text, "整理未完成") {
					fallback = true
				}
			}); e != nil {
				t.Fatal(e)
			}
			var count int
			var covered int64
			if e = db.QueryRow(`SELECT count(*),COALESCE(max(covered_sequence),0) FROM agent_context_summaries WHERE conversation_id=$1`, conversation.ID).Scan(&count, &covered); e != nil {
				t.Fatal(e)
			}
			if model.calls != 1 {
				t.Fatalf("summary calls=%d", model.calls)
			}
			if mode == "valid" && (count != 1 || covered < 1) {
				t.Fatal("valid summary not published")
			}
			if mode != "valid" && (count != 0 || !fallback) {
				t.Fatal("failure advanced coverage or lost warning")
			}
		})
	}
}
