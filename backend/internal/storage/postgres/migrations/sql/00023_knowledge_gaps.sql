-- +goose Up
ALTER TABLE user_capability_profiles
  ADD COLUMN manual_level SMALLINT CHECK (manual_level BETWEEN 0 AND 5),
  ADD COLUMN manual_updated_at TIMESTAMPTZ,
  ADD COLUMN level_source VARCHAR(24) NOT NULL DEFAULT 'evidence';

CREATE TABLE user_capability_level_events (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL,
  ability_id UUID NOT NULL REFERENCES abilities(id),
  previous_level SMALLINT,
  new_level SMALLINT NOT NULL CHECK (new_level BETWEEN 0 AND 5),
  source VARCHAR(24) NOT NULL CHECK (source IN ('manual','validation','initial')),
  session_id UUID,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX user_capability_level_events_lookup ON user_capability_level_events(user_id,ability_id,created_at DESC);

CREATE TABLE jd_ability_option_levels (
  option_id UUID PRIMARY KEY REFERENCES job_description_ability_requirement_options(id) ON DELETE CASCADE,
  job_description_id UUID NOT NULL REFERENCES job_descriptions(id) ON DELETE CASCADE,
  ability_id UUID NOT NULL REFERENCES abilities(id),
  level SMALLINT NOT NULL CHECK (level BETWEEN 1 AND 5),
  source VARCHAR(16) NOT NULL CHECK (source IN ('explicit','inferred')),
  requirement_kind VARCHAR(16) NOT NULL CHECK (requirement_kind IN ('required','preferred','unspecified')),
  evidence_quote TEXT NOT NULL,
  reason TEXT NOT NULL,
  confidence DOUBLE PRECISION NOT NULL CHECK (confidence BETWEEN 0 AND 1),
  prompt_version TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX jd_ability_option_levels_jd ON jd_ability_option_levels(job_description_id);

CREATE TABLE knowledge_gap_reports (
  user_id UUID NOT NULL,
  target_id UUID NOT NULL REFERENCES job_targets(id) ON DELETE CASCADE,
  source_hash TEXT NOT NULL,
  report JSONB NOT NULL,
  generated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  PRIMARY KEY(user_id,target_id)
);

ALTER TABLE profile_practice_sessions
  DROP CONSTRAINT profile_practice_sessions_mode_check,
  ADD CONSTRAINT profile_practice_sessions_mode_check CHECK (mode IN ('validation','review','initial')),
  DROP CONSTRAINT profile_practice_sessions_status_check,
  ADD CONSTRAINT profile_practice_sessions_status_check CHECK (status IN ('ready','clarifying','evaluated')),
  ADD COLUMN core_questions JSONB,
  ADD COLUMN clarification_count SMALLINT NOT NULL DEFAULT 0 CHECK (clarification_count BETWEEN 0 AND 3);

-- Existing one-question-per-ability grading cannot safely be copied into an
-- option when more than one option refers to the same ability in the JD.
INSERT INTO jd_ability_option_levels(option_id,job_description_id,ability_id,level,source,requirement_kind,evidence_quote,reason,confidence,prompt_version)
SELECT option.id,requirement.job_description_id,option.ability_id,a.level,a.source,a.requirement_kind,a.evidence_quote,a.reason,a.confidence,a.prompt_version
FROM job_description_ability_requirement_options option
JOIN job_description_ability_requirements requirement ON requirement.id=option.requirement_id
JOIN jd_ability_level_assessments a ON a.job_description_id=requirement.job_description_id AND a.ability_id=option.ability_id AND a.requirement_kind=requirement.requirement_kind
WHERE option.ability_id IS NOT NULL AND (
  SELECT COUNT(*) FROM job_description_ability_requirement_options other_option
  JOIN job_description_ability_requirements other_requirement ON other_requirement.id=other_option.requirement_id
  WHERE other_requirement.job_description_id=requirement.job_description_id AND other_option.ability_id=option.ability_id AND other_requirement.requirement_kind=requirement.requirement_kind
)=1 AND (POSITION(a.evidence_quote IN requirement.evidence)>0 OR POSITION(requirement.evidence IN a.evidence_quote)>0);

UPDATE jd_ability_level_jobs job SET status='queued',attempts=0,next_attempt_at=NOW(),last_error=NULL,
  lease_token=NULL,heartbeat_at=NULL,lease_expires_at=NULL,completed_at=NULL,updated_at=NOW()
WHERE EXISTS (
  SELECT 1 FROM job_description_ability_requirement_options option
  JOIN job_description_ability_requirements requirement ON requirement.id=option.requirement_id
  WHERE requirement.job_description_id=job.job_description_id AND option.ability_id IS NOT NULL
  AND NOT EXISTS(SELECT 1 FROM jd_ability_option_levels grade WHERE grade.option_id=option.id)
);

-- +goose Down
ALTER TABLE profile_practice_sessions DROP COLUMN clarification_count, DROP COLUMN core_questions;
ALTER TABLE profile_practice_sessions DROP CONSTRAINT profile_practice_sessions_mode_check,
  ADD CONSTRAINT profile_practice_sessions_mode_check CHECK (mode IN ('validation','review')),
  DROP CONSTRAINT profile_practice_sessions_status_check,
  ADD CONSTRAINT profile_practice_sessions_status_check CHECK (status IN ('ready','evaluated'));
DROP TABLE knowledge_gap_reports;
DROP TABLE jd_ability_option_levels;
DROP TABLE user_capability_level_events;
ALTER TABLE user_capability_profiles DROP COLUMN level_source,DROP COLUMN manual_updated_at,DROP COLUMN manual_level;
