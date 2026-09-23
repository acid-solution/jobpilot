-- +goose Up
UPDATE job_descriptions jd
SET ability_mentions = COALESCE(source.value, '[]'::jsonb),
    updated_at = NOW()
FROM (
    SELECT target.id AS job_description_id,
           jsonb_agg(
               jsonb_build_object(
                   'name', ability.name,
                   'catalog_code', ability.code,
                   'qualifier', option.qualifier,
                   'evidence', option.evidence,
                   'required_level', option.required_level
               ) ORDER BY requirement.sort_order, option.sort_order
           ) FILTER (WHERE ability.id IS NOT NULL) AS value
    FROM job_descriptions target
    LEFT JOIN job_description_ability_requirements requirement
      ON requirement.job_description_id = target.id
    LEFT JOIN job_description_ability_requirement_options option
      ON option.requirement_id = requirement.id
    LEFT JOIN abilities ability ON ability.id = option.ability_id
    GROUP BY target.id
) source
WHERE jd.id = source.job_description_id;

-- +goose Down
-- Public mentions cannot safely be reconstructed with unresolved candidate names.
SELECT 1;
