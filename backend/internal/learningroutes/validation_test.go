package learningroutes

import (
	"context"
	"encoding/json"
	"github.com/LeoninCS/jobpilot-next/backend/internal/knowledgegaps"
	"github.com/LeoninCS/jobpilot-next/backend/internal/modelconfig"
	"github.com/LeoninCS/jobpilot-next/backend/internal/projectrecs"
	"github.com/google/uuid"
	"strings"
	"testing"
)

func samplePlans() []Plan {
	return []Plan{{Role: "project", Title: "项目", Objective: "演示主体流程", Stages: []Stage{{Title: "阶段", Outcome: "可运行结果", Tasks: []Task{{ID: "p1", Title: "实现接口", Description: "实现并验证接口", CompletionCriteria: "测试通过", EstimateMinutes: 60}}}}}, {Role: "knowledge", Title: "复习", Objective: "复习与验证", Stages: []Stage{{Title: "阶段", Outcome: "验证记录", Tasks: []Task{{ID: "k1", Title: "复习 Go", Description: "解释并发案例", CompletionCriteria: "给出验证结果", EstimateMinutes: 30}}}}}}
}
func sampleConfig() Config {
	return Config{Project: PlanConfig{Mode: "sequence", WeeklyMinutes: 120}, Knowledge: PlanConfig{Mode: "sequence", WeeklyMinutes: 60}}
}
func TestBudgetsAndScheduleNeverExtendPeriod(t *testing.T) {
	plans, cfg := samplePlans(), sampleConfig()
	if _, e := validatePlans(plans, cfg, 3, 2); e != nil {
		t.Fatal(e)
	}
	if _, e := validatePlans(plans, cfg, 2, 2); e == nil {
		t.Fatal("shared budget exceeded")
	}
	cfg.Project = PlanConfig{Mode: "calendar", WeeklyMinutes: 60, DailyMinutes: 60, StartDate: "2026-10-13", LearningDays: []int{2}}
	if e := schedule(plans, cfg, 1); e != nil {
		t.Fatal(e)
	}
	if plans[0].Stages[0].Tasks[0].ScheduledDate != "2026-10-13" || plans[1].Stages[0].Tasks[0].ScheduledDate != "" {
		t.Fatal("incorrect date semantics")
	}
	if _, e := validatePlans(plans, cfg, 3, 1); e != nil {
		t.Fatal(e)
	}
	plans[0].Stages[0].Tasks = append(plans[0].Stages[0].Tasks, Task{ID: "p2", Title: "另一任务", Description: "做事", CompletionCriteria: "检查", EstimateMinutes: 30})
	if e := schedule(plans, cfg, 1); e == nil {
		t.Fatal("deadline silently extended")
	}
}
func TestAdvisoryDependencyAndCycle(t *testing.T) {
	plans := samplePlans()
	plans[0].Stages[0].Tasks[0].DependsOn = []string{"k1"}
	warnings, e := validatePlans(plans, sampleConfig(), 3, 2)
	if e != nil || len(warnings) != 1 {
		t.Fatal("manual inversion should warn rather than block", e)
	}
	plans[1].Stages[0].Tasks[0].DependsOn = []string{"p1"}
	if _, e = validatePlans(plans, sampleConfig(), 3, 2); e == nil {
		t.Fatal("cycle accepted")
	}
}

type modelFixture struct{ value string }

func (m modelFixture) GenerateJSON(_ context.Context, _, _, _, _ string, out any) error {
	return json.Unmarshal([]byte(m.value), out)
}

type credsFixture struct{}

func (credsFixture) Credentials(context.Context, uuid.UUID) (modelconfig.Credentials, error) {
	return modelconfig.Credentials{APIKey: "test-only", Model: "mock"}, nil
}
func TestKnowledgeUpdatePreservesProjectExactly(t *testing.T) {
	original := samplePlans()
	original[0].Description = "手工编辑的项目说明"
	raw, _ := json.Marshal(struct {
		Plans []Plan `json:"plans"`
	}{Plans: []Plan{original[1]}})
	worker := &Worker{credentials: credsFixture{}, model: modelFixture{string(raw)}}
	input := Input{Profile: projectrecs.Input{WeeklyHours: 3, ExpectedWeeks: 2}, Config: sampleConfig(), Previous: original, KnowledgeOnly: true}
	plans, e := worker.generate(t.Context(), Job{Input: input, UserID: uuid.New()})
	if e != nil {
		t.Fatal(e)
	}
	if Hash(plans[0]) != Hash(original[0]) {
		t.Fatal("project content modified")
	}
}

