package postgres

import (
	"context"
	"database/sql"
	"testing"

	"github.com/LeoninCS/jobpilot-next/backend/internal/jdimport"
	"github.com/LeoninCS/jobpilot-next/backend/internal/market"
	"github.com/google/uuid"
)

// Seed detection output without a model, then exercise the production import
// transaction. Direct SQL is limited to acquiring this fixture's own lease.
func importJDForTest(t *testing.T, ctx context.Context, db *sql.DB, user, target uuid.UUID, raw string, replacement *uuid.UUID) market.JobDescription {
	t.Helper()
	repo := NewJDImportRepository(db)
	session, err := repo.Create(ctx, user, target, raw, replacement)
	if err != nil {
		t.Fatal(err)
	}
	lease := uuid.New()
	if _, err = db.ExecContext(ctx, `UPDATE jd_imports SET status='running',attempts=attempts+1,lease_token=$2,lease_expires_at=NOW()+INTERVAL '5 minutes' WHERE id=$1`, session.ID, lease); err != nil {
		t.Fatal(err)
	}
	if err = repo.Complete(ctx, jdimport.Job{ID: session.ID, UserID: user, TargetID: target, LeaseToken: lease}, []jdimport.Candidate{{RawText: raw}}); err != nil {
		t.Fatal(err)
	}
	session, err = repo.Get(ctx, user, session.ID)
	if err != nil || len(session.Candidates) != 1 {
		t.Fatalf("fixture detection failed: %+v %v", session, err)
	}
	session, err = repo.Confirm(ctx, user, session.ID, []uuid.UUID{session.Candidates[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	if session.Candidates[0].JDID == nil {
		t.Fatal("confirmed fixture has no JD")
	}
	result, err := NewMarketRepository(db).FindByID(ctx, user, *session.Candidates[0].JDID)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
