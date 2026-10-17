package deepseek

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LeoninCS/jobpilot-next/backend/internal/abilityreview"
	"github.com/LeoninCS/jobpilot-next/backend/internal/jdanalysis"
	"github.com/LeoninCS/jobpilot-next/backend/internal/jdsource"
)

func stageTestClient(t *testing.T, respond func(chatRequest) any) *Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request chatRequest
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			t.Error("invalid model request")
		}
		value, _ := json.Marshal(respond(request))
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"content": string(value)}}}})
	}))
	t.Cleanup(server.Close)
	return NewClient(server.URL, server.Client())
}

func TestIndependentAbilityReviewCorrectsInvalidJSONShapeOnce(t *testing.T) {
	calls := 0
	client := stageTestClient(t, func(request chatRequest) any {
		calls++
		if calls == 1 {
			return "invalid object shape"
		}
		if len(request.Messages) != 4 {
			t.Error("review correction did not include the original task and failed response")
		}
		return map[string]any{"decision": "reject", "reason": "候选不是可评估的技术能力"}
	})
	result, err := client.ReviewAbility(t.Context(), "test", "test", abilityreview.Input{Name: "团队精神", Evidence: []string{"具备团队精神"}})
	if err != nil || result.Decision != "reject" || calls != 2 || result.PromptVersion != abilityreview.PromptVersion {
		t.Fatalf("independent review correction lost decision or metadata: %v %#v calls=%d", err, result, calls)
	}
}
func TestExtractionUsesServerQuotedBlocksRatherThanReconstructedModifiers(t *testing.T) {
	raw := "后端开发工程师\n负责订单服务开发。\n熟悉数据结构和算法，较强的沟通能力和学习能力。"
	blocks := jdsource.New(raw).Blocks
	client := stageTestClient(t, func(chatRequest) any {
		return map[string]any{"is_jd": true, "title": "后端开发工程师", "responsibility_block_ids": []string{blocks[1].ID}, "condition_block_ids": []string{blocks[2].ID}, "ability_requirements": []any{map[string]any{"operator": "single", "required_count": 1, "block_ids": []string{blocks[2].ID}, "options": []any{map[string]any{"name": "算法", "block_ids": []string{blocks[2].ID}}}}}}
	})
	result, err := client.ExtractJD(t.Context(), "test", "test", raw)
	if err != nil {
		t.Fatal(err)
	}
	if result.ValidationStatus != "valid" || len(result.AbilityRequirements) != 1 {
		t.Fatalf("extraction lost: %#v", result)
	}
	quote := result.AbilityRequirements[0].Options[0].Evidence
	if quote != "熟悉数据结构和算法，较强的沟通能力和学习能力。" || !strings.Contains(raw, quote) {
		t.Fatalf("quote was rewritten: %q", quote)
	}
}

func TestClassifyJDUsesOnlyJobCatalogAndCorrectsInvalidPrimaryOnce(t *testing.T) {
	raw := "负责订单服务开发。"
	block := jdsource.New(raw).Blocks[0].ID
	catalog := testCatalog()
	catalog.Abilities = append(catalog.Abilities, jdanalysis.AbilityOption{Code: "sentinel", Name: "DO_NOT_SEND_ABILITY_MARKER"})
	calls := 0
	client := stageTestClient(t, func(request chatRequest) any {
		calls++
		if request.ResponseFormat.Type != "json_object" || request.Thinking.Type != "disabled" {
			t.Error("classification must use JSON mode without thinking")
		}
		prompt := request.Messages[1].Content
		for _, field := range []string{"definition", "include_signals", "exclude_signals", "confused_with"} {
			if !strings.Contains(prompt, field) {
				t.Errorf("catalog missing %s", field)
			}
		}
		if strings.Contains(prompt, "DO_NOT_SEND_ABILITY_MARKER") {
			t.Error("classification leaked ability catalog")
		}
		relation := "secondary"
		if calls == 2 {
			relation = "primary"
			if len(request.Messages) != 4 || request.Messages[2].Role != "assistant" || request.Messages[3].Role != "user" {
				t.Error("correction lost rejected result and feedback")
			}
		}
		return map[string]any{"classifications": []any{map[string]any{"category_code": "backend", "specialty_code": "backend-business", "relation": relation, "block_ids": []string{block}, "reason": "交付订单业务服务，而非通用框架"}}}
	})
	result, err := client.ClassifyJD(t.Context(), "test-key", "test-model", raw, jdanalysis.Result{Title: "后端开发", Responsibilities: []string{raw}}, catalog)
	if err != nil || calls != 2 || len(result) != 1 || result[0].Relation != "primary" || result[0].Evidence != raw {
		t.Fatalf("classification correction failed: calls=%d result=%+v err=%v", calls, result, err)
	}
}

