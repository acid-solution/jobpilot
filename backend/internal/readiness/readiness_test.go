package readiness

import (
	"testing"

	"github.com/google/uuid"
)

func TestAdmissionStatesAreDistinct(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		state                     State
		information               bool
		code                      string
		market, profile, analysis bool
	}{
		{"nine JDs", State{IncludedJDCount: 9}, true, "market_incomplete", false, false, false},
		{"waiting review", State{IncludedJDCount: 10, PendingReviewCount: 1}, true, "ability_review_pending", true, true, false},
		{"failed grading", State{IncludedJDCount: 10, FailedGradingCount: 1}, true, "grading_pending", true, true, false},
		{"missing information", State{IncludedJDCount: 10}, false, "profile_incomplete", true, false, false},
		{"unassessed", State{IncludedJDCount: 10, MissingAbilityCount: 1}, true, "profile_incomplete", true, false, false},
		{"ready", State{IncludedJDCount: 10}, true, "ready", true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Evaluate(tc.state, tc.information)
			if got.Code != tc.code || got.MarketComplete != tc.market || got.ProfileComplete != tc.profile || got.AnalysisReady != tc.analysis {
				t.Fatalf("unexpected admission: %+v", got)
			}
		})
	}
}

func TestCombinationAssessmentDoesNotRequireUnusedPaths(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	known := Option{AbilityID: a, Resolved: true, Assessed: true, Graded: true, CurrentLevel: 3, RequiredLevel: 3}
	unknown := Option{AbilityID: b, Resolved: true, Graded: true, RequiredLevel: 2}
	if !Decidable(1, []Option{known, unknown}) {
		t.Fatal("satisfied choice unnecessarily requires all paths")
	}
	if Decidable(2, []Option{known, known, unknown}) {
		t.Fatal("duplicate ability satisfied an at-least-two requirement")
	}
	known.CurrentLevel = 0
	if !Decidable(1, []Option{known}) {
		t.Fatal("explicit L0 was treated as unassessed")
	}
	if Decidable(1, []Option{known, unknown}) {
		t.Fatal("unsatisfied choice with unknown alternatives was treated as decidable")
	}
}
