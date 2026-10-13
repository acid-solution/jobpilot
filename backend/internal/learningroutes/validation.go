package learningroutes

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

func Hash(value any) string {
	raw, _ := json.Marshal(value)
	var doc any
	_ = json.Unmarshal(raw, &doc)
	raw, _ = json.Marshal(doc)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func VersionHash(v Version) string {
	return Hash(struct {
		ID       string
		Revision int64
		Config   Config
		Plans    []Plan
	}{v.ID.String(), v.Revision, v.Config, v.Plans})
}
func validateConfig(c Config, hours, weeks int) error {
	if hours <= 0 || weeks <= 0 || weeks > 104 || c.Project.WeeklyMinutes <= 0 || c.Knowledge.WeeklyMinutes <= 0 || int64(c.Project.WeeklyMinutes)+int64(c.Knowledge.WeeklyMinutes) > int64(hours)*60 {
		return fmt.Errorf("%w: 两份计划的周预算必须为正，合计不能超过画像每周总投入", ErrInvalid)
	}
	for _, p := range []PlanConfig{c.Project, c.Knowledge} {
		if p.Mode == "sequence" {
			if p.StartDate != "" || len(p.LearningDays) > 0 || p.DailyMinutes != 0 {
				return fmt.Errorf("%w: 课时模式不设置日期或每日预算", ErrInvalid)
			}
			continue
		}
		if p.Mode != "calendar" {
			return ErrInvalid
		}
		if _, e := time.Parse("2006-01-02", p.StartDate); e != nil {
			return fmt.Errorf("%w: 请填写有效开始日期", ErrInvalid)
		}
		seen := map[int]bool{}
		for _, d := range p.LearningDays {
			if d < 0 || d > 6 || seen[d] {
				return ErrInvalid
			}
			seen[d] = true
		}
		if len(seen) == 0 || p.DailyMinutes <= 0 || p.DailyMinutes > 1440 || int64(p.WeeklyMinutes) > int64(p.DailyMinutes)*int64(len(seen)) {
			return fmt.Errorf("%w: 可学习日与每日预算不足以容纳周预算", ErrInvalid)
		}
	}
	return nil
}
func configFor(c Config, role string) PlanConfig {
	if role == "project" {
		return c.Project
	}
	return c.Knowledge
}
func validatePlans(plans []Plan, c Config, hours, weeks int) ([]string, error) {
	if err := validateConfig(c, hours, weeks); err != nil {
		return nil, err
	}
	if len(plans) != 2 {
		return nil, ErrInvalid
	}
	roles, ids := map[string]bool{}, map[string]int{}
	tasks := []Task{}
	warnings := []string{}
	for _, p := range plans {
		if (p.Role != "project" && p.Role != "knowledge") || roles[p.Role] || strings.TrimSpace(p.Title) == "" || len(p.Title) > 160 || strings.TrimSpace(p.Objective) == "" || len(p.Objective) > 2000 || len(p.Description) > 2000 || len(p.Stages) == 0 || len(p.Stages) > 50 {
			return nil, ErrInvalid
		}
		roles[p.Role] = true
		budget := configFor(c, p.Role)
		total, count := int64(0), 0
		daily, weekly := map[string]int{}, map[int]int{}
		start, _ := time.Parse("2006-01-02", budget.StartDate)
		for _, stage := range p.Stages {
			if strings.TrimSpace(stage.Title) == "" || len(stage.Title) > 160 || strings.TrimSpace(stage.Outcome) == "" || len(stage.Outcome) > 2000 || len(stage.Tasks) == 0 {
				return nil, ErrInvalid
			}
			for _, task := range stage.Tasks {
				if strings.TrimSpace(task.ID) == "" || len(task.ID) > 100 || strings.TrimSpace(task.Title) == "" || len(task.Title) > 255 || strings.TrimSpace(task.Description) == "" || strings.TrimSpace(task.CompletionCriteria) == "" || len(task.Description)+len(task.CompletionCriteria) > 1800 || task.EstimateMinutes <= 0 || task.EstimateMinutes > 100000 || len(task.DependsOn) > 20 {
					return nil, fmt.Errorf("%w: 任务必须包含行动说明、预计分钟和完成标准", ErrInvalid)
				}
				if _, ok := ids[task.ID]; ok {
					return nil, ErrInvalid
				}
				ids[task.ID] = len(tasks)
				tasks = append(tasks, task)
				total += int64(task.EstimateMinutes)
				count++
				if budget.Mode == "sequence" {
					if task.ScheduledDate != "" {
						return nil, fmt.Errorf("%w: 课时任务不能设置日期", ErrInvalid)
					}
				} else {
					d, e := time.Parse("2006-01-02", task.ScheduledDate)
					days := int(d.Sub(start).Hours() / 24)
					if e != nil || days < 0 || days >= weeks*7 || !containsDay(budget.LearningDays, int(d.Weekday())) {
						return nil, fmt.Errorf("%w: 任务日期不在预期周期或可学习日内", ErrInvalid)
					}
					daily[task.ScheduledDate] += task.EstimateMinutes
					weekly[days/7] += task.EstimateMinutes
					if daily[task.ScheduledDate] > budget.DailyMinutes || weekly[days/7] > budget.WeeklyMinutes {
						return nil, fmt.Errorf("%w: 任务日期超出每日或每周预算，请调整安排", ErrInvalid)
					}
				}
			}
		}
		if count > 200 || total > int64(budget.WeeklyMinutes)*int64(weeks) {
			return nil, fmt.Errorf("%w: 任务投入超出预期周期预算，请缩小范围或调整时间配置", ErrInvalid)
		}
	}
	state := map[string]int{}
	var visit func(string) error
	visit = func(id string) error {
		if state[id] == 1 {
			return fmt.Errorf("%w: 前置关系形成循环", ErrInvalid)
		}
		if state[id] == 2 {
			return nil
		}
		state[id] = 1
		i, ok := ids[id]
		if !ok {
			return ErrInvalid
		}
		for _, dep := range tasks[i].DependsOn {
			j, ok := ids[dep]
			if !ok {
				return fmt.Errorf("%w: 前置任务不存在", ErrInvalid)
			}
			if err := visit(dep); err != nil {
				return err
			}
			if j >= i || (tasks[i].ScheduledDate != "" && tasks[j].ScheduledDate != "" && tasks[i].ScheduledDate < tasks[j].ScheduledDate) {
				warnings = append(warnings, "任务「"+tasks[i].Title+"」安排在前置任务「"+tasks[j].Title+"」之前；可继续，但建议检查顺序")
			}
		}
		state[id] = 2
		return nil
	}
	for id := range ids {
		if err := visit(id); err != nil {
			return nil, err
		}
	}
	sort.Strings(warnings)
	// The actual wire description also includes acceptance criteria and dependency
	// titles; check it before saving, rather than discover this only on import.
	for _, entry := range BuildPayload(Version{Plans: plans, Config: c}, "", hours).Plans {
		for _, stage := range entry.Plan.Milestones {
			for _, task := range stage.Tasks {
				if len(task.Description) > 2000 {
					return nil, fmt.Errorf("%w: 任务说明与前置提示合计过长，请精简后再保存", ErrInvalid)
				}
			}
		}
	}
	return warnings, nil
}
func containsDay(days []int, day int) bool {
	for _, d := range days {
		if d == day {
			return true
		}
	}
	return false
}

// Scheduling is deterministic and bounded by the requested period. It never
// extends a deadline to conceal insufficient capacity.
func schedule(plans []Plan, c Config, weeks int, preserved ...Plan) error {
	type entry struct {
		task *Task
		role string
	}
	entries, ordered := map[string]entry{}, []string{}
	dates := map[string]string{}
	for _, p := range preserved {
		for _, s := range p.Stages {
			for _, t := range s.Tasks {
				dates[t.ID] = t.ScheduledDate
			}
		}
	}
	for pi := range plans {
		if plans[pi].Role != "project" && plans[pi].Role != "knowledge" {
			return ErrInvalid
		}
		for si := range plans[pi].Stages {
			for ti := range plans[pi].Stages[si].Tasks {
				t := &plans[pi].Stages[si].Tasks[ti]
				if _, exists := entries[t.ID]; exists {
					return ErrInvalid
				}
				if _, exists := dates[t.ID]; exists {
					return ErrInvalid
				}
				entries[t.ID] = entry{t, plans[pi].Role}
				ordered = append(ordered, t.ID)
			}
		}
	}
	states := map[string]int{}
	daily, weekly, cursor := map[string]map[int]int{}, map[string]map[int]int{}, map[string]int{}
	for _, role := range []string{"project", "knowledge"} {
		daily[role] = map[int]int{}
		weekly[role] = map[int]int{}
	}
	var place func(string) error
	place = func(id string) error {
		if states[id] == 2 {
			return nil
		}
		if states[id] == 1 {
			return fmt.Errorf("%w: 前置关系形成循环", ErrInvalid)
		}
		e, exists := entries[id]
		if !exists {
			if _, known := dates[id]; known {
				return nil
			}
			return fmt.Errorf("%w: 前置任务不存在", ErrInvalid)
		}
		states[id] = 1
		for _, dep := range e.task.DependsOn {
			if err := place(dep); err != nil {
				return err
			}
		}
		conf := configFor(c, e.role)
		e.task.ScheduledDate = ""
		if conf.Mode == "calendar" {
			start, err := time.Parse("2006-01-02", conf.StartDate)
			if err != nil {
				return ErrInvalid
			}
			found := false
			for day := cursor[e.role]; day < weeks*7; day++ {
				date := start.AddDate(0, 0, day)
				value := date.Format("2006-01-02")
				if !containsDay(conf.LearningDays, int(date.Weekday())) || daily[e.role][day]+e.task.EstimateMinutes > conf.DailyMinutes || weekly[e.role][day/7]+e.task.EstimateMinutes > conf.WeeklyMinutes {
					continue
				}
				ready := true
				for _, dep := range e.task.DependsOn {
					if dates[dep] > value {
						ready = false
					}
				}
				if !ready {
					continue
				}
				e.task.ScheduledDate = value
				daily[e.role][day] += e.task.EstimateMinutes
				weekly[e.role][day/7] += e.task.EstimateMinutes
				cursor[e.role] = day
				found = true
				break
			}
			if !found {
				return fmt.Errorf("%w: 预期周期内无法排下任务，请调整预算或项目范围", ErrInvalid)
			}
		}
		dates[id] = e.task.ScheduledDate
		states[id] = 2
		return nil
	}
	for _, id := range ordered {
		if err := place(id); err != nil {
			return err
		}
	}
	return nil
}
