-- +goose Up
CREATE TABLE user_profile_settings (
    user_id UUID PRIMARY KEY,
    weekly_hours SMALLINT CHECK (weekly_hours BETWEEN 1 AND 80),
    expected_weeks SMALLINT CHECK (expected_weeks BETWEEN 1 AND 52),
    existing_experience TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- +goose Down
DROP TABLE IF EXISTS user_profile_settings;
