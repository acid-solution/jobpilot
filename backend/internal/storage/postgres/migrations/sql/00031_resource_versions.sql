-- +goose Up
-- Revisions live beyond resource deletion so delete/recreate and A -> B -> A
-- cannot make an old confirmation valid again. Triggers share the business
-- transaction: rollback also rolls back revision changes. Account advisory
-- locks are acquired by HTTP/Agent/background writers, not by these triggers.
CREATE TABLE resource_versions (
    user_id UUID NOT NULL,
    resource_key TEXT NOT NULL,
    version BIGINT NOT NULL CHECK (version > 0),
    PRIMARY KEY(user_id, resource_key)
);
ALTER TABLE agent_actions ADD COLUMN expected_versions JSONB NOT NULL DEFAULT '{}'::jsonb
    CHECK (jsonb_typeof(expected_versions)='object');

-- Old proposals never captured a revision. Require a new proposal rather than
-- pretending that today's revision was the one the user originally reviewed.
DELETE FROM agent_checkpoints WHERE key IN (
    SELECT 'agent-' || conversation_id::text FROM agent_actions WHERE status='pending'
);
UPDATE agent_conversations SET status='idle',run_token=NULL,run_expires_at=NULL,updated_at=NOW()
    WHERE status='awaiting_confirmation' AND id IN (
        SELECT conversation_id FROM agent_actions WHERE status='pending'
    );
UPDATE agent_actions SET status='stale',error_code='resource_version_required',updated_at=NOW()
    WHERE status='pending';

-- +goose StatementBegin
CREATE FUNCTION bump_resource_version(owner UUID, resource TEXT) RETURNS void LANGUAGE sql AS $$
    INSERT INTO resource_versions(user_id,resource_key,version) VALUES(owner,resource,1)
    ON CONFLICT(user_id,resource_key) DO UPDATE SET version=resource_versions.version+1;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION track_resource_version() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    row_data JSONB;
    owner UUID;
    object_id UUID;
    parent_id UUID;
    resource TEXT;
    aggregate_key TEXT;
BEGIN
    -- updated_at-only refreshes are not business changes. Explicit corrections
    -- retain manual_updated_at and therefore do invalidate a pending action.
    IF TG_OP='UPDATE' AND (to_jsonb(NEW)-'updated_at')=(to_jsonb(OLD)-'updated_at') THEN
        RETURN NULL;
    END IF;
    IF TG_OP='DELETE' THEN row_data:=to_jsonb(OLD); ELSE row_data:=to_jsonb(NEW); END IF;
    CASE TG_TABLE_NAME
    WHEN 'job_targets' THEN
        owner:=(row_data->>'user_id')::uuid;
        resource:='target:' || (row_data->>'id');
        aggregate_key:='target_selection';
    WHEN 'target_directions' THEN
        object_id:=(row_data->>'target_id')::uuid;
        SELECT user_id INTO owner FROM job_targets WHERE id=object_id;
        resource:='target:' || object_id::text;
        aggregate_key:='target_selection';
    WHEN 'user_profile_materials' THEN
        owner:=(row_data->>'user_id')::uuid;
        resource:='material:' || (row_data->>'id');
        aggregate_key:='profile';
    WHEN 'user_profile_evidence' THEN
        object_id:=(row_data->>'material_id')::uuid;
        SELECT user_id INTO owner FROM user_profile_materials WHERE id=object_id;
        resource:='material:' || object_id::text;
        aggregate_key:='profile';
    WHEN 'user_capability_profiles' THEN
        owner:=(row_data->>'user_id')::uuid;
        resource:='capability:' || (row_data->>'ability_id');
        aggregate_key:='profile';
    WHEN 'user_profile_settings' THEN
        owner:=(row_data->>'user_id')::uuid;
        resource:='profile_settings';
        aggregate_key:='profile';
    WHEN 'knowledge_gap_reports' THEN
        owner:=(row_data->>'user_id')::uuid;
        resource:='knowledge_gaps';
    WHEN 'project_recommendation_reports' THEN
        owner:=(row_data->>'user_id')::uuid;
        resource:='recommendations';
    ELSE
        -- JD child rows are part of the JD's persisted business state, including
        -- classification/review results and independent background grading.
        IF TG_TABLE_NAME='job_descriptions' THEN
            owner:=(row_data->>'user_id')::uuid;
            object_id:=(row_data->>'id')::uuid;
        ELSIF TG_TABLE_NAME='job_description_ability_requirement_options' THEN
            parent_id:=(row_data->>'requirement_id')::uuid;
            SELECT job_description_id INTO object_id FROM job_description_ability_requirements WHERE id=parent_id;
        ELSE
            object_id:=(row_data->>'job_description_id')::uuid;
        END IF;
        IF owner IS NULL THEN SELECT user_id INTO owner FROM job_descriptions WHERE id=object_id; END IF;
        resource:='jd:' || object_id::text;
        aggregate_key:='market';
    END CASE;
    -- On cascading delete a parent can already be absent. Its own trigger has
    -- advanced the tombstone and aggregate; never create an ownerless record.
    IF owner IS NOT NULL AND resource IS NOT NULL THEN
        PERFORM bump_resource_version(owner,resource);
        IF aggregate_key IS NOT NULL THEN PERFORM bump_resource_version(owner,aggregate_key); END IF;
    END IF;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

