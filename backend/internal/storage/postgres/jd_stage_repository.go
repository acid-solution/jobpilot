package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/LeoninCS/jobpilot-next/backend/internal/jdanalysis"
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
