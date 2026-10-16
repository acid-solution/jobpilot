package jdanalysis

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LeoninCS/jobpilot-next/backend/internal/embedding"
	"github.com/google/uuid"
)

type candidateSearch struct {
	matches []AbilityMatch
	calls   int
}

type normalizationFunction func(context.Context, string, string, string, string, any) error

func (f normalizationFunction) GenerateJSON(ctx context.Context, key, model, system, prompt string, output any) error {
	return f(ctx, key, model, system, prompt, output)
}
func TestNormalizerCorrectsOnlyMissingDecisions(t *testing.T) {
	calls := 0
	model := normalizationFunction(func(_ context.Context, _, _, _, prompt string, output any) error {
		index := 0
		calls++
		if calls == 2 {
			index = 1
			if strings.Contains(prompt, "AutoGen") {
				t.Error("already accepted identity decision was regenerated")
			}
		}
		data, _ := json.Marshal(map[string]any{"decisions": []any{map[string]any{"index": index, "decision": "request_new", "reason": "目录没有该具体工具", "candidate": map[string]any{"category_code": "ai", "definition": "可独立评估的技术工具"}}}})
		return json.Unmarshal(data, output)
	})
	normalizer := &VectorNormalizer{Model: model}
	input := Result{AbilityRequirements: []AbilityRequirement{{Operator: RequirementAnyOf, RequiredCount: 1, Evidence: "AutoGen 或 LangGraph", Options: []AbilityRequirementOption{{RawLabel: "AutoGen", Evidence: "AutoGen"}, {RawLabel: "LangGraph", Evidence: "LangGraph"}}}}}
	result, err := normalizer.Normalize(t.Context(), uuid.New(), "test", "test", Catalog{Abilities: []AbilityOption{{Code: "agent", Name: "Agent 开发", CategoryCode: "ai"}}}, input)
	if err != nil || calls != 2 || len(result.AbilityRequirements) != 1 || len(result.AbilityRequirements[0].Options) != 2 {
		t.Fatalf("local identity recovery: %v %#v calls=%d", err, result, calls)
	}
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

func TestVectorNormalizerMissingVectorsStillRequestsIndependentReview(t *testing.T) {
	search := &candidateSearch{}
	model := &normalizeModel{result: map[string]any{"decisions": []any{map[string]any{"index": 0, "decision": "request_new", "reason": "当前目录没有该框架", "candidate": map[string]any{"category_code": "ai", "definition": "多智能体应用框架", "aliases": []string{}, "nearest_candidate_codes": []string{}}}}}}
	n := &VectorNormalizer{Embedder: testEmbedder(t), Search: search, Model: model}
	result := Result{AbilityRequirements: []AbilityRequirement{{Operator: RequirementSingle, RequiredCount: 1, Evidence: "熟悉 AutoGen", Options: []AbilityRequirementOption{{RawLabel: "AutoGen", Evidence: "AutoGen"}}}}}
	got, err := n.Normalize(context.Background(), uuid.New(), "key", "model", Catalog{Abilities: []AbilityOption{{Code: "agent", Name: "Agent 开发", CategoryCode: "ai"}}}, result)
	if err != nil || got.AbilityRequirements[0].Options[0].Candidate == nil || got.AbilityRequirements[0].Options[0].CatalogCode != "" {
		t.Fatalf("missing vectors must still route a real candidate to independent review: %v %#v", err, got)
	}
}

func TestVectorNormalizerRejectsIncompleteConfiguration(t *testing.T) {
	_, err := (&VectorNormalizer{}).Normalize(context.Background(), uuid.New(), "key", "model", Catalog{}, Result{})
	var classified *ClassifiedError
	if !errors.As(err, &classified) || classified.FailureCode != "ability_normalization_unavailable" {
		t.Fatalf("incomplete normalizer must not silently succeed: %v", err)
	}
}

func TestVectorNormalizerKeepsAliasProposalsBeforeOptionDeduplication(t *testing.T) {
	search := &candidateSearch{matches: []AbilityMatch{{Code: "go", Name: "Go"}}}
	model := &normalizeModel{result: map[string]any{"decisions": []any{
		map[string]any{"index": 0, "decision": "reuse_existing", "existing_ability_code": "go", "reason": "第一条关联理由"},
		map[string]any{"index": 1, "decision": "reuse_existing", "existing_ability_code": "go", "reason": "第二条关联理由"},
	}}}
	n := &VectorNormalizer{Embedder: testEmbedder(t), Search: search, Model: model}
	result := Result{AbilityRequirements: []AbilityRequirement{{Operator: RequirementAnyOf, RequiredCount: 1, Evidence: "Go语言或者Go并发编程", Options: []AbilityRequirementOption{{RawLabel: "Go语言", Evidence: "Go语言"}, {RawLabel: "Go并发编程", Evidence: "Go并发编程"}}}}}
	got, err := n.Normalize(context.Background(), uuid.New(), "key", "model", Catalog{Abilities: []AbilityOption{{Code: "go", Name: "Go"}}}, result)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.AbilityRequirements[0].Options) != 1 || len(got.AliasProposals) != 2 || got.AliasProposals[1].Reason != "第二条关联理由" {
		t.Fatalf("alias evidence was lost during business deduplication: %+v", got)
	}
}
