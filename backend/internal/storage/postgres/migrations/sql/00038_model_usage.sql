-- +goose Up
CREATE TABLE model_usage_operations (
 id UUID PRIMARY KEY,user_id UUID NOT NULL,source_operation_id UUID NOT NULL,purpose TEXT NOT NULL,
 started_at TIMESTAMPTZ NOT NULL DEFAULT now(),UNIQUE(user_id,source_operation_id,purpose)
);
CREATE TABLE model_usage_calls (
 id UUID PRIMARY KEY,provider TEXT NOT NULL,requested_model TEXT NOT NULL DEFAULT '',returned_model TEXT NOT NULL DEFAULT '',
 provider_request_id TEXT NOT NULL DEFAULT '',payer TEXT NOT NULL CHECK(payer IN('user','platform')),
 purpose TEXT NOT NULL,source_operation_id UUID NOT NULL,started_at TIMESTAMPTZ NOT NULL,finished_at TIMESTAMPTZ,duration_ms BIGINT NOT NULL DEFAULT 0,
 result TEXT NOT NULL,usage_status TEXT NOT NULL,input_tokens BIGINT,output_tokens BIGINT,total_tokens BIGINT,
 cache_hit_tokens BIGINT,cache_miss_tokens BIGINT,currency TEXT NOT NULL DEFAULT '',estimated_cost NUMERIC(30,12),price_snapshot JSONB,
 legacy BOOLEAN NOT NULL DEFAULT false,confirmed BOOLEAN NOT NULL DEFAULT true,
 legacy_source TEXT UNIQUE,legacy_usage_id UUID
);
CREATE TABLE model_usage_links (
 call_id UUID NOT NULL REFERENCES model_usage_calls(id) ON DELETE CASCADE,
 operation_id UUID NOT NULL REFERENCES model_usage_operations(id) ON DELETE CASCADE,
 PRIMARY KEY(call_id,operation_id)
);
CREATE INDEX model_usage_operations_account ON model_usage_operations(user_id,started_at DESC,id);
CREATE INDEX model_usage_calls_time ON model_usage_calls(started_at DESC,id);
CREATE INDEX model_usage_links_operation ON model_usage_links(operation_id,call_id);
CREATE INDEX model_usage_review_lookup ON model_usage_calls(purpose,source_operation_id);
-- +goose StatementBegin
CREATE FUNCTION attach_model_review_usage() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE owner_id UUID; purpose_name TEXT;
BEGIN
 IF NEW.review_request_id IS NULL THEN RETURN NEW; END IF;
 IF TG_TABLE_NAME='ability_alias_review_sources' THEN
  SELECT COALESCE(jd.user_id,m.user_id) INTO owner_id FROM (SELECT 1) x LEFT JOIN job_descriptions jd ON jd.id=NEW.job_description_id LEFT JOIN user_profile_materials m ON m.id=NEW.material_id;
 ELSE
  SELECT jd.user_id INTO owner_id FROM job_description_ability_requirements req JOIN job_descriptions jd ON jd.id=req.job_description_id WHERE req.id=NEW.requirement_id;
 END IF;
 SELECT CASE WHEN review_type='alias' THEN 'ability_alias_review' ELSE 'ability_review' END INTO purpose_name FROM ability_review_requests WHERE id=NEW.review_request_id;
 INSERT INTO model_usage_operations(id,user_id,source_operation_id,purpose) VALUES(gen_random_uuid(),owner_id,NEW.review_request_id,purpose_name) ON CONFLICT DO NOTHING;
 INSERT INTO model_usage_links(call_id,operation_id) SELECT c.id,o.id FROM model_usage_calls c JOIN model_usage_operations o ON o.user_id=owner_id AND o.source_operation_id=c.source_operation_id AND o.purpose=c.purpose WHERE c.source_operation_id=NEW.review_request_id AND c.purpose=purpose_name ON CONFLICT DO NOTHING;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER usage_review_option AFTER INSERT OR UPDATE OF review_request_id ON job_description_ability_requirement_options FOR EACH ROW EXECUTE FUNCTION attach_model_review_usage();
CREATE TRIGGER usage_review_alias AFTER INSERT ON ability_alias_review_sources FOR EACH ROW EXECUTE FUNCTION attach_model_review_usage();
-- Historical counts are preserved only where an actual provider response can
-- be established. Old failures have unknown dispatch and no fabricated costs.
INSERT INTO model_usage_calls(id,provider,requested_model,payer,purpose,source_operation_id,started_at,result,usage_status,input_tokens,output_tokens,provider_request_id,legacy,confirmed,legacy_source)
SELECT id,provider,model,'platform',purpose,request_id,created_at,status,
 CASE WHEN input_tokens IS NOT NULL AND output_tokens IS NOT NULL AND (COALESCE(input_tokens,0)+COALESCE(output_tokens,0)>0) THEN 'partial' ELSE 'unknown' END,
 CASE WHEN COALESCE(input_tokens,0)+COALESCE(output_tokens,0)>0 THEN input_tokens END,CASE WHEN COALESCE(input_tokens,0)+COALESCE(output_tokens,0)>0 THEN output_tokens END,COALESCE(provider_request_id,''),true,status='succeeded' AND (COALESCE(provider_request_id,'')<>'' OR COALESCE(input_tokens,0)+COALESCE(output_tokens,0)>0),'review:'||id
