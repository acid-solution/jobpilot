-- +goose Up
CREATE TABLE job_description_classifications (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    job_description_id UUID NOT NULL REFERENCES job_descriptions(id) ON DELETE CASCADE,
    category_id UUID NOT NULL REFERENCES job_categories(id),
    specialty_id UUID REFERENCES job_specialties(id),
    relation VARCHAR(20) NOT NULL CHECK (relation IN ('primary', 'secondary')),
    evidence TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (job_description_id, category_id, specialty_id)
);

CREATE INDEX job_description_classifications_jd
    ON job_description_classifications (job_description_id, relation);

CREATE TABLE job_description_abilities (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    job_description_id UUID NOT NULL REFERENCES job_descriptions(id) ON DELETE CASCADE,
    ability_id UUID NOT NULL REFERENCES abilities(id),
    raw_name VARCHAR(100) NOT NULL,
    evidence TEXT NOT NULL,
    required_level SMALLINT CHECK (required_level BETWEEN 0 AND 5),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (job_description_id, ability_id, evidence)
);

CREATE INDEX job_description_abilities_jd
    ON job_description_abilities (job_description_id, ability_id);

-- +goose Down
DROP TABLE IF EXISTS job_description_abilities;
DROP TABLE IF EXISTS job_description_classifications;
