package agent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/LeoninCS/jobpilot-next/backend/internal/embedding"
	"github.com/LeoninCS/jobpilot-next/backend/internal/target"
	"github.com/cloudwego/eino/components/tool"
	"github.com/google/uuid"
)

type sourceSearchStub struct{ calls int }

type sourceCitationRepository struct {
	Repository
	messages []Message
	err      error
}

func (r *sourceCitationRepository) AddMessage(_ context.Context, _ uuid.UUID, role, content string) (Message, error) {
	if r.err != nil {
		return Message{}, r.err
	}
	message := Message{Role: role, Content: content}
	r.messages = append(r.messages, message)
	return message, nil
}

func (s *sourceSearchStub) SearchSources(_ context.Context, _, _ uuid.UUID, _ []float32, _ int) ([]SourceMatch, error) {
	s.calls++
	return []SourceMatch{{SourceType: "jd", SourceID: uuid.New(), Quote: "负责 Go 后端开发"}}, nil
}

func TestSourceSearchRespectsEmbeddingSwitch(t *testing.T) {
	var providerCalls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		providerCalls.Add(1)
		vector := make([]float32, embedding.Dimensions)
		vector[0] = 1
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"index": 0, "embedding": vector}}})
	}))
	defer provider.Close()
	search := &sourceSearchStub{}
	service := &Service{Embedder: embedding.NewClient(provider.URL, "configured-key", provider.Client()), Vectors: search}
	goal := &target.Target{ID: uuid.New()}
	input := readArgs{Resource: "source_search", Query: "Go 后端"}

	answer, err := service.read(context.Background(), uuid.New(), goal, input, func(Event) {})
	if err != nil || !strings.Contains(answer, "尚未启用") {
		t.Fatalf("disabled search should explain the switch: answer=%q err=%v", answer, err)
	}
	if providerCalls.Load() != 0 || search.calls != 0 {
		t.Fatalf("disabled search used vectors: provider=%d search=%d", providerCalls.Load(), search.calls)
	}

	service.EmbeddingEnabled = true
	answer, err = service.read(context.Background(), uuid.New(), goal, input, func(Event) {})
	if err != nil || !strings.Contains(answer, "负责 Go 后端开发") {
		t.Fatalf("enabled search failed: answer=%q err=%v", answer, err)
	}
	if providerCalls.Load() != 1 || search.calls != 1 {
		t.Fatalf("enabled search did not use vectors: provider=%d search=%d", providerCalls.Load(), search.calls)
	}
}

func TestSourceSearchPersistsCitationsForConversationReload(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		vector := make([]float32, embedding.Dimensions)
		vector[0] = 1
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"index": 0, "embedding": vector}}})
	}))
	defer provider.Close()
	repository := &sourceCitationRepository{}
	service := &Service{Repo: repository, EmbeddingEnabled: true, Embedder: embedding.NewClient(provider.URL, "test-key", provider.Client()), Vectors: &sourceSearchStub{}}
	ctx := context.Background()
	tools, err := service.tools(ctx, uuid.New(), uuid.New(), "goal", &target.Target{ID: uuid.New()}, func(Event) {})
	if err != nil {
		t.Fatal(err)
	}
	read := tools[0].(tool.InvokableTool)
	if _, err = read.InvokableRun(ctx, `{"resource":"source_search","query":"Go 后端"}`); err != nil {
		t.Fatal(err)
	}
	if len(repository.messages) != 1 || repository.messages[0].Role != "tool" {
		t.Fatalf("source metadata was not persisted: %#v", repository.messages)
	}
	var record struct {
		Type      string        `json:"type"`
		Citations []SourceMatch `json:"citations"`
	}
	if err = json.Unmarshal([]byte(repository.messages[0].Content), &record); err != nil {
		t.Fatal(err)
	}
	if record.Type != "source_citations" || len(record.Citations) != 1 || record.Citations[0].Quote != "负责 Go 后端开发" {
		t.Fatalf("saved source lost its original quotation: %#v", record)
	}
	repository.err = errors.New("storage unavailable")
	if _, err = read.InvokableRun(ctx, `{"resource":"source_search","query":"Go 后端"}`); err == nil {
		t.Fatal("a source persistence failure must not be reported as success")
	}
}
