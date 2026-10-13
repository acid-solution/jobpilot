-- +goose Up
ALTER TABLE learning_route_jobs ADD COLUMN model_plans JSONB;
ALTER TABLE learning_route_jobs ADD COLUMN last_error_detail TEXT NOT NULL DEFAULT '';
-- +goose Down
ALTER TABLE learning_route_jobs DROP COLUMN last_error_detail;
ALTER TABLE learning_route_jobs DROP COLUMN model_plans;
