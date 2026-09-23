-- +goose Up
CREATE TABLE model_configs (
    user_id UUID NOT NULL,
    provider VARCHAR(30) NOT NULL CHECK (provider IN ('deepseek')),
    model VARCHAR(80) NOT NULL,
    api_key_ciphertext BYTEA NOT NULL,
    api_key_hint VARCHAR(16) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, provider)
);

ALTER TABLE job_descriptions
    ADD COLUMN employment_type VARCHAR(20) NOT NULL DEFAULT 'unknown',
    ADD COLUMN responsibilities JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN ability_mentions JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN analysis_provider VARCHAR(30),
    ADD COLUMN analysis_model VARCHAR(80),
    ADD COLUMN analysis_prompt_version VARCHAR(30),
    ADD COLUMN analysis_completed_at TIMESTAMPTZ;

-- +goose Down
ALTER TABLE job_descriptions
    DROP COLUMN IF EXISTS analysis_completed_at,
    DROP COLUMN IF EXISTS analysis_prompt_version,
    DROP COLUMN IF EXISTS analysis_model,
    DROP COLUMN IF EXISTS analysis_provider,
    DROP COLUMN IF EXISTS ability_mentions,
    DROP COLUMN IF EXISTS responsibilities,
    DROP COLUMN IF EXISTS employment_type;

DROP TABLE IF EXISTS model_configs;
