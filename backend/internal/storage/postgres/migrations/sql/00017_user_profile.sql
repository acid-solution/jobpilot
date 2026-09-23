-- +goose Up
CREATE TABLE user_profile_materials (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL,
    type VARCHAR(20) NOT NULL CHECK (type IN ('resume', 'experience')),
    title VARCHAR(120) NOT NULL,
    source_text TEXT NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'processing', 'ready', 'failed')),
    failure_reason TEXT NOT NULL DEFAULT '',
    confirmed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX user_profile_materials_user_created
    ON user_profile_materials (user_id, created_at DESC);

CREATE TABLE user_profile_evidence (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    material_id UUID NOT NULL REFERENCES user_profile_materials(id) ON DELETE CASCADE,
    ability_id UUID NOT NULL REFERENCES abilities(id),
    level SMALLINT NOT NULL CHECK (level BETWEEN 1 AND 5),
    evidence_quote TEXT NOT NULL,
    reason TEXT NOT NULL,
    confidence DOUBLE PRECISION NOT NULL CHECK (confidence BETWEEN 0 AND 1),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (material_id, ability_id)
);

CREATE INDEX user_profile_evidence_ability
    ON user_profile_evidence (ability_id, level DESC);

CREATE TABLE user_capability_profiles (
    user_id UUID NOT NULL,
    ability_id UUID NOT NULL REFERENCES abilities(id),
    evidence_level SMALLINT NOT NULL DEFAULT 0 CHECK (evidence_level BETWEEN 0 AND 5),
    verified_level SMALLINT NOT NULL DEFAULT 0 CHECK (verified_level BETWEEN 0 AND 5),
    current_level SMALLINT NOT NULL DEFAULT 0 CHECK (current_level BETWEEN 0 AND 5),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, ability_id)
);

CREATE TABLE profile_practice_sessions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL,
    ability_id UUID NOT NULL REFERENCES abilities(id),
    mode VARCHAR(20) NOT NULL CHECK (mode IN ('validation', 'review')),
    base_level SMALLINT NOT NULL CHECK (base_level BETWEEN 0 AND 5),
    target_level SMALLINT NOT NULL CHECK (target_level BETWEEN 0 AND 5),
    status VARCHAR(20) NOT NULL DEFAULT 'ready' CHECK (status IN ('ready', 'evaluated')),
    questions JSONB NOT NULL,
    answers JSONB NOT NULL DEFAULT '[]'::jsonb,
    evaluation JSONB,
    level_updated BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ
);

CREATE INDEX profile_practice_sessions_user_created
    ON profile_practice_sessions (user_id, created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS profile_practice_sessions;
DROP TABLE IF EXISTS user_capability_profiles;
DROP TABLE IF EXISTS user_profile_evidence;
DROP TABLE IF EXISTS user_profile_materials;
