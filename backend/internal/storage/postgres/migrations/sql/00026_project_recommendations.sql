-- +goose Up
CREATE TABLE project_recommendation_reports (
    user_id UUID NOT NULL,
    goal_signature TEXT NOT NULL,
    report_id UUID NOT NULL,
    target_id UUID NOT NULL REFERENCES job_targets(id) ON DELETE CASCADE,
    source_hash TEXT NOT NULL,
    report JSONB NOT NULL,
    selected_project_id TEXT,
    generated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, goal_signature)
);

CREATE TABLE project_recommendation_jobs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL,
    goal_signature TEXT NOT NULL,
    target_id UUID NOT NULL REFERENCES job_targets(id) ON DELETE CASCADE,
    source_hash TEXT NOT NULL,
    input JSONB NOT NULL,
    adjustment TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','running','succeeded','failed')),
    phase TEXT NOT NULL DEFAULT 'draft' CHECK (phase IN ('draft','research','compare')),
    drafts JSONB,
    research JSONB,
    attempts INT NOT NULL DEFAULT 0,
    max_attempts INT NOT NULL DEFAULT 3,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    lease_token UUID,
    heartbeat_at TIMESTAMPTZ,
    lease_expires_at TIMESTAMPTZ,
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX project_recommendation_one_active_user
    ON project_recommendation_jobs(user_id) WHERE status IN ('queued','running');
CREATE INDEX project_recommendation_claim
    ON project_recommendation_jobs(next_attempt_at,created_at) WHERE status='queued';
CREATE INDEX project_recommendation_user_goal
    ON project_recommendation_jobs(user_id,goal_signature,created_at DESC);

-- +goose Down
DROP TABLE project_recommendation_jobs;
DROP TABLE project_recommendation_reports;
