package projectrecs

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/LeoninCS/jobpilot-next/backend/internal/jdanalysis"
	"github.com/LeoninCS/jobpilot-next/backend/internal/modelconfig"
	"github.com/google/uuid"
)

var ErrInputsChanged = errors.New("recommendation inputs changed")

type CredentialReader interface {
	Credentials(context.Context, uuid.UUID) (modelconfig.Credentials, error)
}
type Worker struct {
	repo        Repository
	service     *Service
	credentials CredentialReader
	model       Generator
	search      Searcher
	poll        time.Duration
	lease       time.Duration
	heartbeat   time.Duration
}

func NewWorker(repo Repository, service *Service, credentials CredentialReader, model Generator, search Searcher, poll time.Duration) *Worker {
	return &Worker{repo, service, credentials, model, search, poll, 5 * time.Minute, 30 * time.Second}
}
func (w *Worker) Run(ctx context.Context) {
	if err := w.repo.RecoverExpired(ctx); err != nil {
		slog.Error("recover recommendation jobs", "error", err)
	}
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := w.repo.RecoverExpired(ctx); err != nil {
					slog.Error("recover recommendation jobs", "error", err)
				}
			}
		}
	}()
	ticker := time.NewTicker(w.poll)
	defer ticker.Stop()
	for {
		worked, err := w.ProcessOnce(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			slog.Error("process recommendation job", "error", err)
		}
		if worked {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (w *Worker) ProcessOnce(ctx context.Context) (bool, error) {
	job, err := w.repo.Claim(ctx, w.lease)
	if errors.Is(err, ErrNoJob) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	taskCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	heartbeatCtx, stopHeartbeat := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(w.heartbeat)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				done <- nil
				return
			case <-ticker.C:
				if err := w.repo.Heartbeat(heartbeatCtx, job, w.lease); err != nil {
					cancel()
					done <- err
					return
				}
			}
		}
	}()
	creds, err := w.credentials.Credentials(taskCtx, job.UserID)
	var report Report
	if err == nil {
		report, err = w.pipeline(taskCtx, job, creds)
	}
	stopHeartbeat()
	leaseErr := <-done
	if errors.Is(leaseErr, ErrLeaseLost) {
		slog.Warn("discard recommendation after lease loss", "job_id", job.ID)
		return true, nil
	}
	if leaseErr != nil {
		err = leaseErr
	}
	if err != nil {
		if errors.Is(err, context.Canceled) && ctx.Err() != nil {
			return true, err
		}
		code, retry := failure(err)
		slog.Warn("recommendation generation failed", "job_id", job.ID, "code", code, "error", err)
		if failErr := w.repo.Fail(ctx, job, code, retry); failErr != nil && !errors.Is(failErr, ErrLeaseLost) {
			return true, errors.Join(err, failErr)
		}
		return true, nil
	}
	if err = w.repo.Complete(ctx, job, report); errors.Is(err, ErrLeaseLost) {
		slog.Warn("discard recommendation after lease loss", "job_id", job.ID)
		return true, nil
	}
	return true, err
}
func (w *Worker) pipeline(ctx context.Context, job Job, creds modelconfig.Credentials) (Report, error) {
	report := Report{ID: uuid.New(), TargetTitle: job.Input.Goal, Projects: []Project{}, GeneratedAt: time.Now().UTC(), PromptVersion: PromptVersion}
	drafts := job.Drafts
	if job.Phase == "draft" {
		var empty string
		var err error
		drafts, empty, err = GenerateDrafts(ctx, w.model, creds.APIKey, creds.Model, job.Input, job.Adjustment)
		if err != nil {
			return report, err
		}
		if len(drafts) == 0 {
			report.EmptyReason = empty
			return w.ensureCurrent(ctx, job, report)
		}
		if err = w.repo.SaveDrafts(ctx, job, drafts); err != nil {
			return report, err
		}
	}
	research := job.Research
	if job.Phase == "draft" || job.Phase == "research" {
		completed := make(map[string]Research, len(research))
		for _, item := range research {
			completed[item.DraftID] = item
		}
		research = make([]Research, 0, len(drafts))
		for _, d := range drafts {
			if item, ok := completed[d.ID]; ok {
				research = append(research, item)
				continue
			}
			item, err := w.search.Research(ctx, d)
			if err != nil {
				return report, err
			}
			research = append(research, item)
			if err := w.repo.SaveResearchProgress(ctx, job, research); err != nil {
				return report, err
			}
		}
		if err := w.repo.SaveResearch(ctx, job, research); err != nil {
			return report, err
		}
	}
	assessments, err := Evaluate(ctx, w.model, creds.APIKey, creds.Model, drafts, research, false)
	if err != nil {
		return report, err
	}
	byAssessment := map[string]assessment{}
	byResearch := map[string]Research{}
	byDraft := map[string]Draft{}
	for i, a := range assessments {
		byAssessment[a.DraftID] = assessments[i]
	}
	for _, r := range research {
		byResearch[r.DraftID] = r
	}
	for _, d := range drafts {
		byDraft[d.ID] = d
	}
	revised := []Draft{}
	revisedResearch := []Research{}
	for _, d := range drafts {
		if a := byAssessment[d.ID]; a.TooSimilar && a.Revised != nil {
			candidate := *a.Revised
			candidate.ID = d.ID
			candidate.KnownFacts = profileFacts(job.Input, candidate)
			if candidate.Title == d.Title && candidate.Problem == d.Problem && candidate.Shape == d.Shape {
				continue
			}
			revised = append(revised, candidate)
		}
	}
	for _, d := range revised {
		item, err := w.search.Research(ctx, d)
		if err != nil {
			return report, err
		}
		revisedResearch = append(revisedResearch, item)
	}
	if len(revised) > 0 {
		final, err := Evaluate(ctx, w.model, creds.APIKey, creds.Model, revised, revisedResearch, true)
		if err != nil {
			return report, err
		}
		for _, a := range final {
			byAssessment[a.DraftID] = a
		}
		for _, r := range revisedResearch {
			byResearch[r.DraftID] = r
		}
		for _, d := range revised {
			byDraft[d.ID] = d
		}
	}
	finalDrafts := []Draft{}
	finalResearch := []Research{}
	finalAssessments := []assessment{}
	for _, original := range drafts {
		d := byDraft[original.ID]
		a := byAssessment[original.ID]
		if a.TooSimilar || a.Unsuitable {
			continue
		}
		finalDrafts = append(finalDrafts, d)
		finalResearch = append(finalResearch, byResearch[d.ID])
		finalAssessments = append(finalAssessments, a)
	}
	report.Projects = BuildProjects(finalDrafts, finalResearch, finalAssessments)
	if len(report.Projects) == 0 {
		report.EmptyReason = "本轮候选缺少足够具体的使用场景，或与已有方案过于接近，暂未找到适合写进简历的选题。可以调整条件后重试。"
	}
	return w.ensureCurrent(ctx, job, report)
}
func (w *Worker) ensureCurrent(ctx context.Context, job Job, report Report) (Report, error) {
	ss, err := w.service.Snapshot(ctx, job.UserID)
	if err != nil {
		return report, err
	}
	if ss.GoalSignature != job.GoalSignature || ss.SourceHash != job.SourceHash || ss.Readiness.Code != "ready" {
		return report, ErrInputsChanged
	}
	return report, nil
}
func failure(err error) (string, bool) {
	if errors.Is(err, ErrInputsChanged) {
		return "inputs_changed", false
	}
	if errors.Is(err, modelconfig.ErrNotConfigured) {
		return "model_not_configured", false
	}
	if errors.Is(err, ErrGitHubUnavailable) {
		return "github_unavailable", true
	}
	if errors.Is(err, ErrModelFormat) {
		return "model_invalid_response", true
	}
	if errors.Is(err, ErrLeaseLost) {
		return "lease_lost", false
	}
	var classified *jdanalysis.ClassifiedError
	if errors.As(err, &classified) {
		return classified.FailureCode, classified.CanRetry
	}
	return "recommendation_failed", true
}
