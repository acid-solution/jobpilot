package embedding

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEmbedUsesPlatformModelAndValidatesDimensions(t *testing.T) {
	vector := make([]float32, Dimensions)
	vector[0] = 0.5
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embeddings" || r.Header.Get("Authorization") != "Bearer platform-test-key" {
			t.Errorf("unexpected request %s %s", r.URL.Path, r.Header.Get("Authorization"))
		}
		var body struct {
			Model      string   `json:"model"`
			Dimensions int      `json:"dimensions"`
			Input      []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Model != Model || body.Dimensions != Dimensions || len(body.Input) != 1 || body.Input[0] != "Go 后端" {
			t.Errorf("unexpected embedding request: %+v", body)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"index": 0, "embedding": vector}}})
	}))
	defer server.Close()
	client := NewClient(server.URL, "platform-test-key", server.Client())
	result, err := client.Embed(context.Background(), []string{"Go 后端"})
	if err != nil || len(result) != 1 || len(result[0]) != Dimensions || result[0][0] != 0.5 {
		t.Fatalf("invalid embedding response: %v %v", result, err)
	}
}

func TestEmbedRejectsWrongDimension(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"index": 0, "embedding": []float32{1}}}})
	}))
	defer server.Close()
	if _, err := NewClient(server.URL, "key", server.Client()).Embed(context.Background(), []string{"Go"}); err == nil {
		t.Fatal("accepted an incompatible embedding dimension")
	}
}

func TestSplitPreservesRuneOffsets(t *testing.T) {
	input := strings.Repeat("Go 开发。", 180)
	for _, chunk := range Split(input) {
		runes := []rune(input)
		if chunk.Start < 0 || chunk.End > len(runes) || chunk.Start >= chunk.End || string(runes[chunk.Start:chunk.End]) != chunk.Text {
			t.Fatalf("invalid source offset: %+v", chunk)
		}
	}
}
