-- +goose Up
ALTER TABLE project_recommendation_reports ADD COLUMN project_context_hash TEXT NOT NULL DEFAULT '';
CREATE TABLE learning_routes (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),user_id UUID NOT NULL,goal_signature TEXT NOT NULL,
 report_id UUID NOT NULL,project_id TEXT NOT NULL,current_version INT NOT NULL DEFAULT 0,
 UNIQUE(user_id,goal_signature,report_id,project_id)
);
CREATE TABLE learning_route_versions (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),route_id UUID NOT NULL REFERENCES learning_routes(id) ON DELETE CASCADE,
 version_no INT NOT NULL,revision BIGINT NOT NULL DEFAULT 1,source_hash TEXT NOT NULL,knowledge_hash TEXT NOT NULL,
 config JSONB NOT NULL,plans JSONB NOT NULL,generated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 UNIQUE(route_id,version_no)
);
CREATE TABLE learning_route_jobs (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),route_id UUID NOT NULL REFERENCES learning_routes(id) ON DELETE CASCADE,
 user_id UUID NOT NULL,target_id UUID NOT NULL,goal_signature TEXT NOT NULL,source_hash TEXT NOT NULL,
 base_version INT NOT NULL,input JSONB NOT NULL,
 status TEXT NOT NULL DEFAULT 'queued' CHECK(status IN('queued','running','succeeded','failed')),
 attempts INT NOT NULL DEFAULT 0,max_attempts INT NOT NULL DEFAULT 3,next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 lease_token UUID,heartbeat_at TIMESTAMPTZ,lease_expires_at TIMESTAMPTZ,last_error TEXT NOT NULL DEFAULT '',
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX learning_route_active_user ON learning_route_jobs(user_id) WHERE status IN('queued','running');
CREATE INDEX learning_route_claim ON learning_route_jobs(next_attempt_at,created_at) WHERE status='queued';
CREATE TABLE learning_route_import_requests (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),user_id UUID NOT NULL,route_id UUID NOT NULL REFERENCES learning_routes(id),
 version_id UUID NOT NULL REFERENCES learning_route_versions(id),revision BIGINT NOT NULL,
 idempotency_key TEXT NOT NULL UNIQUE,content_digest TEXT NOT NULL,payload JSONB NOT NULL,
 status TEXT NOT NULL DEFAULT 'accepted' CHECK(status IN('accepted','sending','unknown','failed','succeeded')),
 receipt JSONB,error_code TEXT NOT NULL DEFAULT '',dispatch_token UUID,dispatch_expires_at TIMESTAMPTZ,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 UNIQUE(version_id,revision)
);
-- +goose Down
DROP TABLE learning_route_import_requests;
DROP TABLE learning_route_jobs;
DROP TABLE learning_route_versions;
DROP TABLE learning_routes;
ALTER TABLE project_recommendation_reports DROP COLUMN project_context_hash;
