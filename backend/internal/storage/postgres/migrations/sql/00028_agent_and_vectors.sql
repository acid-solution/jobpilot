-- +goose Up
CREATE EXTENSION IF NOT EXISTS pgcrypto;
ALTER TABLE abilities ALTER COLUMN embedding TYPE vector(1024) USING embedding::vector(1024);

CREATE TABLE agent_conversations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL,
    goal_signature TEXT NOT NULL,
    title TEXT NOT NULL DEFAULT '新对话',
    status TEXT NOT NULL DEFAULT 'idle' CHECK(status IN ('idle','running','awaiting_confirmation')),
    run_token UUID,
    run_expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX agent_conversations_scope ON agent_conversations(user_id, goal_signature, updated_at DESC);

CREATE TABLE agent_messages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    conversation_id UUID NOT NULL REFERENCES agent_conversations(id) ON DELETE CASCADE,
    role TEXT NOT NULL CHECK(role IN ('user','assistant','tool')),
    content TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX agent_messages_history ON agent_messages(conversation_id, created_at, id);

CREATE TABLE agent_actions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    conversation_id UUID NOT NULL REFERENCES agent_conversations(id) ON DELETE CASCADE,
    user_id UUID NOT NULL,
    goal_signature TEXT NOT NULL,
    kind TEXT NOT NULL,
    arguments JSONB NOT NULL,
    summary TEXT NOT NULL,
    expected_hash TEXT NOT NULL,
    interrupt_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','executing','succeeded','failed','cancelled','stale')),
    result JSONB,
    error_code TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX agent_one_pending_action ON agent_actions(conversation_id) WHERE status='pending';
CREATE INDEX agent_actions_scope ON agent_actions(user_id, goal_signature, created_at DESC);

CREATE TABLE agent_checkpoints (
    key TEXT PRIMARY KEY,
    value BYTEA NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE source_embeddings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL,
    goal_signature TEXT NOT NULL,
    source_type TEXT NOT NULL CHECK(source_type IN ('jd','resume','experience')),
    source_id UUID NOT NULL,
    source_hash TEXT NOT NULL,
    chunk_index INT NOT NULL,
    start_offset INT NOT NULL,
    end_offset INT NOT NULL,
    text TEXT NOT NULL,
    embedding vector(1024) NOT NULL,
    embedding_model TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(source_type,source_id,source_hash,chunk_index)
);
CREATE INDEX source_embeddings_scope ON source_embeddings(user_id,goal_signature,source_type);
CREATE INDEX source_embeddings_vector ON source_embeddings USING hnsw(embedding vector_cosine_ops);

CREATE TABLE embedding_jobs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID,
    source_type TEXT NOT NULL CHECK(source_type IN ('ability','jd','resume','experience')),
    source_id UUID NOT NULL,
    source_hash TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'queued' CHECK(status IN ('queued','running','succeeded','failed')),
    attempts INT NOT NULL DEFAULT 0,
    max_attempts INT NOT NULL DEFAULT 3,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    lease_token UUID,
    heartbeat_at TIMESTAMPTZ,
    lease_expires_at TIMESTAMPTZ,
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(source_type,source_id,source_hash)
);
CREATE INDEX embedding_jobs_claim ON embedding_jobs(next_attempt_at,created_at) WHERE status='queued';

CREATE TABLE vector_backfill_state (
    id INTEGER PRIMARY KEY CHECK(id=1),
    historical_jds_requeued BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
INSERT INTO vector_backfill_state(id) VALUES(1);

CREATE TABLE material_reassessment_jobs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL,
    material_id UUID NOT NULL REFERENCES user_profile_materials(id) ON DELETE CASCADE,
    source_hash TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'queued' CHECK(status IN ('queued','running','succeeded','failed')),
    attempts INT NOT NULL DEFAULT 0,
    max_attempts INT NOT NULL DEFAULT 3,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    lease_token UUID,
    heartbeat_at TIMESTAMPTZ,
    lease_expires_at TIMESTAMPTZ,
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(material_id,source_hash)
);
CREATE INDEX material_reassessment_claim ON material_reassessment_jobs(next_attempt_at,created_at) WHERE status='queued';

-- +goose StatementBegin
CREATE FUNCTION enqueue_jd_embedding() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE changed BOOLEAN;
BEGIN
    IF TG_OP = 'DELETE' THEN
        DELETE FROM source_embeddings WHERE source_type='jd' AND source_id=OLD.id;
        DELETE FROM embedding_jobs WHERE source_type='jd' AND source_id=OLD.id;
        RETURN OLD;
    END IF;
    changed := TG_OP = 'INSERT';
    IF TG_OP = 'UPDATE' THEN changed := NEW.raw_text IS DISTINCT FROM OLD.raw_text OR NEW.target_id IS DISTINCT FROM OLD.target_id; END IF;
    IF changed THEN
        DELETE FROM source_embeddings WHERE source_type='jd' AND source_id=NEW.id;
        UPDATE embedding_jobs SET status='failed',lease_token=NULL,heartbeat_at=NULL,lease_expires_at=NULL,last_error='source_changed',updated_at=NOW()
        WHERE source_type='jd' AND source_id=NEW.id AND status IN ('queued','running');
        INSERT INTO embedding_jobs(user_id,source_type,source_id,source_hash)
        VALUES(NEW.user_id,'jd',NEW.id,encode(digest(NEW.raw_text,'sha256'),'hex'))
        ON CONFLICT(source_type,source_id,source_hash) DO UPDATE SET
          status='queued',attempts=0,next_attempt_at=NOW(),lease_token=NULL,heartbeat_at=NULL,lease_expires_at=NULL,last_error='',updated_at=NOW();
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER jd_embedding_changes AFTER INSERT OR UPDATE OF raw_text,target_id OR DELETE ON job_descriptions
FOR EACH ROW EXECUTE FUNCTION enqueue_jd_embedding();

