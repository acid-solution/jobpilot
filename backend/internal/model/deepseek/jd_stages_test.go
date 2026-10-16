package deepseek

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LeoninCS/jobpilot-next/backend/internal/abilityreview"
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
