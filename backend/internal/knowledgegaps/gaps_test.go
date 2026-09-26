package knowledgegaps

import (
	"testing"

	"github.com/LeoninCS/jobpilot-next/backend/internal/market"
	"github.com/LeoninCS/jobpilot-next/backend/internal/profile"
	"github.com/LeoninCS/jobpilot-next/backend/internal/target"
	"github.com/google/uuid"
)

func TestBuildRespectsChoiceAndDistinctAbilities(t *testing.T) {
	goID, pythonID, redisID := uuid.New(), uuid.New(), uuid.New()
	jd1, jd2 := uuid.New(), uuid.New()
	reqs := []Requirement{
		{ID: uuid.New(), JDID: jd1, Operator: "any_of", RequiredCount: 1, Kind: "required", Options: []Option{{ID: uuid.New(), AbilityID: goID, Name: "Go", Level: 3, Graded: true, Resolution: "resolved"}, {ID: uuid.New(), AbilityID: pythonID, Name: "Python", Level: 3, Graded: true, Resolution: "resolved"}}},
		{ID: uuid.New(), JDID: jd2, Operator: "at_least_n", RequiredCount: 2, Kind: "required", Options: []Option{{ID: uuid.New(), AbilityID: goID, Name: "Go", Level: 2, Graded: true, Resolution: "resolved"}, {ID: uuid.New(), AbilityID: goID, Name: "Go", Level: 2, Graded: true, Resolution: "resolved"}, {ID: uuid.New(), AbilityID: redisID, Name: "Redis", Level: 2, Graded: true, Resolution: "resolved"}}},
	}
	users := []profile.Capability{{AbilityID: goID, CurrentLevel: 3, Assessed: true}, {AbilityID: redisID, CurrentLevel: 1, Assessed: true}}
	r := Build(uuid.New(), reqs, nil, users)
	if len(r.Met) != 1 || len(r.Gaps) != 1 {
		t.Fatalf("want one met choice and one unsatisfied two-of group: %+v", r)
	}
	if r.Gaps[0].SatisfiedCount != 1 {
		t.Fatalf("duplicate Go options must count once: %+v", r.Gaps[0])
	}
	index := map[uuid.UUID]profile.Capability{}
	for _, a := range users {
		index[a.AbilityID] = a
	}
	if !groupDecidable(reqs[0], index) {
		t.Fatal("satisfied Go path must not require Python assessment")
	}
}
func TestGoalSignatureSurvivesInPlaceTargetChange(t *testing.T) {
	category := uuid.New()
	a := target.Target{ID: uuid.New(), EmploymentType: "internship", Directions: []target.Direction{{CategoryID: category}}}
	b := a
	b.ID = uuid.New()
	if signature(a) != signature(b) {
		t.Fatal("same goal with a new row ID should restore its report")
	}
	b.EmploymentType = "campus"
	if signature(a) == signature(b) {
		t.Fatal("different goal reused a report")
	}
}

func TestSingleUsesMarketModeAndDeduplicatesJD(t *testing.T) {
	goID, jdID := uuid.New(), uuid.New()
	q := Requirement{ID: uuid.New(), JDID: jdID, Operator: "single", RequiredCount: 1, Kind: "required", Options: []Option{{ID: uuid.New(), AbilityID: goID, Name: "Go", Level: 5, Graded: true, Resolution: "resolved"}}}
	m := []market.AbilitySummary{{AbilityID: goID, Name: "Go", LevelSummary: market.LevelSummary{RecommendedLevel: 3}}}
	r := Build(uuid.New(), []Requirement{q, q}, m, []profile.Capability{{AbilityID: goID, CurrentLevel: 2, Assessed: true, LevelSource: "manual"}})
	if len(r.Gaps) != 1 || r.Gaps[0].TargetLevel != 3 || r.Gaps[0].SampleCount != 1 {
		t.Fatalf("unexpected market-level gap: %+v", r.Gaps)
	}
}

func TestPreferredDoesNotBecomeRequiredGap(t *testing.T) {
	id := uuid.New()
	q := Requirement{ID: uuid.New(), JDID: uuid.New(), Operator: "single", Kind: "preferred", Options: []Option{{ID: uuid.New(), AbilityID: id, Name: "RAG", Level: 4, Graded: true, Resolution: "resolved"}}}
	r := Build(uuid.New(), []Requirement{q}, nil, []profile.Capability{{AbilityID: id, CurrentLevel: 0, Assessed: true}})
	if len(r.Gaps) != 0 || len(r.Preferred) != 1 {
		t.Fatalf("preferred requirement mixed with required: %+v", r)
	}
}
func TestEquivalentChoiceAcrossJDsIsOneReportItem(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	makeRequirement := func(jd uuid.UUID) Requirement {
		return Requirement{ID: uuid.New(), JDID: jd, Operator: "any_of", RequiredCount: 1, Kind: "required", Options: []Option{{ID: uuid.New(), AbilityID: a, Name: "Go", Level: 3, Graded: true, Resolution: "resolved"}, {ID: uuid.New(), AbilityID: b, Name: "Python", Level: 3, Graded: true, Resolution: "resolved"}}}
	}
	r := Build(uuid.New(), []Requirement{makeRequirement(uuid.New()), makeRequirement(uuid.New())}, nil, []profile.Capability{{AbilityID: a, CurrentLevel: 2, Assessed: true}, {AbilityID: b, CurrentLevel: 2, Assessed: true}})
	if len(r.Gaps) != 1 || r.Gaps[0].SampleCount != 2 {
		t.Fatalf("equivalent requirements not consolidated: %+v", r.Gaps)
	}
}
