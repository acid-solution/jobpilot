package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/LeoninCS/jobpilot-next/backend/internal/jdanalysis"
	"github.com/LeoninCS/jobpilot-next/backend/internal/market"
)

type stagePayload struct {
	Result     jdanalysis.Result                 `json:"result"`
	Normalized bool                              `json:"normalized"`
	Aliases    []jdanalysis.AbilityAliasProposal `json:"aliases,omitempty"`
}

func (r *AnalysisRepository) LoadStage(ctx context.Context, job jdanalysis.Job, stage string) (jdanalysis.Result, bool, error) {
	var data []byte
	var status, code string
	var attempts int
	var retryable bool
	err := r.database.QueryRowContext(ctx, `SELECT result,status,error_code,attempts,retryable FROM jd_analysis_stages WHERE job_description_id=$1 AND source_hash=$2 AND stage=$3`, job.JobDescriptionID, sourceHash(job.RawText), stage).Scan(&data, &status, &code, &attempts, &retryable)
	if errors.Is(err, sql.ErrNoRows) {
		return jdanalysis.Result{}, false, nil
	}
	if err != nil {
		return jdanalysis.Result{}, false, err
	}
	var value stagePayload
	if err = json.Unmarshal(data, &value); err != nil {
		return value.Result, false, err
	}
	value.Result.VectorNormalized = value.Normalized
	value.Result.AliasProposals = value.Aliases
	if status == "failed" && (!retryable || attempts >= 3) {
		return value.Result, false, jdanalysis.NewError(code, false, errors.New("JD stage requires manual retry: "+stage))
	}
	return value.Result, status == "succeeded", nil
}
func (r *AnalysisRepository) SaveStage(ctx context.Context, job jdanalysis.Job, stage, status string, result jdanalysis.Result, code string, retry ...bool) error {
	tx, err := r.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = lockUserMutationTx(ctx, tx, job.UserID); err != nil {
		return err
	}
	var held bool
	err = tx.QueryRowContext(ctx, `SELECT TRUE FROM analysis_jobs job JOIN job_descriptions jd ON jd.id=job.job_description_id WHERE job.id=$1 AND job.lease_token=$2 AND job.status='running' AND jd.raw_text=$3 FOR UPDATE OF job`, job.ID, job.LeaseToken, job.RawText).Scan(&held)
	if errors.Is(err, sql.ErrNoRows) {
		return jdanalysis.ErrLeaseLost
	}
	if err != nil {
		return err
	}
	if stage == "ability_resolution" && status == "succeeded" {
		if err = saveJDAbilities(ctx, tx, job, result, true); err != nil {
			return err
		}
	}
	data, err := json.Marshal(stagePayload{Result: result, Normalized: result.VectorNormalized, Aliases: result.AliasProposals})
	if err != nil {
		return err
	}
	retryable := len(retry) > 0 && retry[0]
	_, err = tx.ExecContext(ctx, `INSERT INTO jd_analysis_stages(job_description_id,source_hash,stage,status,result,error_code,retryable,attempts) VALUES($1,$2,$3,$4,$5,$6,$7,CASE WHEN $4='running' THEN 1 ELSE 0 END)
 ON CONFLICT(job_description_id,source_hash,stage) DO UPDATE SET status=$4,result=CASE WHEN $4='running' THEN jd_analysis_stages.result ELSE $5::jsonb END,error_code=$6,retryable=$7,attempts=jd_analysis_stages.attempts+CASE WHEN $4='running' THEN 1 ELSE 0 END,updated_at=now()`, job.JobDescriptionID, sourceHash(job.RawText), stage, status, data, code, retryable)
	if err != nil {
		return err
	}
	if stage == "extraction" && result.ValidationStatus != "" {
		responsibilities, _ := json.Marshal(result.Responsibilities)
		mentions, _ := json.Marshal(result.AbilityMentions)
		_, err = tx.ExecContext(ctx, `UPDATE job_descriptions SET title=$3,company=$4,employment_type=$5,responsibilities=$6,ability_mentions=$7,conditions=$8,document_type=$9,validation_status=$10,validation_reason=$11,analysis_prompt_version=$12,updated_at=now() WHERE id=$1 AND user_id=$2`, job.JobDescriptionID, job.UserID, result.Title, result.Company, result.EmploymentType, responsibilities, mentions, strings.Join(result.Conditions, "；"), result.DocumentType, result.ValidationStatus, result.ValidationReason, jdanalysis.PromptVersion)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *MarketRepository) loadJDAnalysis(ctx context.Context, jd *market.JobDescription) error {
	jd.Analysis = market.AnalysisState{Version: sourceHash(jd.RawText), Stages: []market.AnalysisStage{}, NextPollMS: 2000, Warnings: []string{}}
	rows, err := r.database.QueryContext(ctx, `SELECT stage,status,error_code,attempts,result FROM jd_analysis_stages WHERE job_description_id=$1 AND source_hash=$2 AND stage NOT LIKE 'extraction_window_%' ORDER BY CASE stage WHEN 'extraction' THEN 1 WHEN 'classification' THEN 2 WHEN 'classification_review' THEN 3 ELSE 4 END`, jd.ID, jd.Analysis.Version)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var stage market.AnalysisStage
		var data []byte
		if err = rows.Scan(&stage.Name, &stage.Status, &stage.ErrorCode, &stage.Attempts, &data); err != nil {
			return err
		}
		jd.Analysis.Stages = append(jd.Analysis.Stages, stage)
		if stage.Name == "extraction" {
			var payload stagePayload
			if json.Unmarshal(data, &payload) == nil {
				jd.Analysis.Warnings = payload.Result.Warnings
				jd.Analysis.Requirements = payload.Result.AbilityRequirements
			}
		}
		if stage.Name == "ability_resolution" {
			var payload stagePayload
			if json.Unmarshal(data, &payload) == nil {
				jd.Analysis.Warnings = append(jd.Analysis.Warnings, payload.Result.Warnings...)
			}
		}
		if stage.Status == "succeeded" && (stage.Name == "classification" || stage.Name == "classification_review") {
			var payload stagePayload
			if json.Unmarshal(data, &payload) == nil {
				jd.Analysis.Classifications = payload.Result.Classifications
			}
		}
	}
	if err = rows.Err(); err != nil {
		return err
	}
	jd.Analysis.HasPending = jd.JobStatus == "queued" || jd.JobStatus == "running" || (!jd.AbilityReview.Blocked && jd.AbilityReview.PendingCount > 0) || (!jd.AbilityGrading.Paused && !jd.AbilityReview.Blocked && jd.AbilityReview.FailedCount == 0 && (jd.AbilityGrading.Status == "queued" || jd.AbilityGrading.Status == "running"))
	var automaticRetry bool
	if err = r.database.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM analysis_jobs WHERE job_description_id=$1 AND status='failed' AND attempts<max_attempts)`, jd.ID).Scan(&automaticRetry); err != nil {
		return err
	}
	jd.Analysis.HasPending = jd.Analysis.HasPending || automaticRetry
	if jd.AbilityReview.NextAttemptAt != nil && jd.AbilityReview.NextAttemptAt.After(time.Now().Add(time.Minute)) {
		jd.Analysis.NextPollMS = 30000
	}
	return nil
}
