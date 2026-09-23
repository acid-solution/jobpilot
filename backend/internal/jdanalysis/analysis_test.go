package jdanalysis

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LeoninCS/jobpilot-next/backend/internal/modelconfig"
	"github.com/google/uuid"
)

type repositoryStub struct {
	job           Job
	claimErr      error
	completed     *Result
	failedCode    string
	failedRetry   bool
	heartbeatErr  error
	recoveryCalls atomic.Int32
}

func (r *repositoryStub) RecoverExpired(context.Context) (int64, error) {
	r.recoveryCalls.Add(1)
	return 0, nil
}
func (r *repositoryStub) Catalog(context.Context) (Catalog, error) { return Catalog{}, nil }
func (r *repositoryStub) Claim(context.Context, time.Duration) (Job, error) {
	return r.job, r.claimErr
}
func (r *repositoryStub) Heartbeat(context.Context, Job, time.Duration) error { return r.heartbeatErr }
func (r *repositoryStub) Complete(_ context.Context, _ Job, result Result) error {
	r.completed = &result
	return nil
}
func (r *repositoryStub) Fail(_ context.Context, _ Job, code string, retryable bool) error {
	r.failedCode, r.failedRetry = code, retryable
	return nil
}

type credentialStub struct {
	credentials modelconfig.Credentials
	err         error
}

func (s credentialStub) Credentials(context.Context, uuid.UUID) (modelconfig.Credentials, error) {
	return s.credentials, s.err
}

type analyzerStub struct {
	result Result
	err    error
}

type classificationReviewerStub struct {
	result ClassificationReviewResult
	err    error
	calls  int
}

func (s *classificationReviewerStub) ReviewJobClassifications(context.Context, string, string, string, ClassificationReviewInput) (ClassificationReviewResult, error) {
	s.calls++
	return s.result, s.err
}

func (s analyzerStub) AnalyzeJD(context.Context, string, string, string, Catalog) (Result, error) {
	return s.result, s.err
}

type blockingAnalyzerStub struct{}

func (blockingAnalyzerStub) AnalyzeJD(ctx context.Context, _, _, _ string, _ Catalog) (Result, error) {
	<-ctx.Done()
	return Result{}, ctx.Err()
}

func TestWorkerCompletesClaimedJob(t *testing.T) {
	repository := &repositoryStub{job: Job{ID: uuid.New(), UserID: uuid.New(), Attempts: 1, MaxAttempts: 3}}
	worker := NewWorker(repository, credentialStub{credentials: modelconfig.Credentials{
		Provider: "deepseek", Model: "deepseek-flash", APIKey: "secret",
	}}, analyzerStub{result: Result{Title: "Go 后端实习生"}}, time.Second)

	worked, err := worker.ProcessOnce(context.Background())
	if err != nil || !worked {
		t.Fatalf("ProcessOnce worked=%v err=%v", worked, err)
	}
	if repository.completed == nil || repository.completed.Provider != "deepseek" {
		t.Fatalf("job was not completed: %#v", repository.completed)
	}
}

func TestWorkerUsesIndependentClassificationReviewForValidJD(t *testing.T) {
	repository := &repositoryStub{job: Job{ID: uuid.New(), UserID: uuid.New(), Attempts: 1, MaxAttempts: 3}}
	reviewer := &classificationReviewerStub{result: ClassificationReviewResult{
		Decision: "correct", Reason: "审核后修正", Provider: "deepseek", Model: "deepseek-chat",
		Classifications: []JobClassification{{CategoryCode: "backend", SpecialtyCode: "backend-business", Relation: "primary", Evidence: "开发业务服务", Reason: "交付业务服务。"}},
	}}
	worker := NewWorker(repository, credentialStub{credentials: modelconfig.Credentials{
		Provider: "deepseek", Model: "deepseek-flash", APIKey: "user-secret",
	}}, analyzerStub{result: Result{ValidationStatus: ValidationValid, Title: "后端实习生", Responsibilities: []string{"开发业务服务"}}}, time.Second,
		ClassificationReviewConfig{Enabled: true, Reviewer: reviewer, APIKey: "platform-secret", Model: "deepseek-chat"})

	worked, err := worker.ProcessOnce(context.Background())
	if err != nil || !worked {
		t.Fatalf("ProcessOnce worked=%v err=%v", worked, err)
	}
	if reviewer.calls != 1 || repository.completed == nil || repository.completed.ClassificationReview == nil || len(repository.completed.Classifications) != 1 {
		t.Fatalf("classification review was not applied: calls=%d completed=%#v", reviewer.calls, repository.completed)
	}
}

func TestWorkerMarksMissingConfigurationAsTerminal(t *testing.T) {
	repository := &repositoryStub{job: Job{ID: uuid.New(), UserID: uuid.New(), Attempts: 1, MaxAttempts: 3}}
	worker := NewWorker(repository, credentialStub{err: modelconfig.ErrNotConfigured}, analyzerStub{}, time.Second)

	_, err := worker.ProcessOnce(context.Background())
	if err != nil {
		t.Fatalf("ProcessOnce: %v", err)
	}
	if repository.failedCode != "model_not_configured" || repository.failedRetry {
		t.Fatalf("unexpected failure: code=%s retryable=%v", repository.failedCode, repository.failedRetry)
	}
}

func TestWorkerPreservesRetryClassification(t *testing.T) {
	repository := &repositoryStub{job: Job{ID: uuid.New(), UserID: uuid.New(), Attempts: 1, MaxAttempts: 3}}
	worker := NewWorker(repository, credentialStub{credentials: modelconfig.Credentials{
		Provider: "deepseek", Model: "deepseek-flash", APIKey: "secret",
	}}, analyzerStub{err: NewError("model_rate_limited", true, errors.New("rate limited"))}, time.Second)

	_, err := worker.ProcessOnce(context.Background())
	if err != nil {
		t.Fatalf("ProcessOnce: %v", err)
	}
	if repository.failedCode != "model_rate_limited" || !repository.failedRetry {
		t.Fatalf("unexpected failure: code=%s retryable=%v", repository.failedCode, repository.failedRetry)
	}
}

func TestWorkerDiscardsResultWhenHeartbeatLosesLease(t *testing.T) {
	repository := &repositoryStub{
		job:          Job{ID: uuid.New(), UserID: uuid.New(), LeaseToken: uuid.New(), Attempts: 1, MaxAttempts: 3},
		heartbeatErr: ErrLeaseLost,
	}
	worker := NewWorker(repository, credentialStub{credentials: modelconfig.Credentials{
		Provider: "deepseek", Model: "deepseek-flash", APIKey: "secret",
	}}, blockingAnalyzerStub{}, time.Second)
	worker.heartbeatEvery = time.Millisecond

	worked, err := worker.ProcessOnce(context.Background())
	if err != nil || !worked {
		t.Fatalf("ProcessOnce worked=%v err=%v", worked, err)
	}
	if repository.completed != nil || repository.failedCode != "" {
		t.Fatalf("stale worker wrote a result: completed=%#v failed=%q", repository.completed, repository.failedCode)
	}
}

func TestWorkerScansForExpiredJobsAfterStartup(t *testing.T) {
	repository := &repositoryStub{claimErr: ErrNoJob}
	worker := NewWorker(repository, credentialStub{}, analyzerStub{}, time.Millisecond)
	worker.recoveryEvery = 2 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	worker.Run(ctx)

	if calls := repository.recoveryCalls.Load(); calls < 2 {
		t.Fatalf("expected startup and periodic recovery, got %d calls", calls)
	}
}
