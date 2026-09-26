package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/LeoninCS/jobpilot-next/backend/internal/knowledgegaps"
	"github.com/google/uuid"
)

type KnowledgeGapsRepository struct{ database *sql.DB }

func NewKnowledgeGapsRepository(database *sql.DB) *KnowledgeGapsRepository {
	return &KnowledgeGapsRepository{database}
}

func (r *KnowledgeGapsRepository) LoadRequirements(ctx context.Context, userID, targetID uuid.UUID) ([]knowledgegaps.Requirement, error) {
	rows, err := r.database.QueryContext(ctx, `SELECT requirement.id,jd.id,COALESCE(jd.title,''),requirement.operator,requirement.required_count,
		CASE WHEN grade.requirement_kind='preferred' THEN 'preferred' ELSE requirement.requirement_kind END,requirement.evidence,
		option.id,COALESCE(option.ability_id,'00000000-0000-0000-0000-000000000000'::uuid),COALESCE(ability.name,''),
		COALESCE(grade.level,0),COALESCE(grade.source,''),COALESCE(grade.evidence_quote,''),COALESCE(grade.reason,''),
		option.resolution_status
		FROM job_descriptions jd
		JOIN job_description_ability_requirements requirement ON requirement.job_description_id=jd.id
		JOIN job_description_ability_requirement_options option ON option.requirement_id=requirement.id
		LEFT JOIN abilities ability ON ability.id=option.ability_id
		LEFT JOIN jd_ability_option_levels grade ON grade.option_id=option.id
		WHERE jd.user_id=$1 AND jd.target_id=$2 AND jd.status='included' AND jd.validation_status='valid'
		ORDER BY jd.id,requirement.sort_order,option.sort_order`, userID, targetID)
	if err != nil {
		return nil, fmt.Errorf("load gap requirements: %w", err)
	}
	defer rows.Close()
	result := []knowledgegaps.Requirement{}
	positions := map[uuid.UUID]int{}
	for rows.Next() {
		var q knowledgegaps.Requirement
		var o knowledgegaps.Option
		var level int
		if err := rows.Scan(&q.ID, &q.JDID, &q.JDTitle, &q.Operator, &q.RequiredCount, &q.Kind, &q.Evidence, &o.ID, &o.AbilityID, &o.Name, &level, &o.Source, &o.Evidence, &o.Reason, &o.Resolution); err != nil {
			return nil, err
		}
		o.Level = level
		o.Graded = level > 0
		pos, ok := positions[q.ID]
		if !ok {
			pos = len(result)
			positions[q.ID] = pos
			q.Options = []knowledgegaps.Option{}
			result = append(result, q)
		}
		result[pos].Options = append(result[pos].Options, o)
	}
	return result, rows.Err()
}
func (r *KnowledgeGapsRepository) Get(ctx context.Context, userID uuid.UUID, goalSignature string) (*knowledgegaps.Stored, error) {
	var raw []byte
	var hash string
	err := r.database.QueryRowContext(ctx, `SELECT source_hash,report FROM knowledge_gap_reports WHERE user_id=$1 AND goal_signature=$2`, userID, goalSignature).Scan(&hash, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	value := &knowledgegaps.Stored{SourceHash: hash}
	if err := json.Unmarshal(raw, &value.Report); err != nil {
		return nil, err
	}
	return value, nil
}
func (r *KnowledgeGapsRepository) Save(ctx context.Context, userID, targetID uuid.UUID, goalSignature, hash string, report knowledgegaps.Report) error {
	raw, err := json.Marshal(report)
	if err != nil {
		return err
	}
	_, err = r.database.ExecContext(ctx, `INSERT INTO knowledge_gap_reports(user_id,target_id,goal_signature,source_hash,report)
		VALUES($1,$2,$3,$4,$5) ON CONFLICT(user_id,goal_signature) DO UPDATE SET target_id=EXCLUDED.target_id,source_hash=EXCLUDED.source_hash,report=EXCLUDED.report,generated_at=NOW()`, userID, targetID, goalSignature, hash, raw)
	return err
}
