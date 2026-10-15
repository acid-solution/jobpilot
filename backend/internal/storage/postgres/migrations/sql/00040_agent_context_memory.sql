-- +goose Up
ALTER TABLE agent_conversations ADD COLUMN next_message_sequence BIGINT NOT NULL DEFAULT 0;
ALTER TABLE agent_messages ADD COLUMN sequence BIGINT;
ALTER TABLE agent_messages ADD COLUMN kind TEXT NOT NULL DEFAULT 'legacy';
WITH ordered AS (SELECT id,row_number() OVER(PARTITION BY conversation_id ORDER BY created_at,id) AS seq FROM agent_messages)
UPDATE agent_messages m SET sequence=o.seq FROM ordered o WHERE o.id=m.id;
UPDATE agent_conversations c SET next_message_sequence=COALESCE((SELECT max(sequence) FROM agent_messages WHERE conversation_id=c.id),0);
ALTER TABLE agent_messages ALTER COLUMN sequence SET NOT NULL;
CREATE UNIQUE INDEX agent_message_sequence ON agent_messages(conversation_id,sequence);
-- The trigger also covers existing recovery and action notification inserts.
-- +goose StatementBegin
CREATE FUNCTION allocate_agent_message_sequence() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 UPDATE agent_conversations SET next_message_sequence=next_message_sequence+1 WHERE id=NEW.conversation_id RETURNING next_message_sequence INTO NEW.sequence;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER agent_message_sequence BEFORE INSERT ON agent_messages FOR EACH ROW EXECUTE FUNCTION allocate_agent_message_sequence();
CREATE TABLE agent_context_scopes (
 user_id UUID NOT NULL,scope TEXT NOT NULL,revision BIGINT NOT NULL DEFAULT 0,epoch BIGINT NOT NULL DEFAULT 0,
 PRIMARY KEY(user_id,scope)
);
CREATE TABLE agent_turns (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),conversation_id UUID NOT NULL REFERENCES agent_conversations(id) ON DELETE CASCADE,
 user_id UUID NOT NULL,goal_key TEXT NOT NULL,run_token UUID NOT NULL,user_message_id UUID REFERENCES agent_messages(id) ON DELETE SET NULL,
 global_epoch BIGINT NOT NULL,goal_epoch BIGINT NOT NULL,status TEXT NOT NULL DEFAULT 'running',
 dependencies JSONB NOT NULL DEFAULT '{}',preferences_hash TEXT NOT NULL DEFAULT '',started_at TIMESTAMPTZ NOT NULL DEFAULT now(),finished_at TIMESTAMPTZ
);
CREATE INDEX agent_turns_conversation ON agent_turns(conversation_id,started_at);
ALTER TABLE agent_messages ADD COLUMN turn_id UUID REFERENCES agent_turns(id) ON DELETE SET NULL;
CREATE TABLE agent_context_summaries (
 conversation_id UUID PRIMARY KEY REFERENCES agent_conversations(id) ON DELETE CASCADE,
 revision BIGINT NOT NULL,covered_sequence BIGINT NOT NULL,global_epoch BIGINT NOT NULL,goal_epoch BIGINT NOT NULL,
 content JSONB NOT NULL,dependencies JSONB NOT NULL,preferences_hash TEXT NOT NULL,
 schema_version TEXT NOT NULL,prompt_version TEXT NOT NULL,model TEXT NOT NULL,updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE agent_memories (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),user_id UUID NOT NULL,scope TEXT NOT NULL,memory_key TEXT NOT NULL,
 value JSONB NOT NULL,revision BIGINT NOT NULL DEFAULT 1,content_hash TEXT NOT NULL,status TEXT NOT NULL DEFAULT 'active' CHECK(status IN('active','forgotten')),
 source_message_id UUID REFERENCES agent_messages(id) ON DELETE SET NULL,source_quote TEXT NOT NULL DEFAULT '',updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(user_id,scope,memory_key)
);
CREATE TABLE agent_memory_revisions (
 memory_id UUID NOT NULL REFERENCES agent_memories(id) ON DELETE CASCADE,revision BIGINT NOT NULL,
 value JSONB NOT NULL,status TEXT NOT NULL,created_at TIMESTAMPTZ NOT NULL DEFAULT now(),PRIMARY KEY(memory_id,revision)
);
CREATE TABLE agent_memory_proposals (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),user_id UUID NOT NULL,goal_key TEXT NOT NULL,scope TEXT NOT NULL,
 conversation_id UUID NOT NULL REFERENCES agent_conversations(id) ON DELETE CASCADE,
 source_message_id UUID NOT NULL REFERENCES agent_messages(id) ON DELETE CASCADE,source_quote TEXT NOT NULL,
 memory_key TEXT NOT NULL,value JSONB NOT NULL,before_value JSONB,expected_revision BIGINT NOT NULL,expected_hash TEXT NOT NULL,
 content_hash TEXT NOT NULL,global_epoch BIGINT NOT NULL,goal_epoch BIGINT NOT NULL,status TEXT NOT NULL DEFAULT 'pending',result JSONB,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX agent_memory_pending_key ON agent_memory_proposals(user_id,scope,memory_key) WHERE status='pending';
CREATE INDEX agent_memory_proposals_account ON agent_memory_proposals(user_id,goal_key,status);
CREATE TABLE agent_context_artifacts (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),user_id UUID NOT NULL,goal_key TEXT NOT NULL,
 conversation_id UUID NOT NULL REFERENCES agent_conversations(id) ON DELETE CASCADE,turn_id UUID NOT NULL REFERENCES agent_turns(id) ON DELETE CASCADE,
 global_epoch BIGINT NOT NULL,goal_epoch BIGINT NOT NULL,dependencies JSONB NOT NULL,content TEXT NOT NULL,content_hash TEXT NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX agent_context_artifacts_expiry ON agent_context_artifacts(expires_at);
ALTER TABLE learning_route_versions ADD COLUMN preference_basis JSONB;
ALTER TABLE learning_route_versions ADD COLUMN preference_adjustment TEXT NOT NULL DEFAULT '';
-- +goose Down
ALTER TABLE learning_route_versions DROP COLUMN preference_basis,DROP COLUMN preference_adjustment;
DROP TABLE agent_context_artifacts;
DROP TABLE agent_memory_proposals;
DROP TABLE agent_memory_revisions;
DROP TABLE agent_memories;
DROP TABLE agent_context_summaries;
ALTER TABLE agent_messages DROP COLUMN turn_id;
DROP TABLE agent_turns;
DROP TABLE agent_context_scopes;
DROP TRIGGER agent_message_sequence ON agent_messages;
DROP FUNCTION allocate_agent_message_sequence();
ALTER TABLE agent_messages DROP COLUMN sequence,DROP COLUMN kind;
ALTER TABLE agent_conversations DROP COLUMN next_message_sequence;
