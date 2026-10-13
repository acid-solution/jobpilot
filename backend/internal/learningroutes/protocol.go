package learningroutes

import "strings"

// These JSON fields match StudyFlow's versioned route contract exactly. Local
// task IDs and prerequisite hints are rendered into descriptions, not remote locks.
type WireTask struct {
	Title           string  `json:"title"`
	Description     string  `json:"description,omitempty"`
	EstimateMinutes uint    `json:"estimate_minutes,omitempty"`
	ScheduledDate   *string `json:"scheduled_date,omitempty"`
}
type WireStage struct {
	Title   string     `json:"title"`
	Outcome string     `json:"outcome,omitempty"`
	Tasks   []WireTask `json:"tasks,omitempty"`
}
type WirePlan struct {
	GoalID                string      `json:"goal_id"`
	Title                 string      `json:"title"`
	Description           string      `json:"description"`
	Objective             string      `json:"objective"`
	SuccessCriteria       string      `json:"success_criteria"`
	TargetDate            *string     `json:"target_date"`
	Mode                  string      `json:"mode"`
	WeeklyCapacityMinutes uint        `json:"weekly_capacity_minutes"`
	StartDate             *string     `json:"start_date"`
	EndDate               *string     `json:"end_date"`
	Milestones            []WireStage `json:"milestones"`
	Tasks                 []WireTask  `json:"tasks"`
}
type WireEntry struct {
	Role string   `json:"role"`
	Plan WirePlan `json:"plan"`
}
type Payload struct {
	SourceRouteID      string      `json:"source_route_id"`
	SourceVersion      uint64      `json:"source_version"`
	GoalTitle          string      `json:"goal_title"`
	GoalDescription    string      `json:"goal_description"`
	WeeklyTotalMinutes uint        `json:"weekly_total_minutes"`
	Plans              []WireEntry `json:"plans"`
}
type PlanRef struct {
	Role                  string `json:"role"`
	PlanID                string `json:"plan_id"`
	PlanVersionID         string `json:"plan_version_id"`
	VersionNo             uint   `json:"version_no"`
	ContentDigest         string `json:"content_digest"`
	WeeklyCapacityMinutes uint   `json:"weekly_capacity_minutes"`
}
type Receipt struct {
	ImportID      string    `json:"import_id"`
	SourceRouteID string    `json:"source_route_id"`
	SourceVersion uint64    `json:"source_version"`
	ContentDigest string    `json:"content_digest"`
	GoalID        string    `json:"goal_id"`
	Plans         []PlanRef `json:"plans"`
	Replayed      bool      `json:"replayed"`
}

func BuildPayload(v Version, goal string, hours int) Payload {
	out := Payload{SourceRouteID: v.RouteID.String(), SourceVersion: uint64(v.Number), GoalTitle: goal, GoalDescription: "由 JobPilot 规划的项目实践与知识准备路线", WeeklyTotalMinutes: uint(hours * 60), Plans: []WireEntry{}}
	titles := map[string]string{}
	for _, p := range v.Plans {
		for _, m := range p.Stages {
			for _, t := range m.Tasks {
				titles[t.ID] = t.Title
			}
		}
	}
	for _, p := range v.Plans {
		c := configFor(v.Config, p.Role)
		wire := WirePlan{Title: p.Title, Description: p.Description, Objective: p.Objective, SuccessCriteria: "完成各阶段成果与任务验收标准", Mode: c.Mode, WeeklyCapacityMinutes: uint(c.WeeklyMinutes), Milestones: []WireStage{}, Tasks: []WireTask{}}
		if c.Mode == "calendar" {
			value := c.StartDate
			wire.StartDate = &value
		}
		for _, m := range p.Stages {
			stage := WireStage{Title: m.Title, Outcome: m.Outcome, Tasks: []WireTask{}}
			for _, t := range m.Tasks {
				desc := t.Description + "\n完成标准：" + t.CompletionCriteria
				if len(t.DependsOn) > 0 {
					names := []string{}
					for _, dep := range t.DependsOn {
						names = append(names, titles[dep])
					}
					desc += "\n前置提示（不限制推进）：" + strings.Join(names, "、")
				}
				task := WireTask{Title: t.Title, Description: desc, EstimateMinutes: uint(t.EstimateMinutes)}
				if t.ScheduledDate != "" {
					value := t.ScheduledDate
					task.ScheduledDate = &value
				}
				stage.Tasks = append(stage.Tasks, task)
			}
			wire.Milestones = append(wire.Milestones, stage)
		}
		out.Plans = append(out.Plans, WireEntry{Role: p.Role, Plan: wire})
	}
	return out
}
