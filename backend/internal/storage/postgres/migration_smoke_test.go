package postgres

import (
	"context"
	"os"
	"testing"

	"github.com/LeoninCS/jobpilot-next/backend/internal/storage/postgres/migrations"
	"github.com/pressly/goose/v3"
)

func TestMigrationsSmoke(t *testing.T) {
	dsn := os.Getenv("JOBPILOT_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("JOBPILOT_TEST_DATABASE_URL is not set")
	}
	db, err := Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	goose.SetBaseFS(migrations.Files)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	if err := goose.Up(db, "sql"); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"jd_ability_option_levels", "knowledge_gap_reports", "user_capability_level_events", "project_recommendation_jobs", "project_recommendation_reports"} {
		var found bool
		if err := db.QueryRow(`SELECT to_regclass($1) IS NOT NULL`, table).Scan(&found); err != nil || !found {
			t.Fatalf("table %s missing: %v", table, err)
		}
	}
}
