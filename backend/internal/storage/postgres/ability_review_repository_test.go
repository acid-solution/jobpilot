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
	valid := abilityreview.Result{Decision: "approve_new", Reason: "独立框架", NewAbility: abilityreview.NewAbility{Name: "AutoGen", CategoryCode: "ability-category-07", Definition: "多智能体框架", Levels: []abilityreview.Level{{0, "未学习"}, {1, "了解"}, {2, "基础使用"}, {3, "独立开发"}, {4, "复杂场景"}, {5, "体系设计"}}}}
	if err := validateReviewResult(valid, catalog); err != nil {
		t.Fatalf("valid result rejected: %v", err)
	}
	valid.NewAbility.Levels = valid.NewAbility.Levels[:5]
	if err := validateReviewResult(valid, catalog); err == nil {
		t.Fatal("expected missing level to fail")
	}
}
