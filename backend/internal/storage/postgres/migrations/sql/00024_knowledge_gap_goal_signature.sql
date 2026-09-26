-- +goose Up
ALTER TABLE knowledge_gap_reports ADD COLUMN goal_signature TEXT;
UPDATE knowledge_gap_reports SET goal_signature=target_id::text;
ALTER TABLE knowledge_gap_reports ALTER COLUMN goal_signature SET NOT NULL;
ALTER TABLE knowledge_gap_reports DROP CONSTRAINT knowledge_gap_reports_pkey;
ALTER TABLE knowledge_gap_reports ADD PRIMARY KEY(user_id,goal_signature);

-- +goose Down
DELETE FROM knowledge_gap_reports WHERE goal_signature<>target_id::text;
ALTER TABLE knowledge_gap_reports DROP CONSTRAINT knowledge_gap_reports_pkey;
ALTER TABLE knowledge_gap_reports ADD PRIMARY KEY(user_id,target_id);
ALTER TABLE knowledge_gap_reports DROP COLUMN goal_signature;