func TestCalendarDependencyAcrossPlansAndPreservedProject(t *testing.T) {
	plans, cfg := samplePlans(), sampleConfig()
	cfg.Project = PlanConfig{Mode: "calendar", StartDate: "2026-10-13", WeeklyMinutes: 120, DailyMinutes: 60, LearningDays: []int{2, 3}}
	cfg.Knowledge = PlanConfig{Mode: "calendar", StartDate: "2026-10-14", WeeklyMinutes: 60, DailyMinutes: 60, LearningDays: []int{3}}
	plans[0].Stages[0].Tasks[0].DependsOn = []string{"k1"}
	if err := schedule(plans, cfg, 2); err != nil {
		t.Fatal(err)
	}
	if plans[0].Stages[0].Tasks[0].ScheduledDate < plans[1].Stages[0].Tasks[0].ScheduledDate {
		t.Fatal("later-plan dependency ignored")
	}
	preserved := samplePlans()[0]
	preserved.Stages[0].Tasks[0].ScheduledDate = "2026-10-21"
	knowledge := []Plan{samplePlans()[1]}
	knowledge[0].Stages[0].Tasks[0].DependsOn = []string{"p1"}
	before := Hash(preserved)
	if err := schedule(knowledge, cfg, 2, preserved); err != nil {
		t.Fatal(err)
	}
	if knowledge[0].Stages[0].Tasks[0].ScheduledDate != "2026-10-21" || Hash(preserved) != before {
		t.Fatal("preserved dates lost or edited")
	}
}

func TestOnlyUnmetSufficientCombinationPathsAreRequired(t *testing.T) {
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	input := Input{Knowledge: knowledgegaps.Report{Gaps: []knowledgegaps.Item{{ID: "gap", Kind: "requirement", RequiredCount: 2, SatisfiedCount: 1, Options: []knowledgegaps.OptionState{{AbilityID: a, Satisfied: true}, {AbilityID: b}, {AbilityID: c}}}}}}
	input.Config.Paths = []PathChoice{{GapID: "gap", AbilityIDs: []uuid.UUID{b}}}
	if err := validatePaths(input); err != nil {
		t.Fatal(err)
	}
	input.Config.Paths[0].AbilityIDs = []uuid.UUID{b, c}
	if err := validatePaths(input); err == nil {
		t.Fatal("all candidates treated as mandatory")
	}
	input.Knowledge.Met = []knowledgegaps.Item{{ID: "met", Kind: "requirement", RequiredCount: 1, SatisfiedCount: 1, Options: []knowledgegaps.OptionState{{AbilityID: a, Satisfied: true}, {AbilityID: b}}}}
	filtered, changed, err := planningPaths(input, []PathChoice{{GapID: "met", AbilityIDs: []uuid.UUID{a}}})
	if err != nil || !changed || len(filtered) != 0 {
		t.Fatal("satisfied path not removed", err)
	}
	if _, _, err = planningPaths(input, []PathChoice{{GapID: "met", AbilityIDs: []uuid.UUID{b}}}); err == nil {
		t.Fatal("unsatisfied alternative accepted as satisfied")
	}
}

func TestCheckpointResumeDoesNotCallModelOrReadCredentials(t *testing.T) {
	// Nil dependencies make any accidental paid-stage call fail this test.
	w := &Worker{}
	plans, err := w.generate(t.Context(), Job{ModelPlans: samplePlans(), Input: Input{Config: sampleConfig(), Profile: projectrecs.Input{WeeklyHours: 3, ExpectedWeeks: 2}}})
	if err != nil || len(plans) != 2 {
		t.Fatal("checkpoint could not resume independently", err)
	}
}

func TestMalformedRoleAndOversizedWireDescriptionAreRejected(t *testing.T) {
	plans, cfg := samplePlans(), sampleConfig()
	plans[0].Role = "unknown"
	if err := schedule(plans, cfg, 2); err == nil {
		t.Fatal("unknown model role accepted")
	}
	plans = samplePlans()
	plans[0].Stages[0].Tasks[0].Title = strings.Repeat("x", 255)
	plans[1].Stages[0].Tasks[0].Description = strings.Repeat("x", 1700)
	plans[1].Stages[0].Tasks[0].DependsOn = []string{"p1"}
	if _, err := validatePlans(plans, cfg, 3, 2); err == nil {
		t.Fatal("wire description exceeds StudyFlow limit")
	}
}
