-- +goose Up
ALTER TABLE jd_ability_option_levels
  ADD COLUMN level_standard_version INTEGER NOT NULL DEFAULT 1;

-- +goose Down
ALTER TABLE jd_ability_option_levels DROP COLUMN level_standard_version;
