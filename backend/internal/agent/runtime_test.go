package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/LeoninCS/jobpilot-next/backend/internal/market"
	deepseekmodel "github.com/cloudwego/eino-ext/components/model/deepseek"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
)

func TestEinoBusinessToolSchemas(t *testing.T) {
	s := &Service{}
	tools, err := s.tools(context.Background(), uuid.New(), uuid.New(), "scope", nil, func(Event) {})
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 2 {
		t.Fatalf("expected read and proposal tools, got %d", len(tools))
	}
	model, err := deepseekmodel.NewChatModel(context.Background(), &deepseekmodel.ChatModelConfig{APIKey: "test", Model: "deepseek-chat", BaseURL: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = adk.NewChatModelAgent(context.Background(), &adk.ChatModelAgentConfig{Name: "test", Model: model, ToolsConfig: adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{Tools: tools}}})
	if err != nil {
		t.Fatal(err)
	}
}

func TestConfirmationArgumentsOnlyContainExecutedFields(t *testing.T) {
	input := json.RawMessage(`{"raw_text":"测试公司招聘 Go 后端实习生，负责 API 开发，要求熟悉 Go。","title":"模型猜测标题","company":"模型猜测公司","target_id":"8dffa02e-87fb-44d9-8dd4-efacdd736cdc"}`)
	actual, err := canonicalArguments("jd_add", input)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err = json.Unmarshal(actual, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 1 || fields["raw_text"] == "" {
		t.Fatalf("confirmation card has fields the service will ignore: %s", actual)
	}
}

func TestJDListOnlySendsLocatorFieldsToModel(t *testing.T) {
	id := uuid.New()
	response := summarizeJDList([]market.JobDescription{{
		ID: id, Title: "Go 后端开发", Company: "测试", RawText: "完整岗位原文及个人信息",
		Responsibilities: []string{"很长的职责"},
	}})
	if !strings.Contains(response, id.String()) || !strings.Contains(response, "Go 后端开发") {
		t.Fatalf("list summary cannot locate the JD: %s", response)
	}
	if strings.Contains(response, "完整岗位原文") || strings.Contains(response, "很长的职责") {
		t.Fatalf("list summary leaked full JD into model context: %s", response)
	}
}

func TestEinoDeepSeekStreamsVisibleText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"id\":\"test\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"deepseek-chat\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"你好\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	model, err := deepseekmodel.NewChatModel(context.Background(), &deepseekmodel.ChatModelConfig{APIKey: "test", Model: "deepseek-chat", BaseURL: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	a, err := adk.NewChatModelAgent(context.Background(), &adk.ChatModelAgentConfig{Name: "test", Model: model})
	if err != nil {
		t.Fatal(err)
	}
	runner := adk.NewRunner(context.Background(), adk.RunnerConfig{Agent: a, EnableStreaming: true})
	var received strings.Builder
	for iter := runner.Query(context.Background(), "打招呼"); ; {
		event, ok := iter.Next()
		if !ok {
			break
		}
		if event.Err != nil {
			t.Fatal(event.Err)
		}
		if event.Output == nil || event.Output.MessageOutput == nil {
			continue
		}
		out := event.Output.MessageOutput
		if out.MessageStream != nil {
			for {
				chunk, e := out.MessageStream.Recv()
				if e == io.EOF {
					break
				}
				if e != nil {
					t.Fatal(e)
				}
				if chunk != nil && chunk.Role == schema.Assistant {
					received.WriteString(chunk.Content)
				}
			}
		}
	}
	if received.String() != "你好" {
		t.Fatalf("visible content = %q", received.String())
	}
}

type memoryCheckpoint struct {
	mu     sync.Mutex
	values map[string][]byte
}

func (m *memoryCheckpoint) Get(_ context.Context, key string) ([]byte, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.values[key]
	return v, ok, nil
}
func (m *memoryCheckpoint) Set(_ context.Context, key string, value []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.values[key] = value
	return nil
}
func (m *memoryCheckpoint) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.values, key)
	return nil
}

func TestEinoToolInterruptAndResume(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "text/event-stream")
		var delta any = map[string]any{"role": "assistant", "content": "已执行"}
		finish := "stop"
		if requests == 1 {
			delta = map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": 0, "id": "call1", "type": "function", "function": map[string]any{"name": "confirm_change", "arguments": "{\"value\":\"x\"}"}}}}
			finish = "tool_calls"
		}
		body, _ := json.Marshal(map[string]any{"id": "test", "object": "chat.completion.chunk", "created": 1, "model": "deepseek-chat", "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
		_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", body)
	}))
	defer server.Close()
	model, err := deepseekmodel.NewChatModel(context.Background(), &deepseekmodel.ChatModelConfig{APIKey: "test", Model: "deepseek-chat", BaseURL: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	confirm, err := utils.InferTool("confirm_change", "test confirmation", func(ctx context.Context, in struct {
		Value string `json:"value"`
	}) (string, error) {
		was, has, state := tool.GetInterruptState[string](ctx)
		if !was {
			return "", tool.StatefulInterrupt(ctx, "approve", in.Value)
		}
		isTarget, _, _ := tool.GetResumeContext[bool](ctx)
		if !has || !isTarget || state != "x" {
			return "", fmt.Errorf("invalid resume state")
		}
		return "approved", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	a, err := adk.NewChatModelAgent(context.Background(), &adk.ChatModelAgentConfig{Name: "test", Model: model, ToolsConfig: adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{Tools: []tool.BaseTool{confirm}}}})
	if err != nil {
		t.Fatal(err)
	}
	store := &memoryCheckpoint{values: map[string][]byte{}}
	runner := adk.NewRunner(context.Background(), adk.RunnerConfig{Agent: a, EnableStreaming: true, CheckPointStore: store})
	var interruptID string
	for iter := runner.Query(context.Background(), "change", adk.WithCheckPointID("test-checkpoint")); ; {
		event, ok := iter.Next()
		if !ok {
			break
		}
		if event.Err != nil {
			t.Fatal(event.Err)
		}
		if event.Action != nil && event.Action.Interrupted != nil {
			for _, point := range event.Action.Interrupted.InterruptContexts {
				if point.IsRootCause {
					interruptID = point.ID
				}
			}
		}
		if event.Output != nil && event.Output.MessageOutput != nil && event.Output.MessageOutput.MessageStream != nil {
			stream := event.Output.MessageOutput.MessageStream
			for {
				_, e := stream.Recv()
				if e == io.EOF {
					break
				}
				if e != nil {
					t.Fatal(e)
				}
			}
		}
	}
	if interruptID == "" {
		t.Fatal("tool did not interrupt")
	}
	iter, err := runner.ResumeWithParams(context.Background(), "test-checkpoint", &adk.ResumeParams{Targets: map[string]any{interruptID: true}})
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	for {
		event, ok := iter.Next()
		if !ok {
			break
		}
		if event.Err != nil {
			t.Fatal(event.Err)
		}
		if event.Output != nil && event.Output.MessageOutput != nil {
			out := event.Output.MessageOutput
			if out.MessageStream != nil {
				for {
					piece, e := out.MessageStream.Recv()
					if e == io.EOF {
						break
					}
					if e != nil {
						t.Fatal(e)
					}
					if piece != nil && piece.Role == schema.Assistant {
						text.WriteString(piece.Content)
					}
				}
			}
		}
	}
	if text.String() != "已执行" {
		t.Fatalf("unexpected resume output %q", text.String())
	}
}
