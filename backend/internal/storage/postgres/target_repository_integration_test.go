package postgres

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/LeoninCS/jobpilot-next/backend/internal/target"
	"github.com/google/uuid"
)

func TestTargetCatalogSelection(t *testing.T) {
	databaseURL := os.Getenv("JOBPILOT_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("JOBPILOT_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	database, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	repository := NewTargetRepository(database)

	catalog, err := repository.ListCatalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog) < 9 {
		t.Fatalf("expected at least 9 seeded job categories, got %d", len(catalog))
	}
	backend, ai := catalog[0], catalog[3]
	if len(backend.Specialties) < 1 || len(ai.Specialties) < 2 {
		t.Fatalf("seed catalog is incomplete: %#v %#v", backend, ai)
	}
	userID := uuid.New()
	defer func() {
		_, _ = database.ExecContext(context.Background(), "DELETE FROM job_targets WHERE user_id = $1", userID)
	}()
	year := 2027
	result, err := repository.UpsertCurrent(ctx, userID, target.UpsertInput{
		EmploymentType: "internship", GraduationYear: &year,
		Directions: []target.DirectionInput{
			{CategoryID: backend.ID},
			{CategoryID: ai.ID, SpecialtyID: &ai.Specialties[1].ID},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.CatalogStatus != target.CatalogStatusValid || result.Title != "后端开发＋Agent 应用" || len(result.Directions) != 2 {
		t.Fatalf("unexpected target: %#v", result)
	}
	_, err = repository.UpsertCurrent(ctx, userID, target.UpsertInput{
		EmploymentType: "internship",
		Directions:     []target.DirectionInput{{CategoryID: backend.ID, SpecialtyID: &ai.Specialties[1].ID}},
	})
	if !errors.Is(err, target.ErrValidation) {
		t.Fatalf("cross-category specialty should be rejected, got %v", err)
	}
}
