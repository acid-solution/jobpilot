-- +goose Up
-- Keep the current preference and concurrency/forgetting guards, not old values.
-- Fingerprints only suppress repeated suggestions from the same source message.
ALTER TABLE agent_memory_proposals ADD COLUMN value_fingerprint TEXT NOT NULL DEFAULT '';
UPDATE agent_memory_proposals
SET value_fingerprint=encode(sha256(convert_to(value::text,'UTF8')),'hex');

-- Preserve confirmation receipts without copying the saved preference/source.
UPDATE agent_memory_proposals
SET result=CASE WHEN status='accepted' AND result IS NOT NULL AND result<>'null'::jsonb
 THEN jsonb_build_object('id',result->'id','revision',result->'revision',
  'value_fingerprint',encode(sha256(convert_to((result->'value')::text,'UTF8')),'hex'))
 ELSE NULL END;

-- A preview based on a superseded value is no longer useful or confirmable.
UPDATE agent_memory_proposals p SET status='stale'
WHERE status='pending' AND (
 expected_revision<>COALESCE((SELECT m.revision FROM agent_memories m WHERE m.user_id=p.user_id AND m.scope=p.scope AND m.memory_key=p.memory_key),0)
 OR expected_hash<>COALESCE((SELECT m.content_hash FROM agent_memories m WHERE m.user_id=p.user_id AND m.scope=p.scope AND m.memory_key=p.memory_key),'')
);
UPDATE agent_memory_proposals SET value='null',before_value=NULL,source_quote=''
WHERE status<>'pending';
ALTER TABLE agent_memory_proposals ADD CONSTRAINT agent_memory_proposal_no_history
 CHECK(status='pending' OR (value='null'::jsonb AND before_value IS NULL AND source_quote=''));

DROP TABLE agent_memory_revisions;

-- +goose Down
-- Historical values were intentionally deleted and cannot be reconstructed.
ALTER TABLE agent_memory_proposals DROP CONSTRAINT agent_memory_proposal_no_history;
ALTER TABLE agent_memory_proposals DROP COLUMN value_fingerprint;
CREATE TABLE agent_memory_revisions (
 memory_id UUID NOT NULL REFERENCES agent_memories(id) ON DELETE CASCADE,revision BIGINT NOT NULL,
 value JSONB NOT NULL,status TEXT NOT NULL,created_at TIMESTAMPTZ NOT NULL DEFAULT now(),PRIMARY KEY(memory_id,revision)
);
