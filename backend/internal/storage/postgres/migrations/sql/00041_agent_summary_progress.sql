-- +goose Up
ALTER TABLE agent_context_summaries ADD COLUMN partial_sequence BIGINT NOT NULL DEFAULT 0;
ALTER TABLE agent_context_summaries ADD COLUMN partial_offset INTEGER NOT NULL DEFAULT 0;
ALTER TABLE agent_turns ADD COLUMN observations JSONB NOT NULL DEFAULT '[]';
-- +goose Down
ALTER TABLE agent_turns DROP COLUMN observations;
ALTER TABLE agent_context_summaries DROP COLUMN partial_sequence,DROP COLUMN partial_offset;