FROM platform_model_usage u WHERE NOT EXISTS(SELECT 1 FROM model_usage_calls c WHERE c.legacy_usage_id=u.id) ON CONFLICT DO NOTHING;
INSERT INTO model_usage_operations(id,user_id,source_operation_id,purpose,started_at)
SELECT gen_random_uuid(),user_id,request_id,purpose,MIN(created_at) FROM platform_model_usage GROUP BY user_id,request_id,purpose ON CONFLICT DO NOTHING;
INSERT INTO model_usage_links(call_id,operation_id)
SELECT c.id,o.id FROM platform_model_usage u JOIN model_usage_calls c ON c.id=u.id OR c.legacy_usage_id=u.id JOIN model_usage_operations o ON o.user_id=u.user_id AND o.source_operation_id=u.request_id AND o.purpose=u.purpose ON CONFLICT DO NOTHING;
INSERT INTO model_usage_calls(id,provider,requested_model,payer,purpose,source_operation_id,started_at,result,usage_status,input_tokens,output_tokens,provider_request_id,legacy,confirmed,legacy_source)
SELECT gen_random_uuid(),COALESCE(provider,'deepseek'),COALESCE(model,''),'platform','jd_grading',id,COALESCE(completed_at,created_at),status,CASE WHEN status='succeeded' AND COALESCE(input_tokens,0)+COALESCE(output_tokens,0)>0 THEN 'partial' ELSE 'unknown' END,CASE WHEN status='succeeded' AND COALESCE(input_tokens,0)+COALESCE(output_tokens,0)>0 THEN input_tokens END,CASE WHEN status='succeeded' AND COALESCE(input_tokens,0)+COALESCE(output_tokens,0)>0 THEN output_tokens END,COALESCE(provider_request_id,''),true,status='succeeded' AND (COALESCE(provider_request_id,'')<>'' OR COALESCE(input_tokens,0)+COALESCE(output_tokens,0)>0),'grading:'||id
FROM jd_ability_level_jobs j WHERE status IN('succeeded','failed') AND NOT EXISTS(SELECT 1 FROM model_usage_calls c WHERE c.source_operation_id=j.id AND c.purpose='jd_grading') ON CONFLICT DO NOTHING;
INSERT INTO model_usage_operations(id,user_id,source_operation_id,purpose,started_at)
SELECT gen_random_uuid(),j.user_id,j.id,'jd_grading',COALESCE(j.completed_at,j.created_at) FROM jd_ability_level_jobs j JOIN model_usage_calls c ON c.legacy_source='grading:'||j.id ON CONFLICT DO NOTHING;
INSERT INTO model_usage_links(call_id,operation_id)
SELECT c.id,o.id FROM model_usage_calls c JOIN model_usage_operations o ON o.source_operation_id=c.source_operation_id AND o.purpose='jd_grading' WHERE c.legacy_source LIKE 'grading:%' ON CONFLICT DO NOTHING;
WITH sources AS (
 SELECT jd.user_id,opt.review_request_id FROM job_description_ability_requirement_options opt JOIN job_description_ability_requirements req ON req.id=opt.requirement_id JOIN job_descriptions jd ON jd.id=req.job_description_id WHERE opt.review_request_id IS NOT NULL
 UNION SELECT COALESCE(jd.user_id,m.user_id),src.review_request_id FROM ability_alias_review_sources src LEFT JOIN job_descriptions jd ON jd.id=src.job_description_id LEFT JOIN user_profile_materials m ON m.id=src.material_id
) INSERT INTO model_usage_operations(id,user_id,source_operation_id,purpose,started_at)
SELECT gen_random_uuid(),s.user_id,c.source_operation_id,c.purpose,MIN(c.started_at) FROM sources s JOIN model_usage_calls c ON c.source_operation_id=s.review_request_id AND c.purpose IN('ability_review','ability_alias_review') GROUP BY s.user_id,c.source_operation_id,c.purpose ON CONFLICT DO NOTHING;
INSERT INTO model_usage_links(call_id,operation_id) SELECT c.id,o.id FROM model_usage_calls c JOIN model_usage_operations o ON o.source_operation_id=c.source_operation_id AND o.purpose=c.purpose WHERE c.purpose IN('ability_review','ability_alias_review') ON CONFLICT DO NOTHING;
-- +goose Down
DROP TRIGGER usage_review_alias ON ability_alias_review_sources;
DROP TRIGGER usage_review_option ON job_description_ability_requirement_options;
DROP FUNCTION attach_model_review_usage();
DROP TABLE model_usage_links;
DROP TABLE model_usage_calls;
DROP TABLE model_usage_operations;
