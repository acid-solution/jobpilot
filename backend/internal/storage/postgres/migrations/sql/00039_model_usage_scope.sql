-- +goose Up
ALTER TABLE model_usage_operations ADD COLUMN target_id UUID;
ALTER TABLE model_usage_operations ADD COLUMN resource_id UUID;
-- These are historical references, not foreign keys: deleting business
-- resources must not delete their supplier usage or invent a current target.

-- +goose Down
ALTER TABLE model_usage_operations DROP COLUMN resource_id;
ALTER TABLE model_usage_operations DROP COLUMN target_id;
