-- +goose Up
ALTER TABLE analysis_jobs
    ADD COLUMN lease_token UUID,
    ADD COLUMN heartbeat_at TIMESTAMPTZ,
    ADD COLUMN lease_expires_at TIMESTAMPTZ;

UPDATE analysis_jobs
SET heartbeat_at = COALESCE(locked_at, updated_at),
    lease_expires_at = COALESCE(locked_at, updated_at) + INTERVAL '5 minutes'
WHERE status = 'running';

CREATE INDEX analysis_jobs_expired_lease
    ON analysis_jobs (lease_expires_at)
    WHERE status = 'running';

ALTER TABLE job_descriptions
    ADD COLUMN document_type VARCHAR(30)
        CHECK (document_type IN ('job_description', 'partial_job_description', 'non_job_description', 'unreadable')),
    ADD COLUMN validation_status VARCHAR(20) NOT NULL DEFAULT 'pending'
        CHECK (validation_status IN ('pending', 'valid', 'incomplete', 'invalid')),
    ADD COLUMN validation_reason TEXT;

-- +goose Down
ALTER TABLE job_descriptions
    DROP COLUMN IF EXISTS validation_reason,
    DROP COLUMN IF EXISTS validation_status,
    DROP COLUMN IF EXISTS document_type;

DROP INDEX IF EXISTS analysis_jobs_expired_lease;

ALTER TABLE analysis_jobs
    DROP COLUMN IF EXISTS lease_expires_at,
    DROP COLUMN IF EXISTS heartbeat_at,
    DROP COLUMN IF EXISTS lease_token;