-- Initialize existing object revisions without reprocessing private sources.
INSERT INTO resource_versions(user_id,resource_key,version)
SELECT user_id,'target:' || id::text,1 FROM job_targets
UNION SELECT user_id,'target_selection',1 FROM job_targets
UNION SELECT user_id,'jd:' || id::text,1 FROM job_descriptions
UNION SELECT user_id,'market',1 FROM job_descriptions
UNION SELECT user_id,'material:' || id::text,1 FROM user_profile_materials
UNION SELECT user_id,'profile',1 FROM user_profile_materials
UNION SELECT user_id,'capability:' || ability_id::text,1 FROM user_capability_profiles
UNION SELECT user_id,'profile',1 FROM user_capability_profiles
UNION SELECT user_id,'profile_settings',1 FROM user_profile_settings
UNION SELECT user_id,'profile',1 FROM user_profile_settings
UNION SELECT user_id,'knowledge_gaps',1 FROM knowledge_gap_reports
UNION SELECT user_id,'recommendations',1 FROM project_recommendation_reports;

-- +goose StatementBegin
DO $$
DECLARE table_name TEXT;
BEGIN
    FOREACH table_name IN ARRAY ARRAY[
        'job_targets','target_directions','job_descriptions',
        'job_description_classifications','job_description_abilities',
        'job_description_ability_requirements','job_description_ability_requirement_options',
        'jd_ability_level_assessments','jd_ability_option_levels',
        'user_profile_materials','user_profile_evidence','user_capability_profiles',
        'user_profile_settings','knowledge_gap_reports','project_recommendation_reports'
    ] LOOP
        EXECUTE format('CREATE TRIGGER resource_revision AFTER INSERT OR UPDATE OR DELETE ON %I FOR EACH ROW EXECUTE FUNCTION track_resource_version()',table_name);
    END LOOP;
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
DECLARE table_name TEXT;
BEGIN
    FOREACH table_name IN ARRAY ARRAY[
        'job_targets','target_directions','job_descriptions',
        'job_description_classifications','job_description_abilities',
        'job_description_ability_requirements','job_description_ability_requirement_options',
        'jd_ability_level_assessments','jd_ability_option_levels',
        'user_profile_materials','user_profile_evidence','user_capability_profiles',
        'user_profile_settings','knowledge_gap_reports','project_recommendation_reports'
    ] LOOP
        EXECUTE format('DROP TRIGGER resource_revision ON %I',table_name);
    END LOOP;
END;
$$;
-- +goose StatementEnd
DROP FUNCTION track_resource_version();
DROP FUNCTION bump_resource_version(UUID,TEXT);
ALTER TABLE agent_actions DROP COLUMN expected_versions;
DROP TABLE resource_versions;
