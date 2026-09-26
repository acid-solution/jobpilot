package postgres

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"

	"github.com/LeoninCS/jobpilot-next/backend/internal/profile"
	"github.com/google/uuid"
)

func TestManualL0AndExplicitSessionConfirmationIntegration(t *testing.T) {
	dsn := os.Getenv("JOBPILOT_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("JOBPILOT_TEST_DATABASE_URL is not set")
	}
	db, err := Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	userID, targetID, jdID := uuid.New(), uuid.New(), uuid.New()
	var abilityID uuid.UUID
	if err := db.QueryRowContext(ctx, `SELECT id FROM abilities WHERE name='Go' AND is_active LIMIT 1`).Scan(&abilityID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM profile_practice_sessions WHERE user_id=$1`, userID)
		_, _ = db.Exec(`DELETE FROM user_capability_level_events WHERE user_id=$1`, userID)
		_, _ = db.Exec(`DELETE FROM user_capability_profiles WHERE user_id=$1`, userID)
		_, _ = db.Exec(`DELETE FROM job_targets WHERE id=$1`, targetID)
	})
	if _, err := db.ExecContext(ctx, `INSERT INTO job_targets(id,user_id,title,employment_type,directions,catalog_status) VALUES($1,$2,'后端开发','internship','[]'::jsonb,'valid')`, targetID, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO job_descriptions(id,user_id,target_id,raw_text,raw_text_hash,status,validation_status,title,responsibilities) VALUES($1,$2,$3,$4,encode(digest($4,'sha256'),'hex'),'included','valid','Go 后端','[]'::jsonb)`, jdID, userID, targetID, "Go 后端开发要求 "+jdID.String()); err != nil {
		t.Fatal(err)
	}
	var reqID uuid.UUID
	if err := db.QueryRowContext(ctx, `INSERT INTO job_description_ability_requirements(job_description_id,operator,required_count,requirement_kind,evidence,sort_order) VALUES($1,'single',1,'required','Go 后端开发要求',1) RETURNING id`, jdID).Scan(&reqID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO job_description_ability_requirement_options(requirement_id,ability_id,raw_label,evidence,sort_order) VALUES($1,$2,'Go','Go 后端开发要求',1)`, reqID, abilityID); err != nil {
		t.Fatal(err)
	}
	r := NewProfileRepository(db)
	a, err := r.SetCapabilityLevel(ctx, userID, abilityID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !a.Assessed || a.CurrentLevel != 0 || a.LevelSource != "manual" {
		t.Fatalf("L0 incorrectly treated as missing: %+v", a)
	}
	// A later material refresh may update evidence, but must not undo the correction.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := refreshEvidenceLevels(ctx, tx, userID, []uuid.UUID{abilityID}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	a, err = r.GetCapability(ctx, userID, abilityID)
	if err != nil || a.CurrentLevel != 0 {
		t.Fatalf("manual L0 overwritten: %+v %v", a, err)
	}
	s, err := r.CreateSession(ctx, userID, profile.Session{AbilityID: abilityID, Mode: profile.ModeValidation, BaseLevel: 0, TargetLevel: 1, Questions: []profile.Question{{ID: "q1", Prompt: "说明 Go context", Dimension: "基础", Position: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	s, err = r.CompleteSession(ctx, userID, s.ID, []profile.AnswerInput{{QuestionID: "q1", Answer: "示例"}}, profile.Evaluation{Passed: true, Verdict: "pass", Summary: "通过"})
	if err != nil {
		t.Fatal(err)
	}
	a, err = r.GetCapability(ctx, userID, abilityID)
	if err != nil || a.CurrentLevel != 0 || s.LevelUpdated {
		t.Fatalf("evaluation upgraded before confirmation: %+v %+v %v", s, a, err)
	}
	s, err = r.ConfirmSession(ctx, userID, s.ID)
	if err != nil || !s.LevelUpdated {
		t.Fatalf("confirmation failed: %+v %v", s, err)
	}
	a, err = r.GetCapability(ctx, userID, abilityID)
	if err != nil || a.CurrentLevel != 1 {
		t.Fatalf("level not raised by one: %+v %v", a, err)
	}
	if _, err := r.ConfirmSession(ctx, userID, s.ID); !errors.Is(err, profile.ErrConflict) {
		t.Fatalf("duplicate confirmation: %v", err)
	}
	if _, err := r.SetCapabilityLevel(ctx, userID, abilityID, 0); err != nil {
		t.Fatal(err)
	}
	var current int
	if err := db.QueryRowContext(ctx, `SELECT current_level FROM user_capability_profiles WHERE user_id=$1 AND ability_id=$2`, userID, abilityID).Scan(&current); err != nil && err != sql.ErrNoRows {
		t.Fatal(err)
	}
	if current != 0 {
		t.Fatal("manual downgrade did not persist")
	}
}
