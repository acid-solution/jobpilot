package jdanalysis

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LeoninCS/jobpilot-next/backend/internal/embedding"
	"github.com/google/uuid"
)

type candidateSearch struct {
	matches []AbilityMatch
	calls   int
}

func (s *candidateSearch) SearchAbilities(_ context.Context, _ []float32, _ int) ([]AbilityMatch, error) {
	s.calls++
	return s.matches, nil
}

type normalizeModel struct {
	result any
	calls  int
}

func (m *normalizeModel) GenerateJSON(_ context.Context, _, _, _, _ string, out any) error {
	m.calls++
	b, _ := json.Marshal(m.result)
	return json.Unmarshal(b, out)
}
func testEmbedder(t *testing.T) *embedding.Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		data := make([]map[string]any, len(input.Input))
		for i := range data {
			data[i] = map[string]any{"index": i, "embedding": make([]float32, 1024)}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	t.Cleanup(server.Close)
	return embedding.NewClient(server.URL, "test", server.Client())
}
func TestVectorNormalizerExactMatchSkipsEmbeddingAndModel(t *testing.T) {
	search := &candidateSearch{}
	model := &normalizeModel{}
	n := &VectorNormalizer{Embedder: testEmbedder(t), Search: search, Model: model}
	result := Result{AbilityRequirements: []AbilityRequirement{{Operator: RequirementSingle, RequiredCount: 1, Evidence: "熟悉 Auto-Gen", Options: []AbilityRequirementOption{{RawLabel: "Auto-Gen", Evidence: "Auto-Gen"}}}}}
	got, err := n.Normalize(context.Background(), uuid.New(), "key", "model", Catalog{Abilities: []AbilityOption{{Code: "ability-1", Name: "AutoGen"}}}, result)
	if err != nil {
		t.Fatal(err)
	}
	if got.AbilityRequirements[0].Options[0].CatalogCode != "ability-1" {
		t.Fatal("exact match missing")
	}
	if search.calls != 0 || model.calls != 0 {
		t.Fatal("exact match should not call vector or model")
	}
}
func TestVectorNormalizerDoesNotAutoAcceptNearestVector(t *testing.T) {
	search := &candidateSearch{matches: []AbilityMatch{{Code: "agent", Name: "Agent 开发", CategoryCode: "ai"}}}
	model := &normalizeModel{result: map[string]any{"decisions": []any{map[string]any{"index": 0, "decision": "request_new", "reason": "具体框架不是宽泛的 Agent 开发", "candidate": map[string]any{"category_code": "ai", "definition": "多智能体框架", "aliases": []string{}, "nearest_candidate_codes": []string{}}}}}}
	n := &VectorNormalizer{Embedder: testEmbedder(t), Search: search, Model: model}
	result := Result{AbilityRequirements: []AbilityRequirement{{Operator: RequirementSingle, RequiredCount: 1, Evidence: "用过 AutoGen", Options: []AbilityRequirementOption{{RawLabel: "AutoGen", Evidence: "AutoGen", CatalogCode: "agent"}}}}}
	got, err := n.Normalize(context.Background(), uuid.New(), "key", "model", Catalog{Abilities: []AbilityOption{{Code: "agent", Name: "Agent 开发", CategoryCode: "ai"}}}, result)
	if err != nil {
		t.Fatal(err)
	}
	option := got.AbilityRequirements[0].Options[0]
	if option.CatalogCode != "" || option.Candidate == nil || option.Candidate.CategoryCode != "ai" {
		t.Fatalf("vector similarity auto accepted: %+v", option)
	}
	if search.calls != 1 || model.calls != 1 {
		t.Fatal("expected one vector recall and one model judgment")
	}
}
