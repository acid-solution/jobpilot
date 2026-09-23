package target

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

type targetRepositoryStub struct {
	upsertInput UpsertInput
	result      Target
}

func (s *targetRepositoryStub) FindCurrent(context.Context, uuid.UUID) (Target, error) {
	return Target{}, ErrNotFound
}

func (s *targetRepositoryStub) UpsertCurrent(_ context.Context, _ uuid.UUID, input UpsertInput) (Target, error) {
	s.upsertInput = input
	return s.result, nil
}

func (s *targetRepositoryStub) ListCatalog(context.Context) ([]Category, error) { return nil, nil }

func TestUpsertCurrentNormalizesAndPersistsTarget(t *testing.T) {
	repository := &targetRepositoryStub{result: Target{ID: uuid.New()}}
	service := NewService(repository)
	year := 2027
	backendID, backendSpecialtyID := uuid.New(), uuid.New()
	aiID, agentSpecialtyID := uuid.New(), uuid.New()

	_, err := service.UpsertCurrent(context.Background(), uuid.New(), UpsertInput{
		EmploymentType: " internship ",
		GraduationYear: &year,
		Directions: []DirectionInput{
			{CategoryID: backendID, SpecialtyID: &backendSpecialtyID},
			{CategoryID: aiID, SpecialtyID: &agentSpecialtyID},
		},
	})
	if err != nil {
		t.Fatalf("UpsertCurrent returned error: %v", err)
	}
	if repository.upsertInput.EmploymentType != "internship" {
		t.Fatalf("employment type was not normalized: %q", repository.upsertInput.EmploymentType)
	}
	if *repository.upsertInput.Directions[1].SpecialtyID != agentSpecialtyID {
		t.Fatalf("direction was not normalized: %#v", repository.upsertInput.Directions[1])
	}
}

func TestUpsertCurrentRejectsMissingDirection(t *testing.T) {
	service := NewService(&targetRepositoryStub{})

	_, err := service.UpsertCurrent(context.Background(), uuid.New(), UpsertInput{
		EmploymentType: "internship",
	})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("expected ErrValidation, got %v", err)
	}
}
