package market

import "testing"

func TestBuildLevelSummaryUsesModeAndHigherRequirements(t *testing.T) {
	input := []LevelEvidence{{Level: 2, Source: "inferred"}, {Level: 3, Source: "explicit"}, {Level: 3, Source: "inferred"}, {Level: 4, Source: "explicit"}}
	result := BuildLevelSummary(input, "ready")
	if len(result.CommonLevels) != 1 || result.CommonLevels[0] != 3 || result.RecommendedLevel != 3 || result.HigherRequirementCount != 1 {
		t.Fatalf("unexpected summary: %+v", result)
	}
	if result.ExplicitCount != 2 || result.InferredCount != 2 || result.SampleCount != 4 {
		t.Fatalf("unexpected source counts: %+v", result)
	}
}

func TestBuildLevelSummaryKeepsTiedModesAndUsesHigherForComparison(t *testing.T) {
	result := BuildLevelSummary([]LevelEvidence{{Level: 2, Source: "explicit"}, {Level: 3, Source: "inferred"}}, "ready")
	if len(result.CommonLevels) != 2 || result.CommonLevels[0] != 2 || result.CommonLevels[1] != 3 || result.RecommendedLevel != 3 {
		t.Fatalf("unexpected tied summary: %+v", result)
	}
}

func TestBuildLevelSummaryReturnsSingleSample(t *testing.T) {
	result := BuildLevelSummary([]LevelEvidence{{Level: 3, Source: "explicit"}}, "ready")
	if result.SampleCount != 1 || result.RecommendedLevel != 3 || result.Status != "ready" {
		t.Fatalf("unexpected single-sample summary: %+v", result)
	}
}