-- +goose StatementBegin
CREATE FUNCTION enqueue_material_embedding() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE changed BOOLEAN;
BEGIN
    IF TG_OP = 'DELETE' THEN
        DELETE FROM source_embeddings WHERE source_type IN ('resume','experience') AND source_id=OLD.id;
        DELETE FROM embedding_jobs WHERE source_type IN ('resume','experience') AND source_id=OLD.id;
        RETURN OLD;
    END IF;
    changed := TG_OP = 'INSERT';
    IF TG_OP = 'UPDATE' THEN changed := NEW.source_text IS DISTINCT FROM OLD.source_text OR OLD.status IS DISTINCT FROM NEW.status; END IF;
    IF NEW.status='ready' AND changed THEN
        DELETE FROM source_embeddings WHERE source_type IN ('resume','experience') AND source_id=NEW.id;
        UPDATE embedding_jobs SET status='failed',lease_token=NULL,heartbeat_at=NULL,lease_expires_at=NULL,last_error='source_changed',updated_at=NOW()
        WHERE source_type IN ('resume','experience') AND source_id=NEW.id AND status IN ('queued','running');
        INSERT INTO embedding_jobs(user_id,source_type,source_id,source_hash)
        VALUES(NEW.user_id,NEW.type,NEW.id,encode(digest(NEW.source_text,'sha256'),'hex'))
        ON CONFLICT(source_type,source_id,source_hash) DO UPDATE SET
          status='queued',attempts=0,next_attempt_at=NOW(),lease_token=NULL,heartbeat_at=NULL,lease_expires_at=NULL,last_error='',updated_at=NOW();
    END IF;
    IF NEW.status<>'ready' THEN
        DELETE FROM source_embeddings WHERE source_type IN ('resume','experience') AND source_id=NEW.id;
        UPDATE embedding_jobs SET status='failed',lease_token=NULL,heartbeat_at=NULL,lease_expires_at=NULL,last_error='source_not_ready',updated_at=NOW()
        WHERE source_type IN ('resume','experience') AND source_id=NEW.id AND status IN ('queued','running');
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER material_embedding_changes AFTER INSERT OR UPDATE OF source_text,status OR DELETE ON user_profile_materials
FOR EACH ROW EXECUTE FUNCTION enqueue_material_embedding();

-- +goose StatementBegin
CREATE FUNCTION enqueue_ability_embedding() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE changed BOOLEAN;
BEGIN
    changed := TG_OP = 'INSERT';
    IF TG_OP = 'UPDATE' THEN changed := NEW.name IS DISTINCT FROM OLD.name OR NEW.aliases IS DISTINCT FROM OLD.aliases OR NEW.definition IS DISTINCT FROM OLD.definition; END IF;
    IF NEW.is_active AND changed THEN
        UPDATE embedding_jobs SET status='failed',lease_token=NULL,heartbeat_at=NULL,lease_expires_at=NULL,last_error='source_changed',updated_at=NOW()
        WHERE source_type='ability' AND source_id=NEW.id AND source_hash<>encode(digest(NEW.name||NEW.aliases::text||COALESCE(NEW.definition,''),'sha256'),'hex') AND status IN ('queued','running');
        INSERT INTO embedding_jobs(source_type,source_id,source_hash)
        VALUES('ability',NEW.id,encode(digest(NEW.name||NEW.aliases::text||COALESCE(NEW.definition,''),'sha256'),'hex')) ON CONFLICT DO NOTHING;
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER ability_embedding_changes AFTER INSERT OR UPDATE OF name,aliases,definition ON abilities
FOR EACH ROW EXECUTE FUNCTION enqueue_ability_embedding();

-- Index old material and JD text without changing their existing analysis results.
INSERT INTO embedding_jobs(user_id,source_type,source_id,source_hash)
SELECT user_id,'jd',id,encode(digest(raw_text,'sha256'),'hex') FROM job_descriptions
ON CONFLICT DO NOTHING;
INSERT INTO embedding_jobs(user_id,source_type,source_id,source_hash)
SELECT user_id,type,id,encode(digest(source_text,'sha256'),'hex')
FROM user_profile_materials WHERE status='ready'
ON CONFLICT DO NOTHING;
INSERT INTO embedding_jobs(source_type,source_id,source_hash)
SELECT 'ability',id,encode(digest(name||aliases::text||COALESCE(definition,''),'sha256'),'hex')
FROM abilities WHERE is_active
ON CONFLICT DO NOTHING;

INSERT INTO material_reassessment_jobs(user_id,material_id,source_hash)
SELECT user_id,id,encode(digest(source_text,'sha256'),'hex') FROM user_profile_materials WHERE status='ready'
ON CONFLICT DO NOTHING;

-- +goose Down
DROP TRIGGER ability_embedding_changes ON abilities;
DROP FUNCTION enqueue_ability_embedding();
DROP TRIGGER material_embedding_changes ON user_profile_materials;
DROP FUNCTION enqueue_material_embedding();
DROP TRIGGER jd_embedding_changes ON job_descriptions;
DROP FUNCTION enqueue_jd_embedding();
DROP TABLE embedding_jobs;
DROP TABLE material_reassessment_jobs;
DROP TABLE vector_backfill_state;
DROP TABLE source_embeddings;
DROP TABLE agent_checkpoints;
DROP TABLE agent_actions;
DROP TABLE agent_messages;
DROP TABLE agent_conversations;
ALTER TABLE abilities ALTER COLUMN embedding TYPE vector USING embedding::vector;
