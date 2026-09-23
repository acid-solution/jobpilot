-- +goose Up
-- Recalculate existing valid JDs with the current relationship rule:
-- matching employment type + any primary direction => included;
-- secondary-only direction match => reference; no match => excluded.
UPDATE job_descriptions AS jd
SET status = CASE
        WHEN jd.employment_type = 'unknown' OR jd.employment_type <> target.employment_type THEN 'excluded'
        WHEN EXISTS (
            SELECT 1
            FROM job_description_classifications classification
            JOIN target_directions direction
              ON direction.target_id = jd.target_id
             AND direction.category_id = classification.category_id
             AND (direction.specialty_id IS NULL OR direction.specialty_id = classification.specialty_id)
            WHERE classification.job_description_id = jd.id
              AND classification.relation = 'primary'
        ) THEN 'included'
        WHEN EXISTS (
            SELECT 1
            FROM job_description_classifications classification
            JOIN target_directions direction
              ON direction.target_id = jd.target_id
             AND direction.category_id = classification.category_id
             AND (direction.specialty_id IS NULL OR direction.specialty_id = classification.specialty_id)
            WHERE classification.job_description_id = jd.id
              AND classification.relation = 'secondary'
        ) THEN 'reference'
        ELSE 'excluded'
    END,
    relevance_reason = CASE
        WHEN jd.employment_type = 'unknown' OR jd.employment_type <> target.employment_type
            THEN '求职类型与当前目标不一致或不明确，未计入市场画像。'
        WHEN EXISTS (
            SELECT 1
            FROM job_description_classifications classification
            JOIN target_directions direction
              ON direction.target_id = jd.target_id
             AND direction.category_id = classification.category_id
             AND (direction.specialty_id IS NULL OR direction.specialty_id = classification.specialty_id)
            WHERE classification.job_description_id = jd.id
              AND classification.relation = 'primary'
        ) THEN '主导分类命中当前目标方向，且求职类型一致，已计入市场画像。'
        WHEN EXISTS (
            SELECT 1
            FROM job_description_classifications classification
            JOIN target_directions direction
              ON direction.target_id = jd.target_id
             AND direction.category_id = classification.category_id
             AND (direction.specialty_id IS NULL OR direction.specialty_id = classification.specialty_id)
            WHERE classification.job_description_id = jd.id
              AND classification.relation = 'secondary'
        ) THEN '只有次要分类命中当前目标方向，保留作参考。'
        ELSE '岗位分类未命中当前求职目标，未计入市场画像。'
    END,
    updated_at = NOW()
FROM job_targets AS target
WHERE target.id = jd.target_id
  AND target.catalog_status = 'valid'
  AND jd.validation_status = 'valid';

-- +goose Down
-- Previous relationship results cannot be reconstructed without retaining the
-- former rule version.
SELECT 1;
