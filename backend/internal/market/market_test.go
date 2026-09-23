package market

import (
	"context"
	"errors"
	"testing"

	"github.com/LeoninCS/jobpilot-next/backend/internal/target"
	"github.com/google/uuid"
)

type currentTargetStub struct {
	result target.Target
	err    error
}

func (s currentTargetStub) Current(context.Context, uuid.UUID) (target.Target, error) {
	return s.result, s.err
}

type marketRepositoryStub struct {
	createdTargetID uuid.UUID
	createdRawText  string
	result          JobDescription
	createCalls     int
	createErr       error
}

func (s *marketRepositoryStub) CreateWithAnalysisJob(
	_ context.Context,
	_ uuid.UUID,
	targetID uuid.UUID,
	rawText string,
	_ string,
) (JobDescription, error) {
	s.createdTargetID = targetID
	s.createdRawText = rawText
	s.createCalls++
	return s.result, s.createErr
}

func (s *marketRepositoryStub) ListByTarget(context.Context, uuid.UUID, uuid.UUID, ListFilter) ([]JobDescription, error) {
	return nil, nil
}

func (s *marketRepositoryStub) FindByID(context.Context, uuid.UUID, uuid.UUID) (JobDescription, error) {
	return JobDescription{}, ErrNotFound
}

func (s *marketRepositoryStub) UpdateRawText(context.Context, uuid.UUID, uuid.UUID, string, string) (JobDescription, error) {
	return JobDescription{}, nil
}

func (s *marketRepositoryStub) Delete(context.Context, uuid.UUID, uuid.UUID) error { return nil }

func (s *marketRepositoryStub) RetryAnalysis(context.Context, uuid.UUID, uuid.UUID) (JobDescription, error) {
	return JobDescription{}, nil
}
func (s *marketRepositoryStub) RetryAbilityReviews(context.Context, uuid.UUID, uuid.UUID) (JobDescription, error) {
	return JobDescription{}, nil
}

func (s *marketRepositoryStub) ProfileByTarget(context.Context, uuid.UUID, uuid.UUID) (Profile, error) {
	return Profile{}, nil
}

func TestSubmitUsesCurrentTargetAndPreservesRawJD(t *testing.T) {
	targetID := uuid.New()
	repository := &marketRepositoryStub{result: JobDescription{ID: uuid.New(), Status: StatusProcessing}}
	service := NewService(repository, currentTargetStub{result: target.Target{ID: targetID, CatalogStatus: target.CatalogStatusValid}})
	rawJD := "负责 Go 后端服务与 Agent 工具调用开发，要求熟悉 PostgreSQL 和 Redis。"

	result, err := service.Submit(context.Background(), uuid.New(), "  "+rawJD+"  ")
	if err != nil {
		t.Fatalf("Submit returned error: %v", err)
	}
	if result.Status != StatusProcessing {
		t.Fatalf("unexpected status: %s", result.Status)
	}
	if repository.createdTargetID != targetID {
		t.Fatalf("created under wrong target: %s", repository.createdTargetID)
	}
	if repository.createdRawText != rawJD {
		t.Fatalf("raw JD changed: %q", repository.createdRawText)
	}
}

func TestSubmitRequiresCurrentTarget(t *testing.T) {
	service := NewService(&marketRepositoryStub{}, currentTargetStub{err: target.ErrNotFound})

	_, err := service.Submit(context.Background(), uuid.New(), "负责 Go 后端服务开发，要求熟悉 PostgreSQL、Redis 和常见工程实践。")
	if !errors.Is(err, target.ErrNotFound) {
		t.Fatalf("expected target.ErrNotFound, got %v", err)
	}
}

func TestSubmitRejectsObviousGarbageBeforeReadingTarget(t *testing.T) {
	service := NewService(&marketRepositoryStub{}, currentTargetStub{err: errors.New("target should not be read")})

	for _, text := range []string{
		"https://example.com/jobs/1234567890",
		"##############################",
		"岗位要求岗位要求岗位要求岗位要求岗位要求岗位要求",
	} {
		_, err := service.Submit(context.Background(), uuid.New(), text)
		if !errors.Is(err, ErrInvalidJDText) {
			t.Fatalf("expected ErrInvalidJDText for %q, got %v", text, err)
		}
	}
}

func TestSubmitBatchKeepsValidItemsAndReportsDuplicateAndInvalidEntries(t *testing.T) {
	targetID := uuid.New()
	jdID := uuid.New()
	repository := &marketRepositoryStub{result: JobDescription{ID: jdID, Status: StatusProcessing}}
	service := NewService(repository, currentTargetStub{result: target.Target{ID: targetID, CatalogStatus: target.CatalogStatusValid}})
	valid := "负责 Go 后端服务开发，完成数据库建模、接口实现、自动化测试和线上问题排查。"

	result, err := service.SubmitBatch(context.Background(), uuid.New(), []string{valid, valid, "##############################"})
	if err != nil {
		t.Fatal(err)
	}
	if result.CreatedCount != 1 || result.DuplicateCount != 1 || result.InvalidCount != 1 {
		t.Fatalf("unexpected batch summary: %#v", result)
	}
	if repository.createCalls != 1 || len(result.Items) != 3 || result.Items[1].ExistingID == nil || *result.Items[1].ExistingID != jdID {
		t.Fatalf("unexpected batch items or repository calls: %#v calls=%d", result.Items, repository.createCalls)
	}
}

func TestHashRawTextNormalizesLineEndingsAndOuterWhitespace(t *testing.T) {
	left := hashRawText("  岗位职责\r\n负责 Go 后端开发。  ")
	right := hashRawText("岗位职责\n负责 Go 后端开发。")
	if left != right {
		t.Fatalf("equivalent JD text produced different hashes: %s != %s", left, right)
	}
}
