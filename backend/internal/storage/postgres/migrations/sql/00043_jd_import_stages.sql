-- +goose Up
CREATE TABLE jd_imports (
 id UUID PRIMARY KEY, user_id UUID NOT NULL, target_id UUID NOT NULL REFERENCES job_targets(id) ON DELETE CASCADE,
 replacement_jd_id UUID REFERENCES job_descriptions(id) ON DELETE SET NULL,
 raw_text TEXT NOT NULL, source_hash TEXT NOT NULL,
 status TEXT NOT NULL DEFAULT 'queued' CHECK(status IN('queued','running','ready','confirmed','empty','failed')),
 candidates JSONB NOT NULL DEFAULT '[]', attempts INT NOT NULL DEFAULT 0, max_attempts INT NOT NULL DEFAULT 3,
 next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(), last_error TEXT NOT NULL DEFAULT '',
 lease_token UUID, lease_expires_at TIMESTAMPTZ, created_at TIMESTAMPTZ NOT NULL DEFAULT now(),updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX jd_import_source ON jd_imports(user_id,target_id,source_hash,COALESCE(replacement_jd_id,'00000000-0000-0000-0000-000000000000'::uuid));
CREATE INDEX jd_import_pending ON jd_imports(next_attempt_at) WHERE status IN('queued','running');
CREATE TABLE jd_import_windows (
 import_id UUID NOT NULL REFERENCES jd_imports(id) ON DELETE CASCADE,
 window_index INT NOT NULL, candidates JSONB NOT NULL,
 PRIMARY KEY(import_id,window_index)
);
CREATE TABLE jd_analysis_stages (
 job_description_id UUID NOT NULL REFERENCES job_descriptions(id) ON DELETE CASCADE,
 source_hash TEXT NOT NULL, stage TEXT NOT NULL CHECK(stage IN('extraction','classification','ability_resolution','classification_review') OR stage LIKE 'extraction_window_%'),
 status TEXT NOT NULL CHECK(status IN('running','succeeded','failed')), result JSONB,
 error_code TEXT NOT NULL DEFAULT '', retryable BOOLEAN NOT NULL DEFAULT FALSE, attempts INT NOT NULL DEFAULT 0,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now(), PRIMARY KEY(job_description_id,source_hash,stage)
);
ALTER TABLE job_descriptions ADD COLUMN import_id UUID REFERENCES jd_imports(id) ON DELETE SET NULL;
-- The analysis worker now owns normalization as a versioned stage. Historical
-- normalization jobs remain as audit records; they must not trigger paid reruns.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION enqueue_jd_normalization() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE changed BOOLEAN;
BEGIN
    changed := TG_OP='INSERT';
    IF TG_OP='UPDATE' THEN
        changed := NEW.raw_text IS DISTINCT FROM OLD.raw_text
            OR NEW.analysis_prompt_version IS DISTINCT FROM OLD.analysis_prompt_version
            OR NEW.normalization_prompt_version IS DISTINCT FROM OLD.normalization_prompt_version
            OR NEW.validation_status IS DISTINCT FROM OLD.validation_status;
    END IF;
    IF NEW.analysis_prompt_version<>'jd-source-stages-v15' AND NEW.validation_status='valid' AND NEW.normalization_prompt_version<>'jd-vector-candidates-v12'
       AND EXISTS(SELECT 1 FROM job_description_ability_requirements r WHERE r.job_description_id=NEW.id)
       AND changed THEN
        INSERT INTO jd_normalization_jobs(user_id,job_description_id,source_hash)
        VALUES(NEW.user_id,NEW.id,encode(digest(NEW.raw_text,'sha256'),'hex'))
        ON CONFLICT(job_description_id) DO UPDATE SET
            source_hash=EXCLUDED.source_hash,status='queued',attempts=0,next_attempt_at=NOW(),
            lease_token=NULL,heartbeat_at=NULL,lease_expires_at=NULL,last_error='',updated_at=NOW();
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd
-- +goose Down
ALTER TABLE job_descriptions DROP COLUMN import_id;
DROP TABLE jd_analysis_stages;
DROP TABLE jd_import_windows;
DROP TABLE jd_imports;
