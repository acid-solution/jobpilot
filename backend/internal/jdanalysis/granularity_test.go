package jdanalysis

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestProtocolScopeMergeKeepsAllEvidenceAndDoesNotBecomeAliases(t *testing.T) {
	evidence := "HTTP、SSH、SFTP至少掌握两项"
	model := normalizationFunction(func(_ context.Context, _, _, _, prompt string, out any) error {
		if !strings.Contains(prompt, `"include_signals"`) || !strings.Contains(prompt, "常见协议") {
			t.Error("candidate lost scope boundaries")
		}
		decisions := []map[string]any{}
		for i := range 3 {
			decisions = append(decisions, map[string]any{"index": i, "decision": "reuse_existing", "relation": "within_scope", "existing_ability_code": "network", "reason": "协议交互属于候选说明的内容范围"})
		}
		data, _ := json.Marshal(map[string]any{"decisions": decisions})
		return json.Unmarshal(data, out)
	})
	n := VectorNormalizer{Model: model}
	catalog := Catalog{Abilities: []AbilityOption{{Code: "network", Name: "计算机网络", IncludeSignals: []string{"常见协议HTTP、SSH、SFTP"}}}}
	input := Result{AbilityRequirements: []AbilityRequirement{{Operator: RequirementAtLeastN, RequiredCount: 2, Evidence: evidence, Options: []AbilityRequirementOption{{RawLabel: "HTTP", Evidence: "HTTP"}, {RawLabel: "SSH", Evidence: "SSH"}, {RawLabel: "SFTP", Evidence: "SFTP"}}}}}
	got, err := n.Normalize(t.Context(), uuid.New(), "test", "test", catalog, input)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.AbilityRequirements) != 1 {
		t.Fatalf("real requirement was deleted: %#v", got)
	}
	r := got.AbilityRequirements[0]
	if r.Operator != RequirementSingle || r.RequiredCount != 1 || len(r.Options) != 1 || len(r.Options[0].ScopeDetails) != 3 || r.Options[0].Evidence != evidence || len(got.AliasProposals) != 0 {
		t.Fatalf("scope or counting rule lost: %#v", got)
	}
}

func TestExactAliasesAreDeduplicatedWithoutCallingModel(t *testing.T) {
	model := normalizationFunction(func(_ context.Context, _, _, _, _ string, _ any) error {
		t.Fatal("exact names and aliases must not call the model")
		return nil
	})
	n := VectorNormalizer{Model: model}
	input := Result{AbilityRequirements: []AbilityRequirement{{Operator: RequirementAtLeastN, RequiredCount: 2, Evidence: "Go和Golang至少掌握两项", Options: []AbilityRequirementOption{{RawLabel: "Go", Evidence: "Go"}, {RawLabel: "Golang", Evidence: "Golang"}}}}}
	got, err := n.Normalize(t.Context(), uuid.New(), "test", "test", Catalog{Abilities: []AbilityOption{{Code: "go", Name: "Go", Aliases: []string{"Golang"}}}}, input)
	if err != nil {
		t.Fatal(err)
	}
	r := got.AbilityRequirements[0]
	if len(got.AbilityMentions) != 1 || r.Operator != RequirementSingle || r.RequiredCount != 1 || len(r.Options) != 1 || len(r.Options[0].ScopeDetails) != 2 {
		t.Fatalf("exact alias path bypassed deduplication: %#v", got)
	}
}

func TestPendingReviewIsNotAPublicAbility(t *testing.T) {
	model := &normalizeModel{result: map[string]any{"decisions": []any{map[string]any{"index": 0, "decision": "request_new", "reason": "缺少具体框架", "candidate": map[string]any{"category_code": "ai", "definition": "新的独立框架"}}}}}
	n := VectorNormalizer{Model: model}
	input := Result{AbilityRequirements: []AbilityRequirement{{Operator: RequirementSingle, RequiredCount: 1, Evidence: "使用NewFramework", Options: []AbilityRequirementOption{{RawLabel: "NewFramework", Evidence: "使用NewFramework"}}}}}
	got, err := n.Normalize(t.Context(), uuid.New(), "test", "test", Catalog{Abilities: []AbilityOption{{Code: "llm", Name: "大模型基础", CategoryCode: "ai"}}}, input)
	if err != nil || len(got.AbilityMentions) != 0 || len(got.AbilityRequirements) != 1 || got.AbilityRequirements[0].Options[0].Candidate == nil {
		t.Fatalf("pending label leaked or disappeared: %v %#v", err, got)
	}
}

func TestAmbiguousLabelRecallsItsOwnSourceContext(t *testing.T) {
	model := normalizationFunction(func(_ context.Context, _, _, _, prompt string, out any) error {
		if !strings.Contains(prompt, `"code":"llm"`) {
			t.Error("source context candidate was absent")
		}
		return json.Unmarshal([]byte(`{"decisions":[{"index":0,"decision":"reuse_existing","relation":"within_scope","existing_ability_code":"llm","reason":"该段明确处于LLM应用的语境"}]}`), out)
	})
	n := VectorNormalizer{Model: model}
	input := Result{AbilityRequirements: []AbilityRequirement{{Operator: RequirementSingle, RequiredCount: 1, Evidence: "了解LLM常见应用，对AI应用有基础认知", Options: []AbilityRequirementOption{{RawLabel: "AI", Evidence: "了解LLM常见应用，对AI应用有基础认知"}}}}}
	got, err := n.Normalize(t.Context(), uuid.New(), "test", "test", Catalog{Abilities: []AbilityOption{{Code: "llm", Name: "大模型基础", Aliases: []string{"LLM"}}}}, input)
	if err != nil || got.AbilityRequirements[0].Options[0].CatalogCode != "llm" {
		t.Fatalf("context recall failed: %v %#v", err, got)
	}
}
