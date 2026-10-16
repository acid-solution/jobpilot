package learningroutes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/LeoninCS/jobpilot-next/backend/internal/assistantmemory"
	"sort"
	"strings"

	"github.com/LeoninCS/jobpilot-next/backend/internal/knowledgegaps"
	"github.com/LeoninCS/jobpilot-next/backend/internal/modelconfig"
	"github.com/LeoninCS/jobpilot-next/backend/internal/profile"
	"github.com/LeoninCS/jobpilot-next/backend/internal/projectrecs"
	"github.com/google/uuid"
)

type Projects interface {
	Snapshot(context.Context, uuid.UUID) (projectrecs.Snapshot, error)
	Get(context.Context, uuid.UUID) (projectrecs.View, error)
}
type Gaps interface {
	CurrentComparison(context.Context, uuid.UUID) (knowledgegaps.View, error)
}
type Profiles interface {
	GetOverview(context.Context, uuid.UUID) (profile.Overview, error)
}
type Credentials interface {
	Credentials(context.Context, uuid.UUID) (modelconfig.Credentials, error)
}
type Service struct {
	preferences assistantmemory.Provider
	repo        Repository
	projects    Projects
	gaps        Gaps
	profiles    Profiles
	credentials Credentials
	remote      Remote
	webURL      string
}

func (s *Service) SetPreferences(p assistantmemory.Provider) { s.preferences = p }
func (s *Service) GetImport(ctx context.Context, user, id uuid.UUID) (ImportRequest, error) {
	return s.repo.FindImport(ctx, user, id)
}

func NewService(repo Repository, projects Projects, gaps Gaps, profiles Profiles, creds Credentials, remote Remote, web string) *Service {
	return &Service{repo: repo, projects: projects, gaps: gaps, profiles: profiles, credentials: creds, remote: remote, webURL: strings.TrimRight(web, "/")}
}

func (s *Service) snapshot(ctx context.Context, user uuid.UUID) (Snapshot, error) {
	source, err := s.projects.Snapshot(ctx, user)
	if err != nil {
		return Snapshot{}, err
	}
	source.Input.Preferences = nil
	result := Snapshot{TargetID: source.TargetID, GoalSignature: source.GoalSignature, Readiness: source.Readiness, Input: Input{Profile: source.Input}, Preparation: Preparation{RequiredJDCount: 10, IncludedJDCount: source.Input.IncludedJDCount}}
	if source.GoalSignature == "" {
		return result, nil
	}
	overview, err := s.profiles.GetOverview(ctx, user)
	if err != nil {
		return Snapshot{}, err
	}
	result.Preparation.MarketComplete = overview.MarketProfileReady && source.Input.IncludedJDCount >= result.Preparation.RequiredJDCount
	result.Preparation.ProfileComplete = overview.Complete && source.Input.WeeklyHours > 0 && source.Input.ExpectedWeeks > 0 && source.Input.ExistingExperience != ""
	result.Preparation.AnalysisReady = result.Preparation.MarketComplete && analysisReady(source.Readiness)
	if result.Readiness.Code == "ready" && !result.Preparation.MarketComplete {
		result.Readiness.Code, result.Readiness.Message = "market_incomplete", "请先完成市场画像采集"
	}
	if result.Readiness.Code == "ready" && !result.Preparation.ProfileComplete {
		result.Readiness.Code, result.Readiness.Message = "profile_incomplete", "请先完成用户画像与必要信息"
	}
	if result.Readiness.Code == "ready" && !result.Preparation.AnalysisReady {
		result.Readiness.Code, result.Readiness.Message = "grading_pending", "请先等待相关 JD 判级完成"
		if source.Readiness.PendingReviewCount > 0 || source.Readiness.FailedReviewCount > 0 {
			result.Readiness.Code, result.Readiness.Message = "ability_review_pending", "请先完成相关能力目录审核"
		}
	}
	projects, err := s.projects.Get(ctx, user)
	if err != nil {
		return Snapshot{}, err
	}
	if projects.Report == nil || projects.SelectedProjectID == "" {
		if result.Readiness.Code == "ready" {
			result.Readiness.Code, result.Readiness.Message = "project_missing", "请先生成项目推荐并选择一个项目"
		}
		return result, nil
	}
	result.ReportID = projects.Report.ID
	result.ProjectID = projects.SelectedProjectID
	for _, p := range projects.Report.Projects {
		if p.ID == projects.SelectedProjectID {
			result.Input.Project = p
		}
	}
	if result.Input.Project.ID == "" {
		return Snapshot{}, ErrNotFound
	}
	result.Preparation.ProjectSelected = true
	result.Preparation.ProjectNeedsReview = projects.SelectionNeedsReview
	gaps, err := s.gaps.CurrentComparison(ctx, user)
	if err != nil {
		return Snapshot{}, err
	}
	result.Preparation.AnalysisReady = result.Preparation.AnalysisReady && analysisReady(gaps.Readiness)
	if result.Readiness.Code == "ready" && gaps.Readiness.Code != "ready" {
		result.Readiness = gaps.Readiness
	}
	if result.Readiness.Code == "ready" && !result.Preparation.AnalysisReady {
		result.Readiness = gaps.Readiness
		result.Readiness.Code, result.Readiness.Message = "grading_pending", "请先等待相关 JD 判级完成"
		if gaps.Readiness.PendingReviewCount > 0 || gaps.Readiness.FailedReviewCount > 0 {
			result.Readiness.Code, result.Readiness.Message = "ability_review_pending", "请先完成相关能力目录审核"
		}
	}
	if result.Readiness.Code == "ready" && projects.SelectionNeedsReview {
		result.Readiness.Code, result.Readiness.Message = "project_stale", "项目依据已变化，请先重新核对项目推荐"
	}
	result.Input.KnowledgeHash = gaps.SourceFingerprint
	if gaps.Report != nil {
		result.Input.Knowledge = *gaps.Report
	}
	for _, a := range overview.Capabilities {
		result.Input.Standards = append(result.Input.Standards, AbilityStandard{Name: a.Name, Levels: a.Levels})
	}
	result.Input.ProjectHash = projectrecs.ProjectContextHash(source.Input)
	sort.Slice(result.Input.Standards, func(i, j int) bool { return result.Input.Standards[i].Name < result.Input.Standards[j].Name })
	result.SourceHash = Hash(struct {
		Source    string
		Report    uuid.UUID
		Project   string
		Knowledge string
		Standards string
	}{businessSource(source), result.ReportID, result.ProjectID, gaps.SourceFingerprint, Hash(result.Input.Standards)})
	return result, nil
}

