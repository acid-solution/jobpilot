-- +goose Up
ALTER TABLE abilities
    ADD COLUMN definition TEXT,
    ADD COLUMN normalized_name VARCHAR(180),
    ADD COLUMN source VARCHAR(30) NOT NULL DEFAULT 'seed'
        CHECK (source IN ('seed', 'dynamic_review'));

UPDATE abilities
SET normalized_name = lower(regexp_replace(name, '[[:space:]_.:/\\-]+', '', 'g'));
ALTER TABLE abilities ALTER COLUMN normalized_name SET NOT NULL;
CREATE UNIQUE INDEX abilities_normalized_name_unique ON abilities (normalized_name);

CREATE TABLE ability_review_requests (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    candidate_key VARCHAR(260) NOT NULL,
    normalized_name VARCHAR(180) NOT NULL,
    proposed_name VARCHAR(150) NOT NULL,
    proposed_category_code VARCHAR(80) NOT NULL DEFAULT '',
    proposed_aliases JSONB NOT NULL DEFAULT '[]'::jsonb,
    proposed_definition TEXT NOT NULL DEFAULT '',
    application_reason TEXT NOT NULL DEFAULT '',
    nearest_candidate_codes JSONB NOT NULL DEFAULT '[]'::jsonb,
    evidence_hashes JSONB NOT NULL DEFAULT '[]'::jsonb,
    initiated_by_user_id UUID NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'queued'
        CHECK (status IN ('queued', 'running', 'succeeded', 'failed')),
    decision VARCHAR(30) CHECK (decision IN ('reuse_existing', 'approve_new', 'reject')),
    resolved_ability_id UUID REFERENCES abilities(id),
    decision_reason TEXT,
    provider VARCHAR(30),
    model VARCHAR(100),
    prompt_version VARCHAR(80),
    attempts INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 3,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    lease_token UUID,
    heartbeat_at TIMESTAMPTZ,
    lease_expires_at TIMESTAMPTZ,
    last_error VARCHAR(120),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    decided_at TIMESTAMPTZ
);

CREATE UNIQUE INDEX ability_review_requests_active_candidate
    ON ability_review_requests (candidate_key)
    WHERE status IN ('queued', 'running', 'failed');
CREATE INDEX ability_review_requests_claim
    ON ability_review_requests (next_attempt_at, created_at)
    WHERE status IN ('queued', 'failed');

ALTER TABLE job_description_ability_requirement_options
    ADD COLUMN resolution_status VARCHAR(30) NOT NULL DEFAULT 'resolved'
        CHECK (resolution_status IN ('resolved', 'pending_review', 'review_failed')),
    ADD COLUMN review_request_id UUID REFERENCES ability_review_requests(id),
    ADD COLUMN candidate_metadata JSONB NOT NULL DEFAULT '{}'::jsonb;

UPDATE job_description_ability_requirement_options
SET resolution_status = 'pending_review'
WHERE ability_id IS NULL;

CREATE INDEX jd_ability_options_review_request
    ON job_description_ability_requirement_options (review_request_id)
    WHERE review_request_id IS NOT NULL;

CREATE TABLE platform_model_usage (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    request_id UUID NOT NULL REFERENCES ability_review_requests(id) ON DELETE CASCADE,
    user_id UUID NOT NULL,
    purpose VARCHAR(50) NOT NULL,
    provider VARCHAR(30) NOT NULL,
    model VARCHAR(100) NOT NULL,
    status VARCHAR(20) NOT NULL CHECK (status IN ('dispatched', 'succeeded', 'failed')),
    provider_request_id VARCHAR(200),
    input_tokens INTEGER,
    output_tokens INTEGER,
    error_code VARCHAR(120),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX platform_model_usage_quota
    ON platform_model_usage (created_at, user_id, purpose);

-- Existing unresolved options become real review requests without rerunning JD analysis.
WITH candidates AS (
    SELECT lower(regexp_replace(option.raw_label, '[[:space:]_.:/\\-]+', '', 'g')) AS normalized,
           min(option.raw_label) AS proposed_name,
           min(jd.user_id::text)::uuid AS user_id,
           jsonb_agg(DISTINCT to_jsonb(md5(option.evidence))) AS evidence_hashes
    FROM job_description_ability_requirement_options option
    JOIN job_description_ability_requirements requirement ON requirement.id = option.requirement_id
    JOIN job_descriptions jd ON jd.id = requirement.job_description_id
    WHERE option.ability_id IS NULL
      AND jd.status = 'included' AND jd.validation_status = 'valid'
    GROUP BY lower(regexp_replace(option.raw_label, '[[:space:]_.:/\\-]+', '', 'g'))
), inserted AS (
    INSERT INTO ability_review_requests (
        candidate_key, normalized_name, proposed_name, initiated_by_user_id, evidence_hashes
    )
    SELECT 'unknown:' || normalized, normalized, proposed_name, user_id, evidence_hashes
    FROM candidates
    ON CONFLICT DO NOTHING
    RETURNING id, normalized_name
)
UPDATE job_description_ability_requirement_options option
SET review_request_id = request.id,
    resolution_status = 'pending_review'
FROM job_description_ability_requirements requirement,
     job_descriptions jd,
     ability_review_requests request
WHERE requirement.id = option.requirement_id
  AND jd.id = requirement.job_description_id
  AND jd.status = 'included' AND jd.validation_status = 'valid'
  AND option.ability_id IS NULL
  AND request.candidate_key = 'unknown:' || lower(regexp_replace(option.raw_label, '[[:space:]_.:/\\-]+', '', 'g'))
  AND request.status IN ('queued', 'running', 'failed');

-- +goose Down
ALTER TABLE job_description_ability_requirement_options
    DROP COLUMN IF EXISTS candidate_metadata,
    DROP COLUMN IF EXISTS review_request_id,
    DROP COLUMN IF EXISTS resolution_status;
DROP TABLE IF EXISTS platform_model_usage;
DROP TABLE IF EXISTS ability_review_requests;
DROP INDEX IF EXISTS abilities_normalized_name_unique;
ALTER TABLE abilities
    DROP COLUMN IF EXISTS source,
    DROP COLUMN IF EXISTS normalized_name,
    DROP COLUMN IF EXISTS definition;
