package target

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrNotFound         = errors.New("current target not found")
	ErrValidation       = errors.New("invalid target")
	ErrNeedsReselection = errors.New("target directions must be reselected")
)

const (
	CatalogStatusValid               = "valid"
	CatalogStatusReselectionRequired = "reselection_required"
)

type Specialty struct {
	ID   uuid.UUID `json:"id"`
	Code string    `json:"code"`
	Name string    `json:"name"`
}

type Category struct {
	ID          uuid.UUID   `json:"id"`
	Code        string      `json:"code"`
	Name        string      `json:"name"`
	Specialties []Specialty `json:"specialties"`
}

type Direction struct {
	CategoryID  uuid.UUID  `json:"category_id"`
	Category    string     `json:"category"`
	SpecialtyID *uuid.UUID `json:"specialty_id,omitempty"`
	Specialty   string     `json:"specialty,omitempty"`
}

type DirectionInput struct {
	CategoryID  uuid.UUID  `json:"category_id"`
	SpecialtyID *uuid.UUID `json:"specialty_id,omitempty"`
}

type Target struct {
	ID             uuid.UUID   `json:"id"`
	UserID         uuid.UUID   `json:"-"`
	Title          string      `json:"title"`
	EmploymentType string      `json:"employment_type"`
	GraduationYear *int        `json:"graduation_year,omitempty"`
	Directions     []Direction `json:"directions"`
	CatalogStatus  string      `json:"catalog_status"`
	CreatedAt      time.Time   `json:"created_at"`
	UpdatedAt      time.Time   `json:"updated_at"`
}

type UpsertInput struct {
	EmploymentType string           `json:"employment_type"`
	GraduationYear *int             `json:"graduation_year"`
	Directions     []DirectionInput `json:"directions"`
}

type Repository interface {
	FindCurrent(context.Context, uuid.UUID) (Target, error)
	UpsertCurrent(context.Context, uuid.UUID, UpsertInput) (Target, error)
	ListCatalog(context.Context) ([]Category, error)
}

type Service struct {
	repository Repository
}

func NewService(repository Repository) *Service {
	return &Service{repository: repository}
}

func (s *Service) Current(ctx context.Context, userID uuid.UUID) (Target, error) {
	return s.repository.FindCurrent(ctx, userID)
}

func (s *Service) Catalog(ctx context.Context) ([]Category, error) {
	return s.repository.ListCatalog(ctx)
}

func (s *Service) UpsertCurrent(ctx context.Context, userID uuid.UUID, input UpsertInput) (Target, error) {
	input.EmploymentType = strings.TrimSpace(input.EmploymentType)
	if err := validate(input); err != nil {
		return Target{}, err
	}
	return s.repository.UpsertCurrent(ctx, userID, input)
}

func validate(input UpsertInput) error {
	switch input.EmploymentType {
	case "internship", "campus", "social":
	default:
		return ErrValidation
	}
	if input.GraduationYear != nil && (*input.GraduationYear < 2000 || *input.GraduationYear > 2100) {
		return ErrValidation
	}
	if len(input.Directions) == 0 || len(input.Directions) > 5 {
		return ErrValidation
	}
	seen := make(map[string]struct{}, len(input.Directions))
	for _, direction := range input.Directions {
		if direction.CategoryID == uuid.Nil {
			return ErrValidation
		}
		key := direction.CategoryID.String()
		if direction.SpecialtyID != nil {
			if *direction.SpecialtyID == uuid.Nil {
				return ErrValidation
			}
			key = direction.SpecialtyID.String()
		}
		if _, exists := seen[key]; exists {
			return ErrValidation
		}
		seen[key] = struct{}{}
	}
	return nil
}
