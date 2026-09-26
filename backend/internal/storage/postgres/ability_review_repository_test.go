package postgres

import (
	"testing"

	"github.com/LeoninCS/jobpilot-next/backend/internal/abilityreview"
)

func TestNormalizeAbilityNameUnifiesCommonSeparators(t *testing.T) {
	values := []string{"AutoGen", "auto gen", "AUTO-GEN", "ＡｕｔｏＧｅｎ"}
	for _, value := range values {
		if got := NormalizeAbilityName(value); got != "autogen" {
			t.Fatalf("NormalizeAbilityName(%q)=%q", value, got)
		}
	}
}

func TestValidateReviewResultRequiresExactSixLevels(t *testing.T) {
	catalog := []abilityreview.CatalogAbility{{Code: "ability-agent", CategoryCode: "ability-category-07"}}
	valid := abilityreview.Result{Decision: "approve_new", Reason: "独立框架", NewAbility: abilityreview.NewAbility{Name: "AutoGen", CategoryCode: "ability-category-07", Definition: "多智能体框架", Levels: []abilityreview.Level{{Level: 0, Description: "未学习"}, {Level: 1, Description: "了解"}, {Level: 2, Description: "基础使用"}, {Level: 3, Description: "独立开发"}, {Level: 4, Description: "复杂场景"}, {Level: 5, Description: "体系设计"}}}}
	if err := validateReviewResult(valid, catalog); err != nil {
		t.Fatalf("valid result rejected: %v", err)
	}
	valid.NewAbility.Levels = valid.NewAbility.Levels[:5]
	if err := validateReviewResult(valid, catalog); err == nil {
		t.Fatal("expected missing level to fail")
	}
}
