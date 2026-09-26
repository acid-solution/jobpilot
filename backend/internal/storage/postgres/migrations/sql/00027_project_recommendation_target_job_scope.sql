-- +goose Up
DROP INDEX project_recommendation_one_active_user;
CREATE UNIQUE INDEX project_recommendation_one_active_target
    ON project_recommendation_jobs(user_id,goal_signature) WHERE status IN ('queued','running');

-- +goose Down
DROP INDEX project_recommendation_one_active_target;
CREATE UNIQUE INDEX project_recommendation_one_active_user
    ON project_recommendation_jobs(user_id) WHERE status IN ('queued','running');
