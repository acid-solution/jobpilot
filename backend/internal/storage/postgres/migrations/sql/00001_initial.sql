-- +goose Up
CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE job_targets (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL,
    title VARCHAR(120) NOT NULL,
    employment_type VARCHAR(20) NOT NULL CHECK (employment_type IN ('internship', 'campus', 'social')),
    graduation_year SMALLINT,
    directions JSONB NOT NULL DEFAULT '[]'::jsonb,
    is_current BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX job_targets_one_current_per_user
    ON job_targets (user_id)
    WHERE is_current;

CREATE TABLE job_descriptions (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL,
    target_id UUID NOT NULL REFERENCES job_targets(id),
    raw_text TEXT NOT NULL,
    title VARCHAR(200),
    company VARCHAR(200),
    status VARCHAR(20) NOT NULL CHECK (status IN ('processing', 'included', 'reference', 'excluded', 'failed')),
    primary_category VARCHAR(100),
    secondary_category VARCHAR(100),
    relevance_reason TEXT,
    conditions TEXT,
    analysis_version INTEGER NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX job_descriptions_target_created
    ON job_descriptions (user_id, target_id, created_at DESC);

CREATE TABLE analysis_jobs (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL,
    target_id UUID NOT NULL REFERENCES job_targets(id),
    job_description_id UUID NOT NULL REFERENCES job_descriptions(id) ON DELETE CASCADE,
    job_type VARCHAR(40) NOT NULL,
    status VARCHAR(20) NOT NULL CHECK (status IN ('queued', 'running', 'succeeded', 'failed')),
    attempts INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 3,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    locked_at TIMESTAMPTZ,
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (job_description_id, job_type)
);

CREATE INDEX analysis_jobs_claimable
    ON analysis_jobs (status, next_attempt_at, created_at)
    WHERE status IN ('queued', 'failed');

-- +goose Down
DROP TABLE IF EXISTS analysis_jobs;
DROP TABLE IF EXISTS job_descriptions;
DROP TABLE IF EXISTS job_targets;