func analysisReady(r knowledgegaps.Readiness) bool {
	return r.Code != "ability_review_pending" && r.Code != "grading_pending" &&
		r.PendingReviewCount == 0 && r.FailedReviewCount == 0 && r.PendingGradingCount == 0 && r.FailedGradingCount == 0
}
func (s *Service) Get(ctx context.Context, user uuid.UUID) (View, error) {
	var result View
	err := s.repo.WithinUserTransaction(ctx, user, func(txCtx context.Context) error {
		ss, e := s.snapshot(txCtx, user)
		if e != nil {
			return e
		}
		result = View{Readiness: ss.Readiness, Preparation: ss.Preparation, WeeklyHours: ss.Input.Profile.WeeklyHours, ExpectedWeeks: ss.Input.Profile.ExpectedWeeks, Knowledge: &ss.Input.Knowledge, Warnings: []string{}, ImportEnabled: s.remote != nil && s.remote.Enabled(), StudyFlowURL: s.webURL}
		result.GoalTitle = ss.Input.Profile.Goal
		result.ProjectTitle = ss.Input.Project.Title
		if ss.Input.Knowledge.TargetID == uuid.Nil {
			result.Knowledge = nil
		}
		if ss.ReportID == uuid.Nil {
			return nil
		}
		result.Version, result.Job, result.Import, e = s.repo.Get(txCtx, user, ss.GoalSignature, ss.ReportID, ss.ProjectID)
		if e != nil {
			return e
		}
		if result.Version != nil {
			result.Version.ContentHash = VersionHash(*result.Version)
			result.KnowledgeNeedsUpdate = result.Version.KnowledgeHash != ss.Input.KnowledgeHash
			result.ProjectNeedsReview = result.Version.SourceHash != ss.Input.ProjectHash
			result.Warnings, _ = validatePlans(result.Version.Plans, result.Version.Config, ss.Input.Profile.WeeklyHours, ss.Input.Profile.ExpectedWeeks)
		}
		return nil
	})
	return result, err
}
func (s *Service) Generate(ctx context.Context, user uuid.UUID, request GenerateInput) (View, error) {
	err := s.repo.WithinUserTransaction(ctx, user, func(txCtx context.Context) error {
		ss, err := s.snapshot(txCtx, user)
		if err != nil {
			return err
		}
		if ss.Readiness.Code != "ready" {
			return ErrNotReady
		}
		if err = validateConfig(request.Config, ss.Input.Profile.WeeklyHours, ss.Input.Profile.ExpectedWeeks); err != nil {
			return err
		}
		if len([]rune(request.Adjustment)) > 1000 {
			return ErrInvalid
		}
		if _, err = s.credentials.Credentials(txCtx, user); err != nil {
			return err
		}
		version, _, _, err := s.repo.Get(txCtx, user, ss.GoalSignature, ss.ReportID, ss.ProjectID)
		if err != nil {
			return err
		}
		if s.preferences != nil {
			prefs, e := s.preferences.Snapshot(txCtx, user, ss.GoalSignature)
			if e != nil {
				return e
			}
			purpose := "route"
			if request.KnowledgeOnly {
				purpose = "knowledge"
			}
			relevant := assistantmemory.Relevant(prefs, purpose)
			ss.Input.Preferences = &relevant
		}
		ss.Input.Config = request.Config
		ss.Input.KnowledgeOnly = request.KnowledgeOnly
		ss.Input.Adjustment = request.Adjustment
		if request.KnowledgeOnly {
			if version == nil {
				return ErrNotFound
			}
			if Hash(version.Config.Project) != Hash(request.Config.Project) {
				return fmt.Errorf("%w: 更新知识计划不能修改项目时间配置", ErrInvalid)
			}
			ss.Input.Previous = version.Plans
		}
		if err = validatePaths(ss.Input); err != nil {
			return err
		}
		_, err = s.repo.Enqueue(txCtx, user, ss)
		return err
	})
	if err != nil {
		return View{}, err
	}
	return s.Get(ctx, user)
}
func validatePaths(input Input) error {
	seen := map[string]bool{}
	for _, choice := range input.Config.Paths {
		if seen[choice.GapID] {
			return ErrInvalid
		}
		seen[choice.GapID] = true
		var gap *knowledgegaps.Item
		for i := range input.Knowledge.Gaps {
			if input.Knowledge.Gaps[i].ID == choice.GapID {
				gap = &input.Knowledge.Gaps[i]
			}
		}
		if gap == nil || len(gap.Options) == 0 || len(choice.AbilityIDs) != gap.RequiredCount-gap.SatisfiedCount {
			return fmt.Errorf("%w: 请选择足够且不重复的补强路径", ErrInvalid)
		}
		ids := map[uuid.UUID]bool{}
		for _, id := range choice.AbilityIDs {
			found := false
			for _, option := range gap.Options {
				if option.AbilityID == id && !option.Satisfied {
					found = true
				}
			}
			if !found || ids[id] {
				return ErrInvalid
			}
			ids[id] = true
		}
	}
	return nil
}

