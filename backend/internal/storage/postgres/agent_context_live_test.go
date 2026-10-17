package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LeoninCS/jobpilot-next/backend/internal/agent"
	"github.com/LeoninCS/jobpilot-next/backend/internal/agentcontext"
	"github.com/LeoninCS/jobpilot-next/backend/internal/assistantmemory"
	"github.com/LeoninCS/jobpilot-next/backend/internal/config"
	"github.com/LeoninCS/jobpilot-next/backend/internal/knowledgegaps"
	"github.com/LeoninCS/jobpilot-next/backend/internal/market"
	"github.com/LeoninCS/jobpilot-next/backend/internal/model/deepseek"
	"github.com/LeoninCS/jobpilot-next/backend/internal/modelconfig"
	"github.com/LeoninCS/jobpilot-next/backend/internal/modelusage"
	"github.com/LeoninCS/jobpilot-next/backend/internal/profile"
	"github.com/LeoninCS/jobpilot-next/backend/internal/projectrecs"
	"github.com/LeoninCS/jobpilot-next/backend/internal/secure"
	"github.com/LeoninCS/jobpilot-next/backend/internal/target"
	"github.com/google/uuid"
)

type contextEvalCase struct {
	ID, Category, Key, Initial, Current, Question, Expected string
	Repeated                                                bool
}

func contextEvalCases() []contextEvalCase {
	keys := []string{assistantmemory.Languages, assistantmemory.Excluded, assistantmemory.Architecture, assistantmemory.Learning, assistantmemory.Answer}
	initial := []string{"Go", "商城", "单体服务", "实践优先", "简短"}
	updated := []string{"Python", "博客", "模块化单体", "原理优先", "详细"}
	labels := []string{"项目语言", "排除的项目类型", "项目架构", "学习方式", "回答详略"}
	out := []contextEvalCase{}
	for group, category := range []string{"long_dialogue", "cross_session", "updates", "forgetting", "unknown", "discussion_state"} {
		for i, key := range keys {
			c := contextEvalCase{ID: fmt.Sprintf("%s-%d", category, i+1), Category: category, Key: key, Initial: initial[i], Question: "我之前确认的" + labels[i] + "是什么？", Expected: initial[i], Repeated: group < 2}
			if group == 0 {
				c.Question = "只根据本会话之前的讨论，" + labels[i] + "约束是什么？"
			}
			if group == 2 {
				c.Current = updated[i]
				c.Expected = updated[i]
			}
			if group == 3 {
				c.Expected = "未知"
				c.Question = "我现在仍然保存的" + labels[i] + "是什么？遗忘的内容不可作为当前偏好。"
			}
			if group == 4 {
				c.Expected = "未知"
				c.Question = []string{"我从未提供过的第二邮箱是什么？", "我从未提供过的公司名称是什么？", "我从未提供过的论文编号是什么？", "我从未提供过的毕业日期是哪一天？", "我从未提供过的 GitHub 用户名是什么？"}[i]
			}
			if group == 5 {
				c.Expected = "未知"
				c.Question = "之前只是讨论、尚未正式选择项目 B。我在系统里正式选定的项目名称是什么？没有正式选择则未知。"
			}
			out = append(out, c)
		}
	}
	return out
}
func TestContextEvaluationInventory(t *testing.T) {
	seen := map[string]bool{}
	counts := map[string]int{}
	repeated := 0
	for _, c := range contextEvalCases() {
		if seen[c.ID] || c.Question == "" || c.Expected == "" {
			t.Fatal("invalid evaluation case")
		}
		seen[c.ID] = true
		counts[c.Category]++
		if c.Repeated {
			repeated++
		}
	}
	if len(seen) != 30 || len(counts) != 6 || repeated != 10 {
		t.Fatal("evaluation inventory changed")
	}
}

type contextEvalCredentials struct{ value modelconfig.Credentials }

type contextEvalSummary struct {
	client    *deepseek.Client
	Output    string
	Err       bool
	ErrorCode string
}

func (s *contextEvalSummary) GenerateJSON(ctx context.Context, key, model, system, input string, out any) error {
	e := s.client.GenerateJSON(ctx, key, model, system, input, out)
	b, _ := json.Marshal(out)
	s.Output = string(b)
	s.Err = e != nil
	if e != nil {
		s.ErrorCode = e.Error()
	}
	return e
}
func (c contextEvalCredentials) Credentials(context.Context, uuid.UUID) (modelconfig.Credentials, error) {
	return c.value, nil
}
func evalValue(key, value string) json.RawMessage {
	var b []byte
	if key == assistantmemory.Languages || key == assistantmemory.Excluded {
		b, _ = json.Marshal([]string{value})
	} else {
		b, _ = json.Marshal(value)
	}
	return b
}

