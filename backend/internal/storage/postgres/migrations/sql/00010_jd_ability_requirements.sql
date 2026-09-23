-- +goose Up
CREATE TABLE job_description_ability_requirements (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    job_description_id UUID NOT NULL REFERENCES job_descriptions(id) ON DELETE CASCADE,
    operator VARCHAR(20) NOT NULL CHECK (operator IN ('single', 'any_of', 'at_least_n')),
    required_count INTEGER NOT NULL CHECK (required_count >= 1),
    evidence TEXT NOT NULL,
    sort_order INTEGER NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (job_description_id, sort_order),
    CHECK (
        (operator IN ('single', 'any_of') AND required_count = 1)
        OR (operator = 'at_least_n' AND required_count >= 2)
    )
);

CREATE INDEX job_description_ability_requirements_jd
    ON job_description_ability_requirements (job_description_id, sort_order);

CREATE TABLE job_description_ability_requirement_options (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    requirement_id UUID NOT NULL REFERENCES job_description_ability_requirements(id) ON DELETE CASCADE,
    ability_id UUID REFERENCES abilities(id),
    raw_label VARCHAR(150) NOT NULL,
    qualifier VARCHAR(300) NOT NULL DEFAULT '',
    evidence TEXT NOT NULL,
    required_level SMALLINT CHECK (required_level BETWEEN 0 AND 5),
    sort_order INTEGER NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (requirement_id, sort_order)
);

CREATE INDEX job_description_ability_requirement_options_ability
    ON job_description_ability_requirement_options (ability_id, requirement_id)
    WHERE ability_id IS NOT NULL;

-- Preserve the previous market profile while old JDs wait for v4 reanalysis.
-- Each legacy flat mention is represented as one mandatory requirement until
-- the new structured result atomically replaces it.
CREATE TEMP TABLE legacy_requirement_map AS
SELECT mention.id AS mention_id,
       gen_random_uuid() AS requirement_id,
       mention.job_description_id,
       ROW_NUMBER() OVER (PARTITION BY mention.job_description_id ORDER BY mention.created_at, mention.id)::INTEGER AS sort_order
FROM job_description_abilities mention;

INSERT INTO job_description_ability_requirements (
    id, job_description_id, operator, required_count, evidence, sort_order
)
SELECT mapping.requirement_id, mapping.job_description_id, 'single', 1,
       mention.evidence, mapping.sort_order
FROM legacy_requirement_map mapping
JOIN job_description_abilities mention ON mention.id = mapping.mention_id;

INSERT INTO job_description_ability_requirement_options (
    requirement_id, ability_id, raw_label, qualifier, evidence, required_level, sort_order
)
SELECT mapping.requirement_id, mention.ability_id, mention.raw_name, '',
       mention.evidence, mention.required_level, 1
FROM legacy_requirement_map mapping
JOIN job_description_abilities mention ON mention.id = mapping.mention_id;

DROP TABLE legacy_requirement_map;

UPDATE analysis_jobs job
SET status = 'queued', attempts = 0, next_attempt_at = NOW(),
    locked_at = NULL, lease_token = NULL, heartbeat_at = NULL,
    lease_expires_at = NULL, last_error = NULL,
    preserve_previous_result = TRUE, updated_at = NOW()
FROM job_descriptions jd
WHERE job.job_description_id = jd.id
  AND job.job_type = 'jd_analysis'
  AND job.status = 'succeeded'
  AND jd.validation_status = 'valid'
  AND COALESCE(jd.analysis_prompt_version, '') <> 'jd-requirements-v5';

-- +goose Down
DROP TABLE IF EXISTS job_description_ability_requirement_options;
DROP TABLE IF EXISTS job_description_ability_requirements;