func samePath(a, b PathChoice) bool {
	if len(a.AbilityIDs) != len(b.AbilityIDs) {
		return false
	}
	ids := map[uuid.UUID]bool{}
	for _, id := range a.AbilityIDs {
		ids[id] = true
	}
	for _, id := range b.AbilityIDs {
		if !ids[id] {
			return false
		}
	}
	return true
}
func (s *Service) Edit(ctx context.Context, user uuid.UUID, input EditInput) (View, error) {
	err := s.repo.WithinUserTransaction(ctx, user, func(txCtx context.Context) error {
		ss, e := s.snapshot(txCtx, user)
		if e != nil {
			return e
		}
		version, job, _, e := s.repo.Get(txCtx, user, ss.GoalSignature, ss.ReportID, ss.ProjectID)
		if e != nil {
			return e
		}
		if version == nil || version.ID != input.VersionID || version.Revision != input.Revision || (job != nil && (job.Status == "queued" || job.Status == "running")) {
			return ErrConflict
		}
		_, e = validatePlans(input.Plans, input.Config, ss.Input.Profile.WeeklyHours, ss.Input.Profile.ExpectedWeeks)
		if e != nil {
			return e
		}
		return s.repo.Edit(txCtx, user, input)
	})
	if err != nil {
		return View{}, err
	}
	return s.Get(ctx, user)
}

