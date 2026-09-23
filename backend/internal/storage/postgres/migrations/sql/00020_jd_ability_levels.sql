-- +goose Up
ALTER TABLE job_description_ability_requirements
    ADD COLUMN requirement_kind VARCHAR(20) NOT NULL DEFAULT 'unspecified'
        CHECK (requirement_kind IN ('required', 'preferred', 'unspecified'));

CREATE TABLE jd_ability_level_jobs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL,
    target_id UUID NOT NULL REFERENCES job_targets(id) ON DELETE CASCADE,
    job_description_id UUID NOT NULL UNIQUE REFERENCES job_descriptions(id) ON DELETE CASCADE,
    status VARCHAR(20) NOT NULL DEFAULT 'queued'
        CHECK (status IN ('queued', 'running', 'succeeded', 'failed')),
    attempts INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 3,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    lease_token UUID,
    heartbeat_at TIMESTAMPTZ,
    lease_expires_at TIMESTAMPTZ,
    last_error VARCHAR(160),
    provider VARCHAR(30),
    model VARCHAR(100),
    prompt_version VARCHAR(80),
    provider_request_id VARCHAR(200),
    input_tokens INTEGER,
    output_tokens INTEGER,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ
);

CREATE INDEX jd_ability_level_jobs_claim
    ON jd_ability_level_jobs (next_attempt_at, created_at)
    WHERE status IN ('queued', 'failed');

CREATE TABLE jd_ability_level_assessments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    job_description_id UUID NOT NULL REFERENCES job_descriptions(id) ON DELETE CASCADE,
    ability_id UUID NOT NULL REFERENCES abilities(id),
    level SMALLINT NOT NULL CHECK (level BETWEEN 1 AND 5),
    source VARCHAR(20) NOT NULL CHECK (source IN ('explicit', 'inferred')),
    requirement_kind VARCHAR(20) NOT NULL
        CHECK (requirement_kind IN ('required', 'preferred', 'unspecified')),
    evidence_quote TEXT NOT NULL,
    reason TEXT NOT NULL,
    confidence DOUBLE PRECISION NOT NULL CHECK (confidence BETWEEN 0 AND 1),
    level_standard_version INTEGER NOT NULL DEFAULT 1,
    prompt_version VARCHAR(80) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (job_description_id, ability_id, requirement_kind)
);

CREATE INDEX jd_ability_level_assessments_ability
    ON jd_ability_level_assessments (ability_id, job_description_id);

INSERT INTO jd_ability_level_jobs (user_id, target_id, job_description_id, status)
SELECT jd.user_id, jd.target_id, jd.id, 'queued'
FROM job_descriptions jd
WHERE jd.status = 'included'
  AND jd.validation_status = 'valid'
  AND EXISTS (
      SELECT 1
      FROM job_description_ability_requirements requirement
      JOIN job_description_ability_requirement_options option
        ON option.requirement_id = requirement.id
      WHERE requirement.job_description_id = jd.id
        AND option.ability_id IS NOT NULL
  )
ON CONFLICT (job_description_id) DO NOTHING;

-- +goose Down
DROP TABLE IF EXISTS jd_ability_level_assessments;
DROP TABLE IF EXISTS jd_ability_level_jobs;
ALTER TABLE job_description_ability_requirements
    DROP COLUMN IF EXISTS requirement_kind;
