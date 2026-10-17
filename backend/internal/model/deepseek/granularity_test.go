package deepseek

import (
	"strings"
	"testing"

	"github.com/LeoninCS/jobpilot-next/backend/internal/jdanalysis"
	"github.com/LeoninCS/jobpilot-next/backend/internal/jdsource"
)

func TestCompositeExtractionIsLocallySplitInsteadOfPublished(t *testing.T) {
	raw := "负责产品服务开发。\n熟悉MySQL、PostgreSQL、Redis。"
	blocks := jdsource.New(raw).Blocks
	calls := 0
	group := func(name string) map[string]any {
		return map[string]any{"operator": "single", "required_count": 1, "block_ids": []string{blocks[1].ID}, "options": []any{map[string]any{"name": name, "block_ids": []string{blocks[1].ID}}}}
	}
	c := stageTestClient(t, func(chatRequest) any {
		calls++
		if calls == 1 {
			return map[string]any{"is_jd": true, "responsibility_block_ids": []string{blocks[0].ID}, "ability_requirements": []any{group("MySQL、PostgreSQL、Redis等数据库")}}
		}
		return map[string]any{"ability_requirements": []any{group("MySQL"), group("PostgreSQL"), group("Redis")}}
	})
	got, err := c.ExtractJD(t.Context(), "test", "test", raw)
	if err != nil || calls != 2 || len(got.AbilityRequirements) != 3 {
		t.Fatalf("composite was not repaired: %v calls=%d %#v", err, calls, got)
	}
}

func TestPreferenceMustHaveLocalSourceProof(t *testing.T) {
	doc := jdsource.New("熟悉MySQL，了解UML者优先。")
	ref := modelRefs{doc.Blocks[0].ID}
	for _, tc := range []struct {
		name, proof string
		valid       bool
	}{
		{"MySQL", "熟悉MySQL，了解UML者优先", false},
		{"MySQL", "了解UML者优先", false},
		{"UML", "了解UML者优先", true},
		{"UML", "", false},
	} {
		r := anchoredRequirement{Operator: jdanalysis.RequirementSingle, Count: 1, Kind: "preferred", Refs: ref, KindEvidence: modelText(tc.proof), Options: []anchoredOption{{Name: modelText(tc.name), Refs: ref}}}
		if _, ok := resolveAnchoredRequirement(doc, r); ok != tc.valid {
			t.Errorf("preference scope %q %q valid=%v", tc.name, tc.proof, ok)
		}
	}
}

func TestPreferenceAllowsSourceLineWrappingAndTerminalPunctuation(t *testing.T) {
	for _, raw := range []string{"了解UML者\n优先。", "了解UML者优先。", "Node.js开发经验者\r\n优先。"} {
		doc := jdsource.New(raw)
		refs := modelRefs{}
		for _, b := range doc.Blocks {
			refs = append(refs, b.ID)
		}
		name := "UML"
		if strings.Contains(raw, "Node.js") {
			name = "Node.js"
		}
		item := anchoredRequirement{Operator: jdanalysis.RequirementSingle, Count: 1, Kind: "preferred", KindEvidence: modelText(raw), Refs: refs, Options: []anchoredOption{{Name: modelText(name), Refs: refs}}}
		got, ok := resolveAnchoredRequirement(doc, item)
		if !ok || got.Evidence != raw || got.RequirementKind != "preferred" {
			t.Errorf("formatting rejected a genuine clause or altered its quote: %q %#v", raw, got)
		}
	}
}

func TestFailedExtractionDoesNotReturnAnInvalidJDVerdict(t *testing.T) {
	raw := "负责服务开发。\n熟悉Go、MySQL。"
	doc := jdsource.New(raw)
	client := stageTestClient(t, func(chatRequest) any {
		return map[string]any{"is_jd": true, "responsibility_block_ids": []string{doc.Blocks[0].ID}, "ability_requirements": []any{map[string]any{"operator": "single", "required_count": 1, "block_ids": []string{doc.Blocks[1].ID}, "options": []any{map[string]any{"name": "Go、MySQL", "block_ids": []string{doc.Blocks[1].ID}}}}}}
	})
	got, err := client.ExtractJD(t.Context(), "test", "test", raw)
	if err == nil || got.ValidationStatus == jdanalysis.ValidationInvalid || got.DocumentType == jdanalysis.DocumentNonJobDescription || got.ValidationReason != "" {
		t.Fatalf("technical extraction error became a negative JD decision: %v %#v", err, got)
	}
}

func TestExtractionRejectsInventedOrCompositeNames(t *testing.T) {
	doc := jdsource.New("熟悉Go、MySQL、Redis与CI/CD。")
	ref := modelRefs{doc.Blocks[0].ID}
	for _, tc := range []struct {
		name  string
		valid bool
	}{{"Go", true}, {"CI/CD", true}, {"MySQL、Redis", false}, {"Go等语言", false}, {"PostgreSQL", false}} {
		r := anchoredRequirement{Operator: jdanalysis.RequirementSingle, Count: 1, Refs: ref, Options: []anchoredOption{{Name: modelText(tc.name), Refs: ref}}}
		if _, ok := resolveAnchoredRequirement(doc, r); ok != tc.valid {
			t.Errorf("name %q valid=%v", tc.name, ok)
		}
	}
}

func TestClassificationReasonCannotRejectItsOwnTarget(t *testing.T) {
	reason := "未体现规划、工具调用、状态管理或自主执行闭环，不满足Agent应用核心职责，故不归为次要分类。"
	if !classificationReasonDeniesTarget(reason, "Agent 应用") {
		t.Fatal("explicitly rejected classification was accepted")
	}
	if classificationReasonDeniesTarget("未体现工具闭环，不满足Agent应用核心职责，工作仍属于业务后端。", "业务后端") {
		t.Fatal("comparison with another specialty was mistaken for self-rejection")
	}
}