// This is opt-in, uses only synthetic material and the explicitly selected
// dedicated account's existing key. Real usage is recorded in its normal ledger.
// No credentials, prompts or answers are printed or written to the report.
func TestLiveAgentContextEvaluation(t *testing.T) {
	if os.Getenv("RUN_AGENT_CONTEXT_LIVE_TEST") != "1" {
		t.Skip("explicit opt-in and dedicated test account required")
	}
	sourceUser, e := uuid.Parse(os.Getenv("MODEL_USAGE_TEST_USER"))
	if e != nil {
		t.Fatal("dedicated test account required")
	}
	cfg, e := config.Load()
	if e != nil {
		t.Fatal("local configuration unavailable")
	}
	source, e := Open(t.Context(), cfg.DatabaseURL)
	if e != nil {
		t.Fatal("source database unavailable")
	}
	defer source.Close()
	cipher, e := secure.NewCipher(cfg.CredentialEncryptionKey)
	if e != nil {
		t.Fatal("credential cipher unavailable")
	}
	credentials, e := modelconfig.NewService(NewModelConfigRepository(source), cipher, nil).Credentials(t.Context(), sourceUser)
	if e != nil {
		t.Fatal("dedicated account model configuration unavailable")
	}
	db := agentTransactionDatabase(t)
	repo := NewAgentRepository(db)
	mem := NewAssistantMemoryRepository(db)
	usage := NewModelUsageRepository(source)
	prices, e := modelusage.LoadPricing(cfg.ModelPricingFile)
	if e != nil {
		t.Fatal("pricing configuration invalid")
	}
	client := &http.Client{Timeout: 3 * time.Minute, Transport: &modelusage.Transport{Repo: usage, Provider: "deepseek", Pricing: prices}}
	generator := &contextEvalSummary{client: deepseek.NewClient(cfg.DeepSeekBaseURL, client)}
	targets := target.NewService(NewTargetRepository(db))
	profiles := profile.NewService(NewProfileRepository(db), nil)
	markets := market.NewService(NewMarketRepository(db), targets)
	gaps := knowledgegaps.NewService(NewKnowledgeGapsRepository(db), targets, markets, profiles)
	projects := projectrecs.NewService(NewProjectRecommendationRepository(db), gaps, profiles, nil)
	type row struct {
		Case, Category, Mode             string
		Repeat                           int
		Correct                          bool
		Error                            bool
		DurationMS                       int64
		Calls, InputTokens, OutputTokens int64
	}
	results := []row{}
	retryOnly := map[string]bool{}
	if path := os.Getenv("AGENT_CONTEXT_LIVE_RECHECK"); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var previous []row
		if json.Unmarshal(data, &previous) != nil {
			t.Fatal("invalid aggregate report")
		}
		for _, r := range previous {
			if r.Error {
				retryOnly[fmt.Sprintf("%s:%s:%d", r.Case, r.Mode, r.Repeat)] = true
			}
		}
		if len(retryOnly) == 0 {
			t.Fatal("no failed runs to recheck")
		}
	}
	cases := contextEvalCases()
	if os.Getenv("AGENT_CONTEXT_LIVE_SMOKE") == "1" {
		cases = cases[:1]
	}
	for _, c := range cases {
		if only := os.Getenv("AGENT_CONTEXT_LIVE_CASE"); only != "" && c.ID != only {
			continue
		}
		repeat := 1
		if c.Repeated && os.Getenv("AGENT_CONTEXT_LIVE_SMOKE") != "1" {
			repeat = 3
		}
		for repetition := 1; repetition <= repeat; repetition++ {
			for _, mode := range []string{"last12", "window", "layered"} {
				if len(retryOnly) > 0 && !retryOnly[fmt.Sprintf("%s:%s:%d", c.ID, mode, repetition)] {
					continue
				}
				if only := os.Getenv("AGENT_CONTEXT_LIVE_MODE"); only != "" && mode != only {
					continue
				}
				user := uuid.New()
				conversation, err := repo.Create(t.Context(), user, "no-target", "context-evaluation")
				if err != nil {
					t.Fatal(err)
				}
				pref := assistantmemory.Memory{}
				if c.Category == "cross_session" || c.Category == "updates" || c.Category == "forgetting" {
					pref, err = mem.Save(t.Context(), user, "no-target", nil, assistantmemory.Write{Key: c.Key, Value: evalValue(c.Key, c.Initial)})
					if err != nil {
						t.Fatal(err)
					}
				}
				if c.Category != "cross_session" {
					initial := "只在当前会话讨论的约束：" + c.Key + " 为 " + c.Initial + "。这不是能力等级或正式项目选择。"
					if c.Category == "unknown" {
						initial = "我没有提供个人邮箱、公司、论文编号、毕业日期或用户名。"
					}
					if c.Category == "discussion_state" {
						initial = "我们只是倾向项目B，尚未在业务系统正式选择。"
					}
					if _, err = repo.AddMessage(t.Context(), conversation.ID, "user", initial); err != nil {
						t.Fatal(err)
					}
					filler := strings.Repeat("这里只讨论HTTP请求、Go接口与技术原理，没有修改个人资料，也没有选择项目。", 8)
					if _, err = db.ExecContext(t.Context(), `INSERT INTO agent_messages(conversation_id,role,content) SELECT $1,'user',$2 FROM generate_series(1,80)`, conversation.ID, filler); err != nil {
						t.Fatal(err)
					}
				}
				if c.Category == "updates" {
					_, err = mem.Save(t.Context(), user, "no-target", &pref.ID, assistantmemory.Write{Key: c.Key, Value: evalValue(c.Key, c.Current), Revision: pref.Revision, Hash: pref.Hash})
				}
				if c.Category == "forgetting" {
					err = mem.Forget(t.Context(), user, "no-target", pref.ID, pref.Revision, pref.Hash)
				}
				if err != nil {
					t.Fatal(err)
				}
				options := agentcontext.DefaultConfig()
				options.Enabled = mode == "layered"
				options.MemoryEnabled = mode == "layered"
				s := &agent.Service{Repo: repo, Checkpoints: NewAgentCheckpointStore(db), Transactions: repo, Targets: targets, Profile: profiles, Market: markets, Gaps: gaps, Projects: projects, Credentials: contextEvalCredentials{credentials}, DeepSeekBaseURL: cfg.DeepSeekBaseURL, ModelHTTP: client, ContextConfig: options, SummaryModel: generator}
				if mode != "last12" {
					s.ContextRepo = NewAgentContextRepository(db)
					s.Memories = mem
				}
				operation := uuid.New()
				ctx, cancel := context.WithTimeout(modelusage.For(t.Context(), sourceUser, operation, "agent", "user"), 4*time.Minute)
				var answer strings.Builder
				started := time.Now()
				err = s.Send(ctx, user, conversation.ID, "dashboard", c.Question+" 请只返回 JSON {\"answer\":\"值\"}。无法从有效上下文或已确认偏好确定时填未知。不要凭其他问题猜测，不生成或修改业务资料。", func(event agent.Event) {
					if event.Type == "delta" {
						answer.WriteString(event.Text)
					}
				})
				cancel()
				text := answer.String()
				start, end := strings.LastIndex(text, "{"), strings.LastIndex(text, "}")
				var decoded struct {
					Answer string `json:"answer"`
				}
				if start >= 0 && end > start {
					_ = json.Unmarshal([]byte(text[start:end+1]), &decoded)
				}
				result := row{Case: c.ID, Category: c.Category, Mode: mode, Repeat: repetition, Correct: strings.Contains(decoded.Answer, c.Expected), Error: err != nil, DurationMS: time.Since(started).Milliseconds()}
				_ = source.QueryRowContext(t.Context(), `SELECT count(*),COALESCE(sum(input_tokens),0),COALESCE(sum(output_tokens),0) FROM model_usage_calls WHERE source_operation_id=$1 AND purpose='agent'`, operation).Scan(&result.Calls, &result.InputTokens, &result.OutputTokens)
				results = append(results, result)
				if os.Getenv("AGENT_CONTEXT_EVAL_DEBUG") == "1" {
					var summary string
					_ = db.QueryRow(`SELECT content::text FROM agent_context_summaries WHERE conversation_id=$1`, conversation.ID).Scan(&summary)
					t.Logf("synthetic-only diagnostics answer=%q summary=%q generated=%q summary_error=%t error_code=%q run_error=%v", text, summary, generator.Output, generator.Err, generator.ErrorCode, err)
				}
				t.Logf("case=%s mode=%s repeat=%d correct=%t error=%t calls=%d input=%d output=%d ms=%d", c.ID, mode, repetition, result.Correct, result.Error, result.Calls, result.InputTokens, result.OutputTokens, result.DurationMS)
				// Persist aggregate metadata after every completed run, allowing a failed
				// or interrupted evaluation to resume analysis without re-paying old calls.
				if destination := os.Getenv("AGENT_CONTEXT_EVAL_OUTPUT"); destination != "" {
					b, _ := json.MarshalIndent(results, "", "  ")
					if err = os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
						t.Fatal(err)
					}
					if err = os.WriteFile(destination, b, 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
		}
	}
	for _, r := range results {
		if r.Error {
			t.Error("one or more model runs failed; inspect aggregate report, no automatic paid retries")
			break
		}
	}
}
