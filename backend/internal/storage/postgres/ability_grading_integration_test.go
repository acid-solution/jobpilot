package postgres

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/LeoninCS/jobpilot-next/backend/internal/abilitygrading"
	"github.com/google/uuid"
)

func TestAbilityGradingLeaseAndAtomicReplacementIntegration(t *testing.T) {
	dsn := os.Getenv("JOBPILOT_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("JOBPILOT_TEST_DATABASE_URL is not set")
	}
	database, err := Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	userID, targetID, jdID := uuid.New(), uuid.New(), uuid.New()
	t.Cleanup(func() { _, _ = database.Exec("DELETE FROM job_targets WHERE id=$1", targetID) })
	if _, err := database.ExecContext(ctx, `INSERT INTO job_targets(id,user_id,title,employment_type,directions,catalog_status)
		VALUES($1,$2,'后端开发','internship','[]'::jsonb,'valid')`, targetID, userID); err != nil {
		t.Fatal(err)
	}
	raw := "Go 后端实习生，要求能够独立使用 Go 完成服务端功能。"
	if _, err := database.ExecContext(ctx, `INSERT INTO job_descriptions(id,user_id,target_id,raw_text,raw_text_hash,status,validation_status,title,responsibilities)
		VALUES($1,$2,$3,$4,encode(digest($4,'sha256'),'hex'),'included','valid','Go 后端实习生','["负责服务端功能"]'::jsonb)`, jdID, userID, targetID, raw); err != nil {
		t.Fatal(err)
	}
	var requirementID uuid.UUID
	if err := database.QueryRowContext(ctx, `INSERT INTO job_description_ability_requirements(job_description_id,operator,required_count,requirement_kind,evidence,sort_order)
		VALUES($1,'single',1,'required','能够独立使用 Go 完成服务端功能',1) RETURNING id`, jdID).Scan(&requirementID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO job_description_ability_requirement_options(requirement_id,ability_id,raw_label,evidence,sort_order)
		SELECT $1,id,'Go','能够独立使用 Go 完成服务端功能',1 FROM abilities WHERE name='Go'`, requirementID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO jd_ability_level_jobs(user_id,target_id,job_description_id) VALUES($1,$2,$3)`, userID, targetID, jdID); err != nil {
		t.Fatal(err)
	}

	repository := NewAbilityGradingRepository(database)
	job, err := repository.Claim(ctx, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(job.Abilities) != 1 || len(job.Abilities[0].Levels) != 6 {
		t.Fatalf("unexpected grading input: %#v", job.Abilities)
	}
	result := abilitygrading.Result{Assessments: []abilitygrading.Assessment{{
		AbilityCode: job.Abilities[0].Code, Level: 2, Source: "explicit", RequirementKind: "required",
		EvidenceQuote: "能够独立使用 Go 完成服务端功能", Reason: "原文明确要求独立完成服务端功能。", Confidence: 0.9,
	}}, Provider: "test", Model: "test", PromptVersion: abilitygrading.PromptVersion}
	stale := job
	stale.LeaseToken = uuid.New()
	if err := repository.Complete(ctx, stale, result); !errors.Is(err, abilitygrading.ErrLeaseLost) {
		t.Fatalf("stale worker should lose lease, got %v", err)
	}
	if err := repository.Complete(ctx, job, result); err != nil {
		t.Fatal(err)
	}
	var level, count int
	if err := database.QueryRowContext(ctx, `SELECT MAX(level),COUNT(*) FROM jd_ability_level_assessments WHERE job_description_id=$1`, jdID).Scan(&level, &count); err != nil {
		t.Fatal(err)
	}
	if level != 2 || count != 1 {
		t.Fatalf("unexpected persisted assessment level=%d count=%d", level, count)
	}
}
