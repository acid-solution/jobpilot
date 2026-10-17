package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/LeoninCS/jobpilot-next/backend/internal/abilitygrading"
	"github.com/LeoninCS/jobpilot-next/backend/internal/jdanalysis"
	"github.com/LeoninCS/jobpilot-next/backend/internal/market"
	"github.com/google/uuid"
)

// loadJDDetails uses four bulk queries for any non-empty list. Its IDs come
// exclusively from an account-scoped list/detail query, never from client input.
// Each result set is closed before the next query, including on Agent transactions.
func (r *MarketRepository) loadJDDetails(ctx context.Context, items []market.JobDescription) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, len(items))
	byID := make(map[uuid.UUID]*market.JobDescription, len(items))
	for i := range items {
		jd := &items[i]
		ids[i] = jd.ID
		byID[jd.ID] = jd
		jd.AbilityGrading.Status = "not_started"
		jd.AbilityLevels = []market.JDAbilityLevel{}
		jd.Analysis = market.AnalysisState{Version: sourceHash(jd.RawText), Stages: []market.AnalysisStage{}, NextPollMS: 2000, Warnings: []string{}, NeedsReanalysis: jd.JobStatus == "succeeded" && jd.ValidationStatus == "valid" && jd.AnalysisPromptVersion != jdanalysis.PromptVersion}
	}
	read := func(query string, scan func(*sql.Rows) error, extra ...any) error {
		args := append([]any{uuidArray(ids)}, extra...)
		rows, err := r.database.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			if err := scan(rows); err != nil {
				return err
			}
		}
		return rows.Err()
	}
	automaticRetry := map[uuid.UUID]bool{}
	err := read(`SELECT jd.id,
	 COUNT(DISTINCT request.id) FILTER(WHERE request.status IN ('queued','running') OR (request.status='failed' AND request.attempts<request.max_attempts)),
	 COUNT(DISTINCT request.id) FILTER(WHERE request.status='failed' AND request.attempts>=request.max_attempts),
	 MIN(request.next_attempt_at) FILTER(WHERE request.status IN ('queued','failed') AND COALESCE(request.last_error,'')<>'platform_model_not_configured'),
	 COALESCE(BOOL_OR(request.last_error='platform_model_not_configured'),FALSE),
	 COALESCE(BOOL_OR(job.status='failed' AND job.attempts<job.max_attempts),FALSE)
	 FROM job_descriptions jd
	 LEFT JOIN job_description_ability_requirements requirement ON requirement.job_description_id=jd.id
	 LEFT JOIN job_description_ability_requirement_options option ON option.requirement_id=requirement.id
	 LEFT JOIN ability_review_requests request ON request.id=option.review_request_id
	 LEFT JOIN analysis_jobs job ON job.job_description_id=jd.id
	 WHERE jd.id=ANY($1::uuid[]) GROUP BY jd.id`, func(rows *sql.Rows) error {
		var id uuid.UUID
		var review market.AbilityReviewSummary
		var retry bool
		if err := rows.Scan(&id, &review.PendingCount, &review.FailedCount, &review.NextAttemptAt, &review.Blocked, &retry); err != nil {
			return err
		}
		byID[id].AbilityReview = review
		automaticRetry[id] = retry
		return nil
	})
	if err != nil {
		return err
	}
	err = read(`SELECT jd.id,job.status,COALESCE(job.last_error,''),
	 (jd.status<>'included' OR jd.validation_status<>'valid') AND job.status IN ('queued','failed'),
	 job.result_fingerprint<>jd_ability_grading_fingerprint(jd.id,$2)
	 FROM jd_ability_level_jobs job JOIN job_descriptions jd ON jd.id=job.job_description_id
	 WHERE jd.id=ANY($1::uuid[])`, func(rows *sql.Rows) error {
		var id uuid.UUID
		var grade market.AbilityGradingSummary
		if err := rows.Scan(&id, &grade.Status, &grade.Error, &grade.Paused, &grade.Stale); err != nil {
			return err
		}
		byID[id].AbilityGrading = grade
		return nil
	}, abilitygrading.PromptVersion)
	if err != nil {
		return err
	}
	err = read(`SELECT assessment.job_description_id,assessment.ability_id,ability.name,assessment.level,
	 assessment.source,assessment.requirement_kind,assessment.evidence_quote,assessment.reason,assessment.confidence
	 FROM jd_ability_level_assessments assessment JOIN abilities ability ON ability.id=assessment.ability_id
	 WHERE assessment.job_description_id=ANY($1::uuid[])
	 ORDER BY assessment.job_description_id,ability.sort_order,CASE assessment.requirement_kind WHEN 'preferred' THEN 1 ELSE 0 END`, func(rows *sql.Rows) error {
		var id uuid.UUID
		var grade market.JDAbilityLevel
		if err := rows.Scan(&id, &grade.AbilityID, &grade.Name, &grade.Level, &grade.Source, &grade.RequirementKind, &grade.Evidence, &grade.Reason, &grade.Confidence); err != nil {
			return err
		}
		byID[id].AbilityLevels = append(byID[id].AbilityLevels, grade)
		return nil
	})
	if err != nil {
		return err
	}
	err = read(`SELECT stage.job_description_id,stage.stage,stage.status,stage.error_code,stage.attempts,stage.result
	 FROM jd_analysis_stages stage JOIN job_descriptions jd ON jd.id=stage.job_description_id
	 WHERE jd.id=ANY($1::uuid[]) AND stage.source_hash=encode(digest(jd.raw_text,'sha256'),'hex')
	 AND stage.stage NOT LIKE 'extraction_window_%'
	 ORDER BY jd.id,CASE stage.stage WHEN 'extraction' THEN 1 WHEN 'classification' THEN 2 WHEN 'classification_review' THEN 3 ELSE 4 END`, func(rows *sql.Rows) error {
		var id uuid.UUID
		var stage market.AnalysisStage
		var data []byte
		if err := rows.Scan(&id, &stage.Name, &stage.Status, &stage.ErrorCode, &stage.Attempts, &data); err != nil {
			return err
		}
		jd := byID[id]
		jd.Analysis.Stages = append(jd.Analysis.Stages, stage)
		var payload stagePayload
		if json.Unmarshal(data, &payload) != nil {
			return nil
		}
		switch {
		case stage.Name == "extraction":
			jd.Analysis.Warnings = payload.Result.Warnings
			jd.Analysis.Requirements = payload.Result.AbilityRequirements
		case stage.Name == "ability_resolution":
			jd.Analysis.Warnings = append(jd.Analysis.Warnings, payload.Result.Warnings...)
		case stage.Status == "succeeded" && (stage.Name == "classification" || stage.Name == "classification_review"):
			jd.Analysis.Classifications = payload.Result.Classifications
		}
		return nil
	})
	if err != nil {
		return err
	}
	now := time.Now()
	for i := range items {
		jd := &items[i]
		jd.Analysis.HasPending = automaticRetry[jd.ID] || jd.JobStatus == "queued" || jd.JobStatus == "running" || (!jd.AbilityReview.Blocked && jd.AbilityReview.PendingCount > 0) || (!jd.AbilityGrading.Paused && !jd.AbilityReview.Blocked && jd.AbilityReview.FailedCount == 0 && (jd.AbilityGrading.Status == "queued" || jd.AbilityGrading.Status == "running"))
		if jd.AbilityReview.NextAttemptAt != nil && jd.AbilityReview.NextAttemptAt.After(now.Add(time.Minute)) {
			jd.Analysis.NextPollMS = 30000
		}
	}
	return nil
}
