package modelusage_test

import (
	"context"
	"errors"
	"github.com/LeoninCS/jobpilot-next/backend/internal/model/deepseek"
	"github.com/LeoninCS/jobpilot-next/backend/internal/modelusage"
	"github.com/google/uuid"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type memoryRepo struct {
	mu                  sync.Mutex
	calls               []modelusage.Call
	beginErr, finishErr error
	finishes            int
}

func (r *memoryRepo) Begin(_ context.Context, c modelusage.Call) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.beginErr != nil {
		return r.beginErr
	}
	r.calls = append(r.calls, c)
	return nil
}
func (r *memoryRepo) Finish(_ context.Context, c modelusage.Call) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.finishes++
	if r.finishErr != nil {
		return r.finishErr
	}
	for i := range r.calls {
		if r.calls[i].ID == c.ID {
			r.calls[i] = c
		}
	}
	return nil
}
func (r *memoryRepo) EnsureOperation(context.Context, modelusage.Scope) error { return nil }
func (r *memoryRepo) Summary(context.Context, uuid.UUID, modelusage.Filter) ([]modelusage.Group, error) {
	return nil, nil
}
func (r *memoryRepo) Calls(context.Context, uuid.UUID, modelusage.Filter) (modelusage.Page[modelusage.Call], error) {
	return modelusage.Page[modelusage.Call]{}, nil
}
func (r *memoryRepo) Operations(context.Context, uuid.UUID, modelusage.Filter) (modelusage.Page[modelusage.Operation], error) {
	return modelusage.Page[modelusage.Operation]{}, nil
}
func usageContext() context.Context {
	return modelusage.For(context.Background(), uuid.New(), uuid.New(), "connection_test", "user")
}

func TestStreamCancellationConcurrentWithReadStaysUnverified(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	repo := &memoryRepo{}
	client := &http.Client{Transport: &modelusage.Transport{Repo: repo, Provider: "deepseek"}}
	req, _ := http.NewRequestWithContext(usageContext(), "POST", server.URL, strings.NewReader(`{"model":"test"}`))
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, response.Body); close(done) }()
	_ = response.Body.Close()
	<-done
	_ = response.Body.Close()
	if repo.finishes != 1 || repo.calls[0].Input != nil || repo.calls[0].Cost != nil || repo.calls[0].UsageStatus != "unknown" {
		t.Fatal("cancellation fabricated usage or finalized twice")
	}
}
func TestInvalidGeneratedJSONStillRecordsExactUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"supplier-1","model":"test","usage":{"prompt_tokens":10000,"completion_tokens":1000,"total_tokens":11000,"prompt_cache_hit_tokens":8000,"prompt_cache_miss_tokens":2000},"choices":[{"message":{"content":"{invalid generated JSON"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()
	repo := &memoryRepo{}
	price := modelusage.Pricing{Version: "test-v1", Prices: []modelusage.Price{{Provider: "deepseek", Model: "test", Currency: "CNY", Input: "1", CacheHit: "0.1", Output: "2"}}}
	client := deepseek.NewClient(server.URL, &http.Client{Transport: &modelusage.Transport{Repo: repo, Provider: "deepseek", Pricing: price}})
	var out map[string]any
	if err := client.GenerateJSON(usageContext(), "not-a-real-key", "test", "system", "prompt", &out); err == nil {
		t.Fatal("invalid JSON accepted")
	}
	if len(repo.calls) != 1 || repo.calls[0].UsageStatus != "known" || *repo.calls[0].Input != 10000 || *repo.calls[0].CacheHit != 8000 || repo.calls[0].Cost == nil || *repo.calls[0].Cost != "0.004800000000" {
		t.Fatalf("usage/cost lost or cache counted twice: %+v", repo.calls)
	}
	if strings.Contains(string(repo.calls[0].PriceSnapshot), "not-a-real-key") {
		t.Fatal("credential persisted")
	}
}
func TestRecorderFailurePreventsNetworkDispatch(t *testing.T) {
	sent := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { sent++ }))
	defer server.Close()
	repo := &memoryRepo{beginErr: errors.New("database down")}
	client := &http.Client{Transport: &modelusage.Transport{Repo: repo, Provider: "deepseek"}}
	req, _ := http.NewRequestWithContext(usageContext(), "POST", server.URL, strings.NewReader(`{"model":"test"}`))
	if _, err := client.Do(req); !errors.Is(err, modelusage.ErrUnavailable) {
		t.Fatal(err)
	}
	if sent != 0 {
		t.Fatal("unrecorded paid call sent")
	}
}
func TestStreamFragmentationAndDuplicateUsageDoNotAccumulate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"model\":\"test\",\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n")
		usage := `data: {"id":"stream-1","model":"test","usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0,"prompt_cache_hit_tokens":0,"prompt_cache_miss_tokens":0}}` + "\n\n"
		io.WriteString(w, usage+usage+"data: [DONE]\n\n")
	}))
	defer server.Close()
	repo := &memoryRepo{}
	client := &http.Client{Transport: &modelusage.Transport{Repo: repo, Provider: "deepseek"}}
	req, _ := http.NewRequestWithContext(usageContext(), "POST", server.URL, strings.NewReader(`{"model":"test"}`))
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1)
	for {
		_, err = response.Body.Read(buf)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	response.Body.Close()
	if repo.finishes != 1 || len(repo.calls) != 1 || repo.calls[0].Input == nil || *repo.calls[0].Input != 0 || repo.calls[0].UsageStatus != "known" {
		t.Fatal("zero missing or stream recorded twice")
	}
}
func TestEmbeddingTokensAndUnknownStream(t *testing.T) {
	for _, stream := range []bool{false, true} {
		repo := &memoryRepo{}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if stream {
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"unfinished\"}}]}\n\n")
			} else {
				io.WriteString(w, `{"model":"text-embedding-v4","id":"embedding-1","usage":{"total_tokens":27},"data":[]}`)
			}
		}))
		client := &http.Client{Transport: &modelusage.Transport{Repo: repo, Provider: "bailian", Payer: "platform"}}
		req, _ := http.NewRequestWithContext(usageContext(), "POST", server.URL, strings.NewReader(`{"model":"text-embedding-v4"}`))
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		server.Close()
		c := repo.calls[0]
		if stream {
			if c.UsageStatus != "unknown" || c.Input != nil || c.Cost != nil {
				t.Fatal("unknown usage fabricated")
			}
		} else if c.Input == nil || *c.Input != 27 || c.Output != nil || c.CacheHit != nil || c.Payer != "platform" {
			t.Fatal("embedding billed as chat")
		}
	}
}
func TestRetriesAndPostResponseStorageFailure(t *testing.T) {
	repo := &memoryRepo{finishErr: errors.New("write failed")}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"model":"test","usage":{"prompt_tokens":1,"completion_tokens":2}}`)
	}))
	defer server.Close()
	client := &http.Client{Transport: &modelusage.Transport{Repo: repo, Provider: "deepseek"}}
	ctx := usageContext()
	for range 2 {
		req, _ := http.NewRequestWithContext(ctx, "POST", server.URL, strings.NewReader(`{"model":"test"}`))
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
	}
	if len(repo.calls) != 2 || repo.calls[0].ID == repo.calls[1].ID || repo.calls[0].UsageStatus != "unknown" {
		t.Fatal("retry merged or failed persistence pretended complete")
	}
}
