-- +goose Up
ALTER TABLE job_categories
    ADD COLUMN normalized_name VARCHAR(180),
    ADD COLUMN source VARCHAR(30) NOT NULL DEFAULT 'seed'
        CHECK (source IN ('seed', 'dynamic_review'));

UPDATE job_categories
SET normalized_name = lower(regexp_replace(name, '[[:space:]_.:/\\-]+', '', 'g'));
ALTER TABLE job_categories ALTER COLUMN normalized_name SET NOT NULL;
CREATE UNIQUE INDEX job_categories_normalized_name_unique
    ON job_categories (normalized_name);

ALTER TABLE job_specialties
    ADD COLUMN normalized_name VARCHAR(180),
    ADD COLUMN source VARCHAR(30) NOT NULL DEFAULT 'seed'
        CHECK (source IN ('seed', 'dynamic_review'));

UPDATE job_specialties
SET normalized_name = lower(regexp_replace(name, '[[:space:]_.:/\\-]+', '', 'g'));
ALTER TABLE job_specialties ALTER COLUMN normalized_name SET NOT NULL;
CREATE UNIQUE INDEX job_specialties_category_normalized_name_unique
    ON job_specialties (category_id, normalized_name);

ALTER TABLE job_descriptions
    ADD COLUMN classification_review_decision VARCHAR(20)
        CHECK (classification_review_decision IN ('accept', 'correct', 'reject')),
    ADD COLUMN classification_review_reason TEXT,
    ADD COLUMN classification_review_provider VARCHAR(30),
    ADD COLUMN classification_review_model VARCHAR(100),
    ADD COLUMN classification_review_prompt_version VARCHAR(80),
    ADD COLUMN classification_review_request_id VARCHAR(200),
    ADD COLUMN classification_review_input_tokens INTEGER,
    ADD COLUMN classification_review_output_tokens INTEGER,
    ADD COLUMN classification_reviewed_at TIMESTAMPTZ;

-- +goose Down
ALTER TABLE job_descriptions
    DROP COLUMN IF EXISTS classification_reviewed_at,
    DROP COLUMN IF EXISTS classification_review_output_tokens,
    DROP COLUMN IF EXISTS classification_review_input_tokens,
    DROP COLUMN IF EXISTS classification_review_request_id,
    DROP COLUMN IF EXISTS classification_review_prompt_version,
    DROP COLUMN IF EXISTS classification_review_model,
    DROP COLUMN IF EXISTS classification_review_provider,
    DROP COLUMN IF EXISTS classification_review_reason,
    DROP COLUMN IF EXISTS classification_review_decision;

DROP INDEX IF EXISTS job_specialties_category_normalized_name_unique;
ALTER TABLE job_specialties
    DROP COLUMN IF EXISTS source,
    DROP COLUMN IF EXISTS normalized_name;

DROP INDEX IF EXISTS job_categories_normalized_name_unique;
ALTER TABLE job_categories
    DROP COLUMN IF EXISTS source,
    DROP COLUMN IF EXISTS normalized_name;
