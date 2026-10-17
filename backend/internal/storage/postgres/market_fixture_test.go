package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/LeoninCS/jobpilot-next/backend/internal/market"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// These helpers seed historical JD states for integration tests. Production writes use JDImportRepository.
func (r *MarketRepository) CreateWithAnalysisJob(
	ctx context.Context,
	userID uuid.UUID,
	targetID uuid.UUID,
	rawText string,
	rawTextHash string,
) (market.JobDescription, error) {
	transaction, err := r.database.BeginTx(ctx, nil)
	if err != nil {
		return market.JobDescription{}, fmt.Errorf("begin jd transaction: %w", err)
	}
	defer transaction.Rollback()

	jdID := uuid.New()
	jobID := uuid.New()
	const insertJD = `
		INSERT INTO job_descriptions (id, user_id, target_id, raw_text, raw_text_hash, status)
		VALUES ($1, $2, $3, $4, $5, 'processing')
		RETURNING id, target_id, COALESCE(title, ''), COALESCE(company, ''), status,
		          COALESCE(primary_category, ''), COALESCE(secondary_category, ''),
		          COALESCE(relevance_reason, ''), COALESCE(conditions, ''), raw_text,
		          created_at, updated_at`

	result, err := scanJD(transaction.QueryRowContext(ctx, insertJD, jdID, userID, targetID, rawText, rawTextHash), "queued")
	if err != nil {
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) && postgresError.Code == "23505" {
			if rollbackErr := transaction.Rollback(); rollbackErr != nil {
				return market.JobDescription{}, errors.Join(err, rollbackErr)
			}
			return market.JobDescription{}, r.duplicateError(ctx, userID, targetID, rawTextHash, uuid.Nil)
		}
		return market.JobDescription{}, err
	}

	const insertJob = `
		INSERT INTO analysis_jobs (
			id, user_id, target_id, job_description_id, job_type, status
		) VALUES ($1, $2, $3, $4, 'jd_analysis', 'queued')`
	if _, err := transaction.ExecContext(ctx, insertJob, jobID, userID, targetID, jdID); err != nil {
		return market.JobDescription{}, fmt.Errorf("insert jd analysis job: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return market.JobDescription{}, fmt.Errorf("commit jd transaction: %w", err)
	}
	result.ValidationStatus = "pending"
	return result, nil
}

func (r *MarketRepository) duplicateError(ctx context.Context, userID, targetID uuid.UUID, rawTextHash string, excludeID uuid.UUID) error {
	query := `SELECT id FROM job_descriptions WHERE user_id=$1 AND target_id=$2 AND raw_text_hash=$3`
	arguments := []any{userID, targetID, rawTextHash}
	if excludeID != uuid.Nil {
		query += ` AND id<>$4`
		arguments = append(arguments, excludeID)
	}
	query += ` LIMIT 1`
	var existingID uuid.UUID
	if err := r.database.QueryRowContext(ctx, query, arguments...).Scan(&existingID); err != nil {
		return market.ErrDuplicateJD
	}
	return &market.DuplicateError{ExistingID: existingID}
}

func scanJD(row rowScanner, jobStatus string) (market.JobDescription, error) {
	var result market.JobDescription
	if err := row.Scan(
		&result.ID,
		&result.TargetID,
		&result.Title,
		&result.Company,
		&result.Status,
		&result.PrimaryCategory,
		&result.SecondaryCategory,
		&result.Reason,
		&result.Conditions,
		&result.RawText,
		&result.CreatedAt,
		&result.UpdatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return market.JobDescription{}, market.ErrNotFound
		}
		return market.JobDescription{}, fmt.Errorf("scan job description: %w", err)
	}
	result.JobStatus = jobStatus
	return result, nil
}
