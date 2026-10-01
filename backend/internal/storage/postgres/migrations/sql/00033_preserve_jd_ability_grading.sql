-- +goose Up
ALTER TABLE jd_ability_level_jobs
    ADD COLUMN input_fingerprint TEXT NOT NULL DEFAULT '',
    ADD COLUMN result_fingerprint TEXT NOT NULL DEFAULT '',
    ADD COLUMN result_snapshot JSONB NOT NULL DEFAULT '[]'::jsonb;

-- Row IDs, display order and target relevance are deliberately excluded.
-- Recreated options with the same meaning can reuse the last successful grade.
-- +goose StatementBegin
CREATE FUNCTION jd_ability_grading_option_key(option_id UUID) RETURNS JSONB
LANGUAGE SQL STABLE AS $$
    SELECT jsonb_build_object(
        'ability_code', ability.code, 'raw_label', option.raw_label,
        'kind', requirement.requirement_kind, 'operator', requirement.operator,
        'required_count', requirement.required_count,
        'quote', requirement.evidence, 'qualifier', option.qualifier,
        'option_evidence', option.evidence)
    FROM job_description_ability_requirement_options option
    JOIN job_description_ability_requirements requirement ON requirement.id=option.requirement_id
    LEFT JOIN abilities ability ON ability.id=option.ability_id AND ability.is_active
    WHERE option.id=option_id
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION jd_ability_grading_fingerprint(jd_id UUID, grading_prompt TEXT) RETURNS TEXT
LANGUAGE SQL STABLE AS $$
    SELECT encode(digest(jsonb_build_object(
        'prompt', grading_prompt, 'raw_text', jd.raw_text,
        'title', COALESCE(jd.title,''), 'responsibilities', jd.responsibilities,
        'validation', jd.validation_status,
        'options', COALESCE((
            SELECT jsonb_agg(item.value ORDER BY item.value::text)
            FROM (
                SELECT jsonb_build_object(
                    'option', jd_ability_grading_option_key(option.id),
                    'ability_name', ability.name,
                    'levels', COALESCE((SELECT jsonb_agg(jsonb_build_object(
                        'level', level.level, 'description', level.description) ORDER BY level.level)
                        FROM ability_levels level WHERE level.ability_id=ability.id), '[]'::jsonb)
                ) AS value
                FROM job_description_ability_requirements requirement
                JOIN job_description_ability_requirement_options option ON option.requirement_id=requirement.id
                LEFT JOIN abilities ability ON ability.id=option.ability_id AND ability.is_active
                WHERE requirement.job_description_id=jd.id
            ) item
        ), '[]'::jsonb)
    )::text, 'sha256'),'hex')
    FROM job_descriptions jd WHERE jd.id=jd_id
$$;
-- +goose StatementEnd

-- A single last-success snapshot survives option FK cascades during reanalysis.
-- +goose StatementBegin
CREATE FUNCTION jd_ability_grading_snapshot(jd_id UUID) RETURNS JSONB
LANGUAGE SQL STABLE AS $$
    SELECT COALESCE(jsonb_agg(jsonb_build_object(
        'option_key', jd_ability_grading_option_key(grade.option_id),
        'level', grade.level, 'source', grade.source, 'kind', grade.requirement_kind,
        'evidence', grade.evidence_quote, 'reason', grade.reason,
        'confidence', grade.confidence, 'prompt', grade.prompt_version,
        'created_at', grade.created_at)), '[]'::jsonb)
    FROM jd_ability_option_levels grade WHERE grade.job_description_id=jd_id
$$;
-- +goose StatementEnd

-- Only complete option-level results from the current grading prompt are reusable.
UPDATE jd_ability_level_jobs job SET
    input_fingerprint=jd_ability_grading_fingerprint(job.job_description_id,'jd-ability-level-v4-options');
UPDATE jd_ability_level_jobs job SET
    result_fingerprint=job.input_fingerprint,
    result_snapshot=jd_ability_grading_snapshot(job.job_description_id)
WHERE job.prompt_version='jd-ability-level-v4-options'
  AND EXISTS (SELECT 1 FROM jd_ability_option_levels grade WHERE grade.job_description_id=job.job_description_id)
  AND NOT EXISTS (
      SELECT 1 FROM job_description_ability_requirements requirement
      JOIN job_description_ability_requirement_options option ON option.requirement_id=requirement.id
      LEFT JOIN jd_ability_option_levels grade ON grade.option_id=option.id AND grade.ability_id=option.ability_id
          AND grade.prompt_version='jd-ability-level-v4-options'
      WHERE requirement.job_description_id=job.job_description_id
        AND option.ability_id IS NOT NULL AND grade.option_id IS NULL
  );

-- Repair the old worker's false 'not_in_market' successes without invoking a
-- model for complete reusable results. Non-included queued jobs simply wait.
UPDATE jd_ability_level_jobs SET status='succeeded',last_error=NULL
WHERE last_error='not_in_market' AND result_fingerprint<>'';
UPDATE jd_ability_level_jobs SET status='queued',attempts=0,next_attempt_at=NOW(),
    lease_token=NULL,heartbeat_at=NULL,lease_expires_at=NULL,last_error=NULL,completed_at=NULL
WHERE status='succeeded' AND result_fingerprint='';

INSERT INTO jd_ability_level_jobs(user_id,target_id,job_description_id,input_fingerprint)
SELECT jd.user_id,jd.target_id,jd.id,jd_ability_grading_fingerprint(jd.id,'jd-ability-level-v4-options')
FROM job_descriptions jd WHERE jd.validation_status='valid' AND EXISTS (
    SELECT 1 FROM job_description_ability_requirements requirement
    JOIN job_description_ability_requirement_options option ON option.requirement_id=requirement.id
    WHERE requirement.job_description_id=jd.id AND option.ability_id IS NOT NULL
) ON CONFLICT(job_description_id) DO NOTHING;

-- +goose Down
DROP FUNCTION IF EXISTS jd_ability_grading_snapshot(UUID);
DROP FUNCTION IF EXISTS jd_ability_grading_fingerprint(UUID,TEXT);
DROP FUNCTION IF EXISTS jd_ability_grading_option_key(UUID);
ALTER TABLE jd_ability_level_jobs DROP COLUMN result_snapshot,
    DROP COLUMN result_fingerprint, DROP COLUMN input_fingerprint;