func TestQuotedRequirementRelations(t *testing.T) {
	doc := jdsource.New("熟悉 Go 或 Python。\n负责后端服务开发。")
	for _, tc := range []struct {
		name           string
		operator       jdanalysis.AbilityRequirementOperator
		count, options int
		outside, valid bool
	}{
		{"single", jdanalysis.RequirementSingle, 1, 1, false, true},
		{"any of", jdanalysis.RequirementAnyOf, 1, 2, false, true},
		{"at least two", jdanalysis.RequirementAtLeastN, 2, 2, false, true},
		{"at least one repaired", jdanalysis.RequirementAtLeastN, 1, 2, false, true},
		{"single multiple options", jdanalysis.RequirementSingle, 1, 2, false, false},
		{"any of one option", jdanalysis.RequirementAnyOf, 1, 1, false, false},
		{"unreachable count", jdanalysis.RequirementAtLeastN, 3, 2, false, false},
		{"evidence outside group", jdanalysis.RequirementSingle, 1, 1, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			item := anchoredRequirement{Operator: tc.operator, Count: tc.count, Refs: modelRefs{doc.Blocks[0].ID}}
			for i := 0; i < tc.options; i++ {
				ref := doc.Blocks[0].ID
				if tc.outside {
					ref = doc.Blocks[1].ID
				}
				item.Options = append(item.Options, anchoredOption{Name: modelText([]string{"Go", "Python"}[i]), Refs: modelRefs{ref}})
			}
			result, valid := resolveAnchoredRequirement(doc, item)
			if valid != tc.valid {
				t.Fatalf("valid=%v result=%+v", valid, result)
			}
			if valid && (result.Evidence != doc.Blocks[0].Text || result.Options[0].Evidence != doc.Blocks[0].Text) {
				t.Fatal("requirement lost verbatim source evidence")
			}
		})
	}
}

func TestRequirementDeduplicationPreservesDistinctEvidence(t *testing.T) {
	option := jdanalysis.AbilityRequirementOption{RawLabel: "Go", AbilityName: "Go", Evidence: "Go"}
	first := jdanalysis.AbilityRequirement{Operator: jdanalysis.RequirementSingle, RequiredCount: 1, Evidence: "Go 或 Python", Options: []jdanalysis.AbilityRequirementOption{option}}
	choice := first
	choice.Operator = jdanalysis.RequirementAnyOf
	choice.Options = append([]jdanalysis.AbilityRequirementOption{option}, jdanalysis.AbilityRequirementOption{RawLabel: "Python", Evidence: "Python"})
	other := first
	other.Evidence = "独立开发 Go 服务"
	result := removeRedundantAbilityRequirements([]jdanalysis.AbilityRequirement{first, choice, other})
	if len(result) != 2 || result[0].Operator != jdanalysis.RequirementAnyOf || result[1].Evidence != other.Evidence {
		t.Fatalf("duplicate cleanup changed independent requirement: %+v", result)
	}
}
func TestDetectorKeepsNovelAndRandomTextAsNormalEmptyResult(t *testing.T) {
	client := stageTestClient(t, func(request chatRequest) any {
		if !strings.Contains(request.Messages[0].Content, "小说") {
			t.Error("missing non-JD semantics")
		}
		return map[string]any{"candidates": []any{}}
	})
	for _, raw := range []string{"夜色渐深，他推开木门，远处传来钟声。明天的旅途依然没有答案。", "首页 登录 关于我们 联系方式 备案说明 zxq @@@ 123"} {
		candidates, err := client.DetectJDs(t.Context(), "test", "test", raw)
		if err != nil || len(candidates) != 0 {
			t.Fatalf("non-JD must be normal empty result: %v %#v", err, candidates)
		}
	}
}
func TestDetectorSeparatesJobsAndSkipsPageFurniture(t *testing.T) {
	raw := "首页 登录 帮助中心\n甲公司 后端工程师\n负责 Go 订单接口开发。\n乙公司 前端工程师\n负责 React 页面开发。\n备案号和热门推荐"
	b := jdsource.New(raw).Blocks
	client := stageTestClient(t, func(chatRequest) any {
		return map[string]any{"candidates": []any{map[string]any{"title": "后端工程师", "company": "甲公司", "block_ids": []string{b[1].ID, b[2].ID}}, map[string]any{"title": "前端工程师", "company": "乙公司", "block_ids": []string{b[3].ID, b[4].ID}}}}
	})
	got, err := client.DetectJDs(t.Context(), "test", "test", raw)
	if err != nil || len(got) != 2 {
		t.Fatalf("discovery: %v %#v", err, got)
	}
	if strings.Contains(got[0].RawText, "React") || strings.Contains(got[1].RawText, "Go") || strings.Contains(got[0].RawText, "帮助中心") {
		t.Fatal("cross-job or page noise contaminated a candidate")
	}
}
func TestExtractionRepairsOnlyInvalidGroupAndKeepsValidSibling(t *testing.T) {
	raw := "负责后端开发。\n熟悉 Go。\n掌握 Java 或 Python。"
	b := jdsource.New(raw).Blocks
	calls := 0
	client := stageTestClient(t, func(request chatRequest) any {
		calls++
		if calls == 1 {
			return map[string]any{"is_jd": true, "responsibility_block_ids": []string{b[0].ID}, "ability_requirements": []any{map[string]any{"operator": "single", "required_count": 1, "block_ids": []string{b[1].ID}, "options": []any{map[string]any{"name": "Go", "block_ids": []string{b[1].ID}}}}, map[string]any{"operator": "wrong", "block_ids": []string{b[2].ID}}}}
		}
		if strings.Contains(request.Messages[1].Content, "\"name\":\"Go\"") {
			t.Error("valid Go group was unnecessarily regenerated")
		}
		return map[string]any{"ability_requirements": []any{map[string]any{"operator": "any_of", "required_count": 1, "block_ids": []string{b[2].ID}, "options": []any{map[string]any{"name": "Java", "block_ids": []string{b[2].ID}}, map[string]any{"name": "Python", "block_ids": []string{b[2].ID}}}}}}
	})
	got, err := client.ExtractJD(t.Context(), "test", "test", raw)
	if err != nil || calls != 2 || len(got.AbilityRequirements) != 2 || got.AbilityRequirements[1].Operator != "any_of" {
		t.Fatalf("local recovery: calls=%d err=%v result=%#v", calls, err, got)
	}
}
