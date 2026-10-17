-- +goose Up
-- A failed extraction used to publish its default invalid placeholder. Restore
-- only from successful checkpoints of the exact same source; never requeue jobs.
WITH corrupted AS (
 SELECT jd.id,encode(digest(jd.raw_text,'sha256'),'hex') source_hash,previous.value
 FROM job_descriptions jd
 JOIN analysis_jobs job ON job.job_description_id=jd.id AND job.job_type='jd_analysis' AND job.status='failed'
 JOIN jd_analysis_stages failed ON failed.job_description_id=jd.id
  AND failed.source_hash=encode(digest(jd.raw_text,'sha256'),'hex') AND failed.stage='extraction' AND failed.status='failed'
 LEFT JOIN LATERAL (
  SELECT s.result->'result' value FROM jd_analysis_stages s
  WHERE s.job_description_id=jd.id AND s.source_hash=failed.source_hash AND s.status='succeeded'
   AND s.stage IN('ability_resolution','classification','extraction','extraction_window_0')
   AND s.result#>>'{result,ValidationStatus}'='valid'
  ORDER BY CASE s.stage WHEN 'ability_resolution' THEN 0 WHEN 'classification' THEN 1 WHEN 'extraction' THEN 2 ELSE 3 END,s.updated_at DESC
  LIMIT 1
 ) previous ON TRUE
 WHERE jd.validation_status='invalid' AND failed.result#>>'{result,ValidationStatus}'='invalid'
)
UPDATE job_descriptions jd SET
 title=COALESCE(NULLIF(c.value->>'Title',''),jd.title),
 company=COALESCE(NULLIF(c.value->>'Company',''),jd.company),
 employment_type=COALESCE(NULLIF(c.value->>'EmploymentType',''),jd.employment_type),
 responsibilities=CASE WHEN jsonb_typeof(c.value->'Responsibilities')='array' THEN c.value->'Responsibilities' ELSE jd.responsibilities END,
 conditions=CASE WHEN jsonb_typeof(c.value->'Conditions')='array' THEN COALESCE((SELECT string_agg(value,'；') FROM jsonb_array_elements_text(c.value->'Conditions')),'') ELSE jd.conditions END,
 document_type=CASE WHEN c.value IS NULL THEN NULL ELSE COALESCE(NULLIF(c.value->>'DocumentType',''),'job_description') END,
 validation_status=CASE WHEN c.value IS NULL THEN 'pending' ELSE 'valid' END,
 validation_reason='',analysis_prompt_version=COALESCE(NULLIF(c.value->>'PromptVersion',''),jd.analysis_prompt_version),updated_at=now()
FROM corrupted c WHERE jd.id=c.id AND encode(digest(jd.raw_text,'sha256'),'hex')=c.source_hash;

-- +goose Down
-- Do not reintroduce corrupted metadata or retry paid model calls on rollback.
SELECT 1;
