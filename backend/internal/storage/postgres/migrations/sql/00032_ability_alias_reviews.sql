-- +goose Up
-- Keep the database and Go normalization rules in sync, including NFKC.
CREATE FUNCTION normalize_ability_name(value TEXT) RETURNS TEXT
LANGUAGE SQL IMMUTABLE STRICT PARALLEL SAFE
AS $$ SELECT lower(regexp_replace(normalize(value, NFKC), '[[:space:]_.:/\\·-]+', '', 'g')) $$;

ALTER TABLE ability_review_requests
    ADD COLUMN review_type VARCHAR(20) NOT NULL DEFAULT 'ability' CHECK (review_type IN ('ability','alias')),
    ADD COLUMN target_ability_id UUID REFERENCES abilities(id),
    ADD CONSTRAINT ability_review_target CHECK ((review_type='alias') = (target_ability_id IS NOT NULL));
ALTER TABLE ability_review_requests DROP CONSTRAINT ability_review_requests_decision_check;
ALTER TABLE ability_review_requests ADD CONSTRAINT ability_review_requests_decision_check
    CHECK (decision IN ('reuse_existing','approve_new','reject','approve_alias','reject_alias'));

ALTER TABLE job_description_ability_requirement_options ADD COLUMN normalization_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE user_profile_evidence
    ADD COLUMN raw_label VARCHAR(150) NOT NULL DEFAULT '',
    ADD COLUMN normalization_reason TEXT NOT NULL DEFAULT '';

CREATE TABLE ability_alias_review_sources (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    review_request_id UUID NOT NULL REFERENCES ability_review_requests(id) ON DELETE CASCADE,
    job_description_id UUID REFERENCES job_descriptions(id) ON DELETE CASCADE,
    material_id UUID REFERENCES user_profile_materials(id) ON DELETE CASCADE,
    evidence TEXT NOT NULL,
    source_hash TEXT NOT NULL,
    normalization_reason TEXT NOT NULL,
    CHECK ((job_description_id IS NOT NULL) <> (material_id IS NOT NULL))
);
CREATE UNIQUE INDEX alias_review_jd_source ON ability_alias_review_sources(review_request_id,job_description_id,md5(evidence)) WHERE job_description_id IS NOT NULL;
CREATE UNIQUE INDEX alias_review_material_source ON ability_alias_review_sources(review_request_id,material_id,md5(evidence)) WHERE material_id IS NOT NULL;

-- JSON aliases remain the read-facing directory. This table tracks reviewed
-- additions separately so they can be audited and revoked without losing history.
CREATE TABLE reviewed_ability_aliases (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    ability_id UUID NOT NULL REFERENCES abilities(id) ON DELETE CASCADE,
    alias VARCHAR(150) NOT NULL,
    normalized_name VARCHAR(180) NOT NULL,
    review_request_id UUID NOT NULL REFERENCES ability_review_requests(id),
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    revoked_at TIMESTAMPTZ,
    revocation_reason TEXT
);
CREATE UNIQUE INDEX reviewed_ability_aliases_active_name ON reviewed_ability_aliases(normalized_name) WHERE is_active;

-- +goose Down
DROP TABLE reviewed_ability_aliases;
DROP TABLE ability_alias_review_sources;
ALTER TABLE user_profile_evidence DROP COLUMN raw_label, DROP COLUMN normalization_reason;
ALTER TABLE job_description_ability_requirement_options DROP COLUMN normalization_reason;
DELETE FROM platform_model_usage WHERE request_id IN (SELECT id FROM ability_review_requests WHERE review_type='alias');
DELETE FROM ability_review_requests WHERE review_type='alias';
ALTER TABLE ability_review_requests DROP CONSTRAINT ability_review_requests_decision_check;
ALTER TABLE ability_review_requests ADD CONSTRAINT ability_review_requests_decision_check CHECK (decision IN ('reuse_existing','approve_new','reject'));
ALTER TABLE ability_review_requests DROP CONSTRAINT ability_review_target, DROP COLUMN target_ability_id, DROP COLUMN review_type;
DROP FUNCTION normalize_ability_name(TEXT);
