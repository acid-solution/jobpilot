-- +goose Up
UPDATE job_description_ability_requirement_options option
SET review_request_id = request.id,
    resolution_status = 'pending_review'
FROM ability_review_requests request
WHERE option.ability_id IS NULL
  AND option.review_request_id IS NULL
  AND request.candidate_key = 'unknown:' || lower(regexp_replace(option.raw_label, '[[:space:]_.:/\\-]+', '', 'g'))
  AND request.status IN ('queued', 'running', 'failed');

-- +goose Down
UPDATE job_description_ability_requirement_options
SET review_request_id = NULL
WHERE ability_id IS NULL;
