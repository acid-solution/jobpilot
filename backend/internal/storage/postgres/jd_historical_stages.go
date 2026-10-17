package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/LeoninCS/jobpilot-next/backend/internal/jdanalysis"
	"github.com/google/uuid"
)

// seedHistoricalJDStages lets an explicit retry reuse an older JD's extracted
// requirements and reviewed classification. It neither queues work on migration
// nor changes the visible result before the staged worker successfully publishes.
func seedHistoricalJDStages(ctx context.Context, tx *sql.Tx, user, jd uuid.UUID) error {
	var result jdanalysis.Result
	var raw, conditions string
	var responsibilities []byte
	err := tx.QueryRowContext(ctx, `SELECT raw_text,COALESCE(title,''),COALESCE(company,''),
		employment_type,responsibilities,COALESCE(conditions,''),COALESCE(NULLIF(document_type,''),'job_description')
		FROM job_descriptions jd WHERE id=$1 AND user_id=$2 AND validation_status='valid'
		AND NOT EXISTS(SELECT 1 FROM jd_analysis_stages stage WHERE stage.job_description_id=jd.id
			AND stage.source_hash=encode(digest(jd.raw_text,'sha256'),'hex') AND stage.stage='extraction')
		FOR UPDATE`, jd, user).Scan(&raw, &result.Title, &result.Company, &result.EmploymentType,
		&responsibilities, &conditions, &result.DocumentType)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if err = json.Unmarshal(responsibilities, &result.Responsibilities); err != nil {
		return err
	}
	result.ValidationStatus = jdanalysis.ValidationValid
	if conditions != "" {
		result.Conditions = []string{conditions}
	}

	rows, err := tx.QueryContext(ctx, `SELECT jsonb_build_object(
		'Operator',requirement.operator,'RequiredCount',requirement.required_count,
		'RequirementKind',requirement.requirement_kind,'Evidence',requirement.evidence,
		'Options',COALESCE((SELECT jsonb_agg(jsonb_build_object(
			'RawLabel',option.raw_label,'AbilityName',COALESCE(ability.name,option.raw_label),
			'CatalogCode',COALESCE(ability.code,''),'Qualifier',option.qualifier,'Evidence',option.evidence,
			'Candidate',option.candidate_metadata,'NormalizationReason',option.normalization_reason)
			ORDER BY option.sort_order) FROM job_description_ability_requirement_options option
			LEFT JOIN abilities ability ON ability.id=option.ability_id WHERE option.requirement_id=requirement.id),'[]'::jsonb))
		FROM job_description_ability_requirements requirement WHERE job_description_id=$1 ORDER BY sort_order`, jd)
	if err != nil {
		return err
	}
	for rows.Next() {
		var data []byte
		var requirement jdanalysis.AbilityRequirement
		if err = rows.Scan(&data); err == nil {
			err = json.Unmarshal(data, &requirement)
		}
		if err != nil {
			rows.Close()
			return err
		}
		result.AbilityRequirements = append(result.AbilityRequirements, requirement)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	rows, err = tx.QueryContext(ctx, `SELECT category.code,specialty.code,classification.relation,
		classification.evidence,COALESCE(classification.reason,'') FROM job_description_classifications classification
		JOIN job_categories category ON category.id=classification.category_id
		JOIN job_specialties specialty ON specialty.id=classification.specialty_id
		WHERE classification.job_description_id=$1 ORDER BY classification.relation,classification.id`, jd)
	if err != nil {
		return err
	}
	for rows.Next() {
		var classification jdanalysis.JobClassification
		if err = rows.Scan(&classification.CategoryCode, &classification.SpecialtyCode,
			&classification.Relation, &classification.Evidence, &classification.Reason); err != nil {
			rows.Close()
			return err
		}
		result.Classifications = append(result.Classifications, classification)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	data, err := json.Marshal(stagePayload{Result: result})
	if err != nil {
		return err
	}
	stages := []string{"extraction"}
	if len(result.Classifications) > 0 {
		stages = append(stages, "classification")
	}
	for _, stage := range stages {
		if _, err = tx.ExecContext(ctx, `INSERT INTO jd_analysis_stages(job_description_id,source_hash,stage,status,result)
			VALUES($1,$2,$3,'succeeded',$4) ON CONFLICT DO NOTHING`, jd, sourceHash(raw), stage, data); err != nil {
			return err
		}
	}
	return nil
}