// AdmitImport is called inside the same user transaction as page/Agent
// confirmation. It only freezes a payload; network I/O belongs to Dispatch.
func (s *Service) AdmitImport(ctx context.Context, user uuid.UUID, input ConfirmInput) (ImportRequest, error) {
	var frozen ImportRequest
	err := s.repo.WithinUserTransaction(ctx, user, func(txCtx context.Context) error {
		ss, e := s.snapshot(txCtx, user)
		if e != nil {
			return e
		}
		version, job, old, e := s.repo.Get(txCtx, user, ss.GoalSignature, ss.ReportID, ss.ProjectID)
		if e != nil {
			return e
		}
		if version == nil || version.ID != input.VersionID || version.Revision != input.Revision || VersionHash(*version) != input.ContentHash {
			return ErrConflict
		}
		if old != nil && old.VersionID == version.ID && old.Revision == version.Revision {
			frozen = *old
			return nil
		}
		if ss.Readiness.Code != "ready" || version.SourceHash != ss.Input.ProjectHash || version.KnowledgeHash != ss.Input.KnowledgeHash {
			return ErrInputsChanged
		}
		if job != nil && (job.Status == "queued" || job.Status == "running") {
			return ErrConflict
		}
		if _, e = validatePlans(version.Plans, version.Config, ss.Input.Profile.WeeklyHours, ss.Input.Profile.ExpectedWeeks); e != nil {
			return e
		}
		payload := BuildPayload(*version, ss.Input.Profile.Goal, ss.Input.Profile.WeeklyHours)
		frozen, e = s.repo.Freeze(txCtx, user, *version, payload, Hash(payload))
		return e
	})
	return frozen, err
}
func (s *Service) Import(ctx context.Context, user uuid.UUID, input ConfirmInput, access string) (View, error) {
	request, err := s.AdmitImport(ctx, user, input)
	if err != nil {
		return View{}, err
	}
	if err = s.Dispatch(ctx, user, request.ID, access); err != nil {
		return View{}, err
	}
	return s.Get(ctx, user)
}
func (s *Service) RetryImport(ctx context.Context, user, id uuid.UUID, access string) (View, error) {
	if err := s.Dispatch(ctx, user, id, access); err != nil {
		return View{}, err
	}
	return s.Get(ctx, user)
}
func (s *Service) Dispatch(ctx context.Context, user, id uuid.UUID, access string) error {
	if s.remote == nil || !s.remote.Enabled() {
		return fmt.Errorf("%w: StudyFlow 导入尚未配置", ErrNotReady)
	}
	request, err := s.repo.ClaimImport(ctx, user, id)
	if err != nil {
		return err
	}
	if request.Status == "succeeded" {
		return nil
	}
	receipt, err := s.remote.Send(ctx, access, request)
	if err != nil {
		if errors.Is(err, ErrRemoteRejected) {
			return s.repo.FinishImport(context.WithoutCancel(ctx), request, nil, "failed", "studyflow_import_conflict_or_invalid")
		}
		// Transport errors are ambiguous: never report a rolled-back or successful remote write without evidence.
		if e := s.repo.FinishImport(context.WithoutCancel(ctx), request, nil, "unknown", "import_result_unknown"); e != nil {
			return errors.Join(err, e)
		}
		return nil
	}
	if !validReceipt(request, receipt) {
		return s.repo.FinishImport(context.WithoutCancel(ctx), request, nil, "unknown", "invalid_import_receipt")
	}
	return s.repo.FinishImport(context.WithoutCancel(ctx), request, receipt, "succeeded", "")
}

func validReceipt(request ImportRequest, receipt *Receipt) bool {
	if receipt == nil || receipt.SourceRouteID != request.RouteID.String() || receipt.SourceVersion != request.Payload.SourceVersion || receipt.ContentDigest != request.Digest || len(receipt.Plans) != 2 {
		return false
	}
	for _, id := range []string{receipt.ImportID, receipt.GoalID} {
		if value, err := uuid.Parse(id); err != nil || value == uuid.Nil {
			return false
		}
	}
	roles, ids := map[string]bool{}, map[string]bool{}
	for _, p := range receipt.Plans {
		if (p.Role != "project" && p.Role != "knowledge") || roles[p.Role] || p.VersionNo == 0 {
			return false
		}
		roles[p.Role] = true
		for _, id := range []string{p.PlanID, p.PlanVersionID} {
			if value, err := uuid.Parse(id); err != nil || value == uuid.Nil || ids[id] {
				return false
			}
			ids[id] = true
		}
	}
	return true
}
func copyPlans(plans []Plan) []Plan {
	raw, _ := json.Marshal(plans)
	var out []Plan
	_ = json.Unmarshal(raw, &out)
	return out
}

func businessSource(ss projectrecs.Snapshot) string {
	if ss.BusinessSourceHash != "" {
		return ss.BusinessSourceHash
	}
	return ss.SourceHash
}
func (s *Service) validatePreferences(ctx context.Context, job Job) error {
	if s.preferences == nil || job.Input.Preferences == nil {
		return nil
	}
	prefs, e := s.preferences.Snapshot(ctx, job.UserID, job.GoalSignature)
	if e != nil {
		return e
	}
	purpose := "route"
	if job.Input.KnowledgeOnly {
		purpose = "knowledge"
	}
	current := assistantmemory.Relevant(prefs, purpose)
	if assistantmemory.Fingerprint(current) != assistantmemory.Fingerprint(*job.Input.Preferences) {
		return ErrInputsChanged
	}
	return nil
}
