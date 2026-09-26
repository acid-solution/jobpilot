package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/LeoninCS/jobpilot-next/backend/internal/projectrecs"
	"github.com/google/uuid"
)

type ProjectRecommendationRepository struct{ database *sql.DB }

func NewProjectRecommendationRepository(db *sql.DB) *ProjectRecommendationRepository {
	return &ProjectRecommendationRepository{db}
}

func (r *ProjectRecommendationRepository) Get(ctx context.Context, userID uuid.UUID, signature string) (*projectrecs.Stored, *projectrecs.JobView, error) {
	var stored *projectrecs.Stored
	var raw []byte
	var hash string
	var selected sql.NullString
	err := r.database.QueryRowContext(ctx, `SELECT source_hash,report,selected_project_id FROM project_recommendation_reports WHERE user_id=$1 AND goal_signature=$2`, userID, signature).Scan(&hash, &raw, &selected)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, nil, err
	}
	if err == nil {
		stored = &projectrecs.Stored{SourceHash: hash, SelectedProjectID: selected.String}
		if err = json.Unmarshal(raw, &stored.Report); err != nil {
			return nil, nil, err
		}
	}
	var job projectrecs.JobView
	var code string
	var nextAttempt time.Time
	err = r.database.QueryRowContext(ctx, `SELECT id,status,phase,
		jsonb_array_length(COALESCE(drafts,'[]'::jsonb)),jsonb_array_length(COALESCE(research,'[]'::jsonb)),
		attempts,max_attempts,next_attempt_at,last_error,updated_at
		FROM project_recommendation_jobs WHERE user_id=$1 AND goal_signature=$2 ORDER BY created_at DESC LIMIT 1`, userID, signature).Scan(
		&job.ID, &job.Status, &job.Phase, &job.DraftCount, &job.ResearchedCount,
		&job.Attempts, &job.MaxAttempts, &nextAttempt, &code, &job.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return stored, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	job.ErrorCode = code
	if job.Status == "queued" {
		job.NextAttemptAt = &nextAttempt
	}
	return stored, &job, nil
}
func (r *ProjectRecommendationRepository) Enqueue(ctx context.Context, userID uuid.UUID, ss projectrecs.Snapshot, adjustment string) (projectrecs.JobView, error) {
	raw, err := json.Marshal(ss.Input)
	if err != nil {
		return projectrecs.JobView{}, err
	}
	var job projectrecs.JobView
	err = r.database.QueryRowContext(ctx, `INSERT INTO project_recommendation_jobs(user_id,goal_signature,target_id,source_hash,input,adjustment)
		VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING RETURNING id,status,phase,updated_at`, userID, ss.GoalSignature, ss.TargetID, ss.SourceHash, raw, adjustment).Scan(&job.ID, &job.Status, &job.Phase, &job.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return job, projectrecs.ErrConflict
	}
	return job, err
}
func (r *ProjectRecommendationRepository) Select(ctx context.Context, userID uuid.UUID, signature string, reportID uuid.UUID, projectID string) error {
	tx, err := r.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var id uuid.UUID
	var raw []byte
	err = tx.QueryRowContext(ctx, `SELECT report_id,report FROM project_recommendation_reports WHERE user_id=$1 AND goal_signature=$2 FOR UPDATE`, userID, signature).Scan(&id, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return projectrecs.ErrNotFound
	}
	if err != nil {
		return err
	}
	if id != reportID {
		return projectrecs.ErrConflict
	}
	var report projectrecs.Report
	if err = json.Unmarshal(raw, &report); err != nil {
		return err
	}
	found := false
	for _, p := range report.Projects {
		if p.ID == projectID {
			found = true
			break
		}
	}
	if !found {
		return projectrecs.ErrNotFound
	}
	_, err = tx.ExecContext(ctx, `UPDATE project_recommendation_reports SET selected_project_id=$3 WHERE user_id=$1 AND goal_signature=$2`, userID, signature, projectID)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (r *ProjectRecommendationRepository) RecoverExpired(ctx context.Context) error {
	_, err := r.database.ExecContext(ctx, `UPDATE project_recommendation_jobs SET status=CASE WHEN attempts<max_attempts THEN 'queued' ELSE 'failed' END,
		next_attempt_at=NOW(),lease_token=NULL,heartbeat_at=NULL,lease_expires_at=NULL,last_error='worker_interrupted',updated_at=NOW()
		WHERE status='running' AND lease_expires_at<NOW()`)
	return err
}
func (r *ProjectRecommendationRepository) Claim(ctx context.Context, lease time.Duration) (projectrecs.Job, error) {
	var job projectrecs.Job
	var raw, drafts, research []byte
	var token = uuid.New()
	var adjustment string
	err := r.database.QueryRowContext(ctx, `WITH candidate AS (
		SELECT id FROM project_recommendation_jobs WHERE status='queued' AND next_attempt_at<=NOW()
		ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1)
		UPDATE project_recommendation_jobs j SET status='running',attempts=j.attempts+1,lease_token=$1,
		heartbeat_at=NOW(),lease_expires_at=NOW()+($2::bigint*INTERVAL '1 millisecond'),updated_at=NOW()
		FROM candidate WHERE j.id=candidate.id
		RETURNING j.id,j.user_id,j.target_id,j.goal_signature,j.source_hash,j.input,j.adjustment,j.phase,
		COALESCE(j.drafts,'[]'::jsonb),COALESCE(j.research,'[]'::jsonb),j.attempts,j.max_attempts,j.lease_token`, token, lease.Milliseconds()).Scan(
		&job.ID, &job.UserID, &job.TargetID, &job.GoalSignature, &job.SourceHash, &raw, &adjustment, &job.Phase, &drafts, &research, &job.Attempts, &job.MaxAttempts, &job.LeaseToken)
	if errors.Is(err, sql.ErrNoRows) {
		return job, projectrecs.ErrNoJob
	}
	if err != nil {
		return job, err
	}
	job.Adjustment = adjustment
	if err = json.Unmarshal(raw, &job.Input); err != nil {
		return job, err
	}
	if err = json.Unmarshal(drafts, &job.Drafts); err != nil {
		return job, err
	}
	if err = json.Unmarshal(research, &job.Research); err != nil {
		return job, err
	}
	return job, nil
}
func (r *ProjectRecommendationRepository) Heartbeat(ctx context.Context, job projectrecs.Job, lease time.Duration) error {
	result, err := r.database.ExecContext(ctx, `UPDATE project_recommendation_jobs SET heartbeat_at=NOW(),lease_expires_at=NOW()+($3::bigint*INTERVAL '1 millisecond'),updated_at=NOW()
		WHERE id=$1 AND status='running' AND lease_token=$2`, job.ID, job.LeaseToken, lease.Milliseconds())
	return leaseResult(result, err)
}
func (r *ProjectRecommendationRepository) SaveDrafts(ctx context.Context, job projectrecs.Job, drafts []projectrecs.Draft) error {
	raw, err := json.Marshal(drafts)
	if err != nil {
		return err
	}
	result, err := r.database.ExecContext(ctx, `UPDATE project_recommendation_jobs SET drafts=$3,phase='research',updated_at=NOW() WHERE id=$1 AND status='running' AND lease_token=$2`, job.ID, job.LeaseToken, raw)
	return leaseResult(result, err)
}
func (r *ProjectRecommendationRepository) SaveResearchProgress(ctx context.Context, job projectrecs.Job, research []projectrecs.Research) error {
	raw, err := json.Marshal(research)
	if err != nil {
		return err
	}
	result, err := r.database.ExecContext(ctx, `UPDATE project_recommendation_jobs SET research=$3,phase='research',updated_at=NOW()
		WHERE id=$1 AND status='running' AND lease_token=$2`, job.ID, job.LeaseToken, raw)
	return leaseResult(result, err)
}
func (r *ProjectRecommendationRepository) SaveResearch(ctx context.Context, job projectrecs.Job, research []projectrecs.Research) error {
	raw, err := json.Marshal(research)
	if err != nil {
		return err
	}
	result, err := r.database.ExecContext(ctx, `UPDATE project_recommendation_jobs SET research=$3,phase='compare',updated_at=NOW() WHERE id=$1 AND status='running' AND lease_token=$2`, job.ID, job.LeaseToken, raw)
	return leaseResult(result, err)
}
func (r *ProjectRecommendationRepository) Complete(ctx context.Context, job projectrecs.Job, report projectrecs.Report) error {
	raw, err := json.Marshal(report)
	if err != nil {
		return err
	}
	tx, err := r.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockUserMutationTx(ctx, tx, job.UserID); err != nil {
		return err
	}
	var active bool
	err = tx.QueryRowContext(ctx, `SELECT TRUE FROM project_recommendation_jobs WHERE id=$1 AND status='running' AND lease_token=$2 FOR UPDATE`, job.ID, job.LeaseToken).Scan(&active)
	if errors.Is(err, sql.ErrNoRows) {
		return projectrecs.ErrLeaseLost
	}
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO project_recommendation_reports(user_id,goal_signature,report_id,target_id,source_hash,report,selected_project_id)
		VALUES($1,$2,$3,$4,$5,$6,NULL) ON CONFLICT(user_id,goal_signature) DO UPDATE SET report_id=EXCLUDED.report_id,target_id=EXCLUDED.target_id,
		source_hash=EXCLUDED.source_hash,report=EXCLUDED.report,selected_project_id=NULL,generated_at=NOW()`, job.UserID, job.GoalSignature, report.ID, job.TargetID, job.SourceHash, raw)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE project_recommendation_jobs SET status='succeeded',lease_token=NULL,heartbeat_at=NULL,lease_expires_at=NULL,
		last_error='',completed_at=NOW(),updated_at=NOW() WHERE id=$1`, job.ID)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (r *ProjectRecommendationRepository) Fail(ctx context.Context, job projectrecs.Job, code string, retryable bool) error {
	status := "failed"
	delay := 0
	if retryable && job.Attempts < job.MaxAttempts {
		status = "queued"
		delay = 10 * (1 << (job.Attempts - 1))
	}
	result, err := r.database.ExecContext(ctx, `UPDATE project_recommendation_jobs SET status=$3,next_attempt_at=NOW()+($4::int*INTERVAL '1 second'),
		lease_token=NULL,heartbeat_at=NULL,lease_expires_at=NULL,last_error=$5,updated_at=NOW()
		WHERE id=$1 AND status='running' AND lease_token=$2`, job.ID, job.LeaseToken, status, delay, code)
	return leaseResult(result, err)
}
func leaseResult(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read affected rows: %w", err)
	}
	if n == 0 {
		return projectrecs.ErrLeaseLost
	}
	return nil
}
