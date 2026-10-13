-- +goose Up
ALTER TABLE project_recommendation_reports ADD COLUMN selected_context_hash TEXT NOT NULL DEFAULT '';
-- +goose Down
ALTER TABLE project_recommendation_reports DROP COLUMN selected_context_hash;
