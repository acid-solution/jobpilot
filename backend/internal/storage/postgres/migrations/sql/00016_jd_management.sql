-- +goose Up
CREATE EXTENSION IF NOT EXISTS pgcrypto;

ALTER TABLE job_descriptions ADD COLUMN raw_text_hash CHAR(64);

UPDATE job_descriptions
SET raw_text_hash = encode(
    digest(btrim(replace(replace(raw_text, E'\r\n', E'\n'), E'\r', E'\n')), 'sha256'),
    'hex'
);

-- Historical exact duplicates are retained, but only the newest row receives
-- the canonical hash. This makes the migration non-destructive while ensuring
-- all future writes are unique within one user's current target.
WITH ranked AS (
    SELECT id,
           ROW_NUMBER() OVER (
               PARTITION BY user_id, target_id, raw_text_hash
               ORDER BY created_at DESC, id DESC
           ) AS duplicate_rank
    FROM job_descriptions
)
UPDATE job_descriptions jd
SET raw_text_hash = encode(digest(jd.raw_text || jd.id::text, 'sha256'), 'hex')
FROM ranked
WHERE ranked.id = jd.id AND ranked.duplicate_rank > 1;

ALTER TABLE job_descriptions ALTER COLUMN raw_text_hash SET NOT NULL;
CREATE UNIQUE INDEX job_descriptions_user_target_raw_text_unique
    ON job_descriptions (user_id, target_id, raw_text_hash);

-- +goose Down
DROP INDEX IF EXISTS job_descriptions_user_target_raw_text_unique;
ALTER TABLE job_descriptions DROP COLUMN IF EXISTS raw_text_hash;
