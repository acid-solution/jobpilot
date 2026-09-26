package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/LeoninCS/jobpilot-next/backend/internal/profile"
	"github.com/google/uuid"
)

type ProfileRepository struct{ database *sql.DB }

func NewProfileRepository(database *sql.DB) *ProfileRepository {
	return &ProfileRepository{database: database}
}

func (r *ProfileRepository) CreateMaterial(ctx context.Context, userID uuid.UUID, input profile.MaterialInput) (profile.Material, error) {
	id := uuid.New()
	_, err := r.database.ExecContext(ctx, `INSERT INTO user_profile_materials(id,user_id,type,title,source_text) VALUES($1,$2,$3,$4,$5)`, id, userID, input.Type, input.Title, input.Text)
	if err != nil {
		return profile.Material{}, fmt.Errorf("create profile material: %w", err)
	}
	return r.getMaterial(ctx, userID, id)
}

func (r *ProfileRepository) UpdateMaterial(ctx context.Context, userID, id uuid.UUID, input profile.MaterialInput) (profile.Material, error) {
	result, err := r.database.ExecContext(ctx, `UPDATE user_profile_materials SET type=$3,title=$4,source_text=$5,status='draft',failure_reason='',confirmed_at=NULL,updated_at=NOW() WHERE id=$1 AND user_id=$2 AND status IN ('draft','failed')`, id, userID, input.Type, input.Title, input.Text)
	if err != nil {
		return profile.Material{}, fmt.Errorf("update profile material: %w", err)
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return profile.Material{}, profile.ErrConflict
	}
	return r.getMaterial(ctx, userID, id)
}

func (r *ProfileRepository) DeleteMaterial(ctx context.Context, userID, id uuid.UUID) error {
	tx, err := r.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT e.ability_id FROM user_profile_evidence e JOIN user_profile_materials m ON m.id=e.material_id WHERE m.id=$1 AND m.user_id=$2`, id, userID)
	if err != nil {
		return err
	}
	var abilities []uuid.UUID
	for rows.Next() {
		var value uuid.UUID
		if err := rows.Scan(&value); err != nil {
			rows.Close()
			return err
		}
		abilities = append(abilities, value)
	}
	rows.Close()
	result, err := tx.ExecContext(ctx, `DELETE FROM user_profile_materials WHERE id=$1 AND user_id=$2 AND status<>'processing'`, id, userID)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return profile.ErrConflict
	}
	if err := refreshEvidenceLevels(ctx, tx, userID, abilities); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *ProfileRepository) ListMaterials(ctx context.Context, userID uuid.UUID) ([]profile.Material, error) {
	rows, err := r.database.QueryContext(ctx, `SELECT m.id,m.type,m.title,m.source_text,m.status,m.failure_reason,m.confirmed_at,m.created_at,m.updated_at,COUNT(e.id) FROM user_profile_materials m LEFT JOIN user_profile_evidence e ON e.material_id=m.id WHERE m.user_id=$1 GROUP BY m.id ORDER BY m.created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []profile.Material{}
	for rows.Next() {
		value, err := scanMaterial(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (r *ProfileRepository) getMaterial(ctx context.Context, userID, id uuid.UUID) (profile.Material, error) {
	row := r.database.QueryRowContext(ctx, `SELECT m.id,m.type,m.title,m.source_text,m.status,m.failure_reason,m.confirmed_at,m.created_at,m.updated_at,COUNT(e.id) FROM user_profile_materials m LEFT JOIN user_profile_evidence e ON e.material_id=m.id WHERE m.id=$1 AND m.user_id=$2 GROUP BY m.id`, id, userID)
	value, err := scanMaterial(row)
	if errors.Is(err, sql.ErrNoRows) {
		return profile.Material{}, profile.ErrNotFound
	}
	return value, err
}

type profileRowScanner interface{ Scan(...any) error }

func scanMaterial(row profileRowScanner) (profile.Material, error) {
	var value profile.Material
	var kind, status string
	err := row.Scan(&value.ID, &kind, &value.Title, &value.Text, &status, &value.FailureReason, &value.ConfirmedAt, &value.CreatedAt, &value.UpdatedAt, &value.EvidenceCount)
	value.Type, value.Status = profile.MaterialType(kind), profile.MaterialStatus(status)
	return value, err
}

func (r *ProfileRepository) BeginMaterialAnalysis(ctx context.Context, userID, id uuid.UUID) (profile.Material, error) {
	result, err := r.database.ExecContext(ctx, `UPDATE user_profile_materials SET status='processing',failure_reason='',confirmed_at=NOW(),updated_at=NOW() WHERE id=$1 AND user_id=$2 AND status IN ('draft','failed')`, id, userID)
	if err != nil {
		return profile.Material{}, err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return profile.Material{}, profile.ErrConflict
	}
	return r.getMaterial(ctx, userID, id)
}

func (r *ProfileRepository) CompleteMaterialAnalysis(ctx context.Context, userID, id uuid.UUID, drafts []profile.EvidenceDraft) (profile.Material, error) {
	tx, err := r.database.BeginTx(ctx, nil)
	if err != nil {
		return profile.Material{}, err
	}
	defer tx.Rollback()
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM user_profile_materials WHERE id=$1 AND user_id=$2 FOR UPDATE`, id, userID).Scan(&status); errors.Is(err, sql.ErrNoRows) {
		return profile.Material{}, profile.ErrNotFound
	} else if err != nil {
		return profile.Material{}, err
	}
	if status != "processing" {
		return profile.Material{}, profile.ErrConflict
	}
	rows, err := tx.QueryContext(ctx, `SELECT ability_id FROM user_profile_evidence WHERE material_id=$1`, id)
	if err != nil {
		return profile.Material{}, err
	}
	var affected []uuid.UUID
	for rows.Next() {
		var value uuid.UUID
		if err := rows.Scan(&value); err != nil {
			rows.Close()
			return profile.Material{}, err
		}
		affected = append(affected, value)
	}
	rows.Close()
	if _, err = tx.ExecContext(ctx, `DELETE FROM user_profile_evidence WHERE material_id=$1`, id); err != nil {
		return profile.Material{}, err
	}
	for _, draft := range drafts {
		if _, err = tx.ExecContext(ctx, `INSERT INTO user_profile_evidence(material_id,ability_id,level,evidence_quote,reason,confidence) VALUES($1,$2,$3,$4,$5,$6)`, id, draft.AbilityID, draft.Level, draft.Quote, draft.Reason, draft.Confidence); err != nil {
			return profile.Material{}, err
		}
		affected = append(affected, draft.AbilityID)
	}
	if err = refreshEvidenceLevels(ctx, tx, userID, affected); err != nil {
		return profile.Material{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE user_profile_materials SET status='ready',failure_reason='',updated_at=NOW() WHERE id=$1 AND user_id=$2 AND status='processing'`, id, userID); err != nil {
		return profile.Material{}, err
	}
	if err = tx.Commit(); err != nil {
		return profile.Material{}, err
	}
	return r.getMaterial(ctx, userID, id)
}

func (r *ProfileRepository) FailMaterialAnalysis(ctx context.Context, userID, id uuid.UUID, reason string) error {
	_, err := r.database.ExecContext(ctx, `UPDATE user_profile_materials SET status='failed',failure_reason=$3,updated_at=NOW() WHERE id=$1 AND user_id=$2 AND status='processing'`, id, userID, reason)
	return err
}

func (r *ProfileRepository) ListCapabilityInputs(ctx context.Context, userID uuid.UUID) ([]profile.CapabilityInput, error) {
	values, _, err := r.listCapabilities(ctx, userID)
	if err != nil {
		return nil, err
	}
	result := make([]profile.CapabilityInput, 0, len(values))
	for _, value := range values {
		if len(value.Levels) == 6 {
			result = append(result, profile.CapabilityInput{AbilityID: value.AbilityID, Name: value.Name, Description: value.Category, Levels: value.Levels})
		}
	}
	return result, nil
}

func (r *ProfileRepository) GetOverview(ctx context.Context, userID uuid.UUID) (profile.Overview, error) {
	capabilities, marketReady, err := r.listCapabilities(ctx, userID)
	if err != nil {
		return profile.Overview{}, err
	}
	var total, ready int
	if err := r.database.QueryRowContext(ctx, `SELECT COUNT(*),COUNT(*) FILTER(WHERE status='ready') FROM user_profile_materials WHERE user_id=$1`, userID).Scan(&total, &ready); err != nil {
		return profile.Overview{}, err
	}
	result := profile.Overview{MarketProfileReady: marketReady, MaterialCount: total, ReadyMaterialCount: ready, Capabilities: capabilities}
	for _, item := range capabilities {
		if item.Assessed {
			result.AssessedCount++
		} else {
			result.PendingCount++
		}
	}
	result.BlockingCount, err = r.blockingRequirementCount(ctx, userID, capabilities)
	if err != nil {
		return profile.Overview{}, err
	}
	settings, err := r.GetSettings(ctx, userID)
	if err != nil {
		return profile.Overview{}, err
	}
	result.Complete = marketReady && settings.WeeklyHours != nil && settings.ExpectedWeeks != nil && settings.ExistingExperience != "" && result.BlockingCount == 0
	return result, nil
}

func (r *ProfileRepository) blockingRequirementCount(ctx context.Context, userID uuid.UUID, capabilities []profile.Capability) (int, error) {
	rows, err := r.database.QueryContext(ctx, `SELECT requirement.id,requirement.required_count,option.ability_id,COALESCE(grade.level,0)
		FROM job_description_ability_requirements requirement
		JOIN job_descriptions jd ON jd.id=requirement.job_description_id
		JOIN job_targets target ON target.id=jd.target_id AND target.is_current AND target.user_id=$1
		JOIN job_description_ability_requirement_options option ON option.requirement_id=requirement.id AND option.ability_id IS NOT NULL
		LEFT JOIN jd_ability_option_levels grade ON grade.option_id=option.id
		WHERE jd.user_id=$1 AND jd.status='included' AND COALESCE(grade.requirement_kind,requirement.requirement_kind)<>'preferred'
		ORDER BY requirement.id`, userID)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	users := map[uuid.UUID]profile.Capability{}
	for _, a := range capabilities {
		users[a.AbilityID] = a
	}
	type state struct {
		needed    int
		satisfied map[uuid.UUID]bool
		unknown   bool
	}
	groups := map[uuid.UUID]*state{}
	for rows.Next() {
		var id, abilityID uuid.UUID
		var required, level int
		if err := rows.Scan(&id, &required, &abilityID, &level); err != nil {
			return 0, err
		}
		g := groups[id]
		if g == nil {
			if required < 1 {
				required = 1
			}
			g = &state{needed: required, satisfied: map[uuid.UUID]bool{}}
			groups[id] = g
		}
		a, ok := users[abilityID]
		if !ok || !a.Assessed {
			g.unknown = true
		} else if level > 0 && a.CurrentLevel >= level {
			g.satisfied[abilityID] = true
		}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	count := 0
	for _, g := range groups {
		if len(g.satisfied) < g.needed && g.unknown {
			count++
		}
	}
	return count, nil
}

func (r *ProfileRepository) SetCapabilityLevel(ctx context.Context, userID, abilityID uuid.UUID, level int) (profile.Capability, error) {
	tx, err := r.database.BeginTx(ctx, nil)
	if err != nil {
		return profile.Capability{}, err
	}
	defer tx.Rollback()
	var previous sql.NullInt16
	err = tx.QueryRowContext(ctx, `SELECT current_level FROM user_capability_profiles WHERE user_id=$1 AND ability_id=$2 FOR UPDATE`, userID, abilityID).Scan(&previous)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return profile.Capability{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO user_capability_profiles(user_id,ability_id,current_level,manual_level,manual_updated_at,level_source)
		VALUES($1,$2,$3,$3,NOW(),'manual') ON CONFLICT(user_id,ability_id) DO UPDATE SET
		current_level=$3,manual_level=$3,manual_updated_at=NOW(),level_source='manual',updated_at=NOW()`, userID, abilityID, level)
	if err != nil {
		return profile.Capability{}, err
	}
	var old any
	if previous.Valid {
		old = previous.Int16
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO user_capability_level_events(user_id,ability_id,previous_level,new_level,source) VALUES($1,$2,$3,$4,'manual')`, userID, abilityID, old, level)
	if err != nil {
		return profile.Capability{}, err
	}
	if err = tx.Commit(); err != nil {
		return profile.Capability{}, err
	}
	return r.GetCapability(ctx, userID, abilityID)
}

func (r *ProfileRepository) GetSettings(ctx context.Context, userID uuid.UUID) (profile.Settings, error) {
	var value profile.Settings
	err := r.database.QueryRowContext(ctx, `SELECT weekly_hours,expected_weeks,existing_experience,updated_at FROM user_profile_settings WHERE user_id=$1`, userID).Scan(&value.WeeklyHours, &value.ExpectedWeeks, &value.ExistingExperience, &value.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return profile.Settings{ExistingExperience: ""}, nil
	}
	return value, err
}

func (r *ProfileRepository) SaveSettings(ctx context.Context, userID uuid.UUID, input profile.Settings) (profile.Settings, error) {
	_, err := r.database.ExecContext(ctx, `INSERT INTO user_profile_settings(user_id,weekly_hours,expected_weeks,existing_experience) VALUES($1,$2,$3,$4) ON CONFLICT(user_id) DO UPDATE SET weekly_hours=EXCLUDED.weekly_hours,expected_weeks=EXCLUDED.expected_weeks,existing_experience=EXCLUDED.existing_experience,updated_at=NOW()`, userID, input.WeeklyHours, input.ExpectedWeeks, input.ExistingExperience)
	if err != nil {
		return profile.Settings{}, err
	}
	return r.GetSettings(ctx, userID)
}

func (r *ProfileRepository) GetCapability(ctx context.Context, userID, abilityID uuid.UUID) (profile.Capability, error) {
	values, _, err := r.listCapabilities(ctx, userID)
	if err != nil {
		return profile.Capability{}, err
	}
	for _, value := range values {
		if value.AbilityID == abilityID {
			return value, nil
		}
	}
	return profile.Capability{}, profile.ErrNotFound
}

func (r *ProfileRepository) listCapabilities(ctx context.Context, userID uuid.UUID) ([]profile.Capability, bool, error) {
	var included int
	err := r.database.QueryRowContext(ctx, `SELECT COUNT(*) FROM job_descriptions jd JOIN job_targets t ON t.id=jd.target_id WHERE jd.user_id=$1 AND t.user_id=$1 AND t.is_current AND jd.status='included'`, userID).Scan(&included)
	if err != nil {
		return nil, false, err
	}
	rows, err := r.database.QueryContext(ctx, `
		WITH current_target AS (SELECT id FROM job_targets WHERE user_id=$1 AND is_current),
		ability_jd AS (
			SELECT option.ability_id,jd.id AS jd_id,
			       MAX(assessment.level) FILTER(WHERE assessment.requirement_kind<>'preferred') AS level
			FROM job_description_ability_requirement_options option
			JOIN job_description_ability_requirements requirement ON requirement.id=option.requirement_id
			JOIN job_descriptions jd ON jd.id=requirement.job_description_id
			JOIN current_target target ON target.id=jd.target_id
			LEFT JOIN jd_ability_level_assessments assessment
			  ON assessment.job_description_id=jd.id AND assessment.ability_id=option.ability_id
			WHERE jd.user_id=$1 AND jd.status='included' AND option.ability_id IS NOT NULL
			GROUP BY option.ability_id,jd.id
		), level_counts AS (
			SELECT ability_id,level,COUNT(*) AS count,ROW_NUMBER() OVER(PARTITION BY ability_id ORDER BY COUNT(*) DESC,level DESC) AS rank
			FROM ability_jd WHERE level BETWEEN 1 AND 5 GROUP BY ability_id,level
		)
		SELECT ability.id,ability.name,category.name,COALESCE(MAX(level_counts.level) FILTER(WHERE level_counts.rank=1),0) AS market_level,
			COUNT(DISTINCT ability_jd.jd_id) AS covered_count,COALESCE(user_profile.current_level,0),COALESCE(user_profile.evidence_level,0),COALESCE(user_profile.verified_level,0),user_profile.updated_at,
			(user_profile.manual_level IS NOT NULL OR COALESCE(user_profile.current_level,0)>0 OR COALESCE(user_profile.evidence_level,0)>0 OR COALESCE(user_profile.verified_level,0)>0) AS assessed,
			CASE WHEN user_profile.manual_level IS NOT NULL THEN user_profile.level_source
			     WHEN user_profile.verified_level>0 AND user_profile.verified_level>=user_profile.current_level THEN 'validation'
			     ELSE COALESCE(user_profile.level_source,'unassessed') END,user_profile.manual_updated_at
		FROM ability_jd JOIN abilities ability ON ability.id=ability_jd.ability_id JOIN ability_categories category ON category.id=ability.category_id
		LEFT JOIN level_counts ON level_counts.ability_id=ability.id LEFT JOIN user_capability_profiles user_profile ON user_profile.user_id=$1 AND user_profile.ability_id=ability.id
		WHERE ability.is_active GROUP BY ability.id,ability.name,category.name,user_profile.current_level,user_profile.evidence_level,user_profile.verified_level,user_profile.updated_at,user_profile.manual_level,user_profile.level_source,user_profile.manual_updated_at
		ORDER BY covered_count DESC,ability.sort_order`, userID)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	values := []profile.Capability{}
	index := map[uuid.UUID]int{}
	var ids []uuid.UUID
	for rows.Next() {
		var value profile.Capability
		var covered int
		if err := rows.Scan(&value.AbilityID, &value.Name, &value.Category, &value.MarketLevel, &covered, &value.CurrentLevel, &value.EvidenceLevel, &value.VerifiedLevel, &value.UpdatedAt, &value.Assessed, &value.LevelSource, &value.ManualUpdatedAt); err != nil {
			return nil, false, err
		}
		value.MarketLevelReady = value.MarketLevel > 0
		value.Status = "needs_evidence"
		if value.Assessed {
			value.Status = "evidence_backed"
		}
		if value.VerifiedLevel > 0 && value.VerifiedLevel >= value.CurrentLevel {
			value.Status = "verified"
		}
		value.NeedsValidation = value.Assessed && value.MarketLevelReady && value.CurrentLevel < value.MarketLevel
		value.Levels = []profile.LevelDefinition{}
		value.Evidence = []profile.Evidence{}
		index[value.AbilityID] = len(values)
		ids = append(ids, value.AbilityID)
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	if len(ids) == 0 {
		return values, included >= 10, nil
	}
	levelRows, err := r.database.QueryContext(ctx, `SELECT ability_id,level,description FROM ability_levels WHERE ability_id=ANY($1) ORDER BY ability_id,level`, uuidArray(ids))
	if err != nil {
		return nil, false, err
	}
	for levelRows.Next() {
		var id uuid.UUID
		var item profile.LevelDefinition
		if err := levelRows.Scan(&id, &item.Level, &item.Description); err != nil {
			levelRows.Close()
			return nil, false, err
		}
		pos, ok := index[id]
		if ok {
			values[pos].Levels = append(values[pos].Levels, item)
		}
	}
	levelRows.Close()
	evidenceRows, err := r.database.QueryContext(ctx, `SELECT e.id,e.material_id,e.ability_id,a.name,e.level,e.evidence_quote,e.reason,e.confidence,e.created_at FROM user_profile_evidence e JOIN user_profile_materials m ON m.id=e.material_id AND m.status='ready' JOIN abilities a ON a.id=e.ability_id WHERE m.user_id=$1 AND e.ability_id=ANY($2) ORDER BY e.level DESC,e.created_at DESC`, userID, uuidArray(ids))
	if err != nil {
		return nil, false, err
	}
	for evidenceRows.Next() {
		var item profile.Evidence
		if err := evidenceRows.Scan(&item.ID, &item.MaterialID, &item.AbilityID, &item.AbilityName, &item.Level, &item.Quote, &item.Reason, &item.Confidence, &item.CreatedAt); err != nil {
			evidenceRows.Close()
			return nil, false, err
		}
		pos, ok := index[item.AbilityID]
		if ok {
			values[pos].Evidence = append(values[pos].Evidence, item)
		}
	}
	evidenceRows.Close()
	return values, included >= 10, nil
}

func (r *ProfileRepository) CreateSession(ctx context.Context, userID uuid.UUID, value profile.Session) (profile.Session, error) {
	id := uuid.New()
	questions, err := json.Marshal(value.Questions)
	if err != nil {
		return profile.Session{}, err
	}
	_, err = r.database.ExecContext(ctx, `INSERT INTO profile_practice_sessions(id,user_id,ability_id,mode,base_level,target_level,questions) VALUES($1,$2,$3,$4,$5,$6,$7)`, id, userID, value.AbilityID, value.Mode, value.BaseLevel, value.TargetLevel, questions)
	if err != nil {
		return profile.Session{}, err
	}
	return r.GetSession(ctx, userID, id)
}
func (r *ProfileRepository) GetSession(ctx context.Context, userID, id uuid.UUID) (profile.Session, error) {
	var value profile.Session
	var mode string
	var questions, answers, evaluation []byte
	err := r.database.QueryRowContext(ctx, `SELECT s.id,s.ability_id,a.name,s.mode,s.base_level,s.target_level,s.status,s.questions,s.answers,s.evaluation,s.level_updated,s.clarification_count,s.created_at,s.completed_at FROM profile_practice_sessions s JOIN abilities a ON a.id=s.ability_id WHERE s.id=$1 AND s.user_id=$2`, id, userID).Scan(&value.ID, &value.AbilityID, &value.AbilityName, &mode, &value.BaseLevel, &value.TargetLevel, &value.Status, &questions, &answers, &evaluation, &value.LevelUpdated, &value.ClarificationCount, &value.CreatedAt, &value.CompletedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return profile.Session{}, profile.ErrNotFound
	}
	if err != nil {
		return profile.Session{}, err
	}
	value.Mode = profile.PracticeMode(mode)
	if err = json.Unmarshal(questions, &value.Questions); err != nil {
		return profile.Session{}, err
	}
	if len(answers) > 0 {
		_ = json.Unmarshal(answers, &value.Answers)
	}
	if len(evaluation) > 0 && string(evaluation) != "null" {
		var result profile.Evaluation
		if err = json.Unmarshal(evaluation, &result); err != nil {
			return profile.Session{}, err
		}
		value.Evaluation = &result
	}
	return value, nil
}
func (r *ProfileRepository) ListSessions(ctx context.Context, userID uuid.UUID) ([]profile.Session, error) {
	rows, err := r.database.QueryContext(ctx, `SELECT id FROM profile_practice_sessions WHERE user_id=$1 ORDER BY created_at DESC LIMIT 50`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	result := []profile.Session{}
	for _, id := range ids {
		s, err := r.GetSession(ctx, userID, id)
		if err != nil {
			return nil, err
		}
		result = append(result, s)
	}
	return result, nil
}
func (r *ProfileRepository) SaveAnswer(ctx context.Context, userID, id uuid.UUID, answer profile.AnswerInput) (profile.Session, error) {
	tx, err := r.database.BeginTx(ctx, nil)
	if err != nil {
		return profile.Session{}, err
	}
	defer tx.Rollback()
	var raw []byte
	var status string
	err = tx.QueryRowContext(ctx, `SELECT answers,status FROM profile_practice_sessions WHERE id=$1 AND user_id=$2 FOR UPDATE`, id, userID).Scan(&raw, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return profile.Session{}, profile.ErrNotFound
	}
	if err != nil {
		return profile.Session{}, err
	}
	if status != "ready" && status != "clarifying" {
		return profile.Session{}, profile.ErrConflict
	}
	var answers []profile.AnswerInput
	if err = json.Unmarshal(raw, &answers); err != nil {
		return profile.Session{}, err
	}
	found := false
	for i := range answers {
		if answers[i].QuestionID == answer.QuestionID {
			answers[i] = answer
			found = true
			break
		}
	}
	if !found {
		answers = append(answers, answer)
	}
	encoded, _ := json.Marshal(answers)
	if _, err = tx.ExecContext(ctx, `UPDATE profile_practice_sessions SET answers=$3 WHERE id=$1 AND user_id=$2`, id, userID, encoded); err != nil {
		return profile.Session{}, err
	}
	if err = tx.Commit(); err != nil {
		return profile.Session{}, err
	}
	return r.GetSession(ctx, userID, id)
}
func (r *ProfileRepository) AddClarification(ctx context.Context, userID, id uuid.UUID, answers []profile.AnswerInput, followups []profile.Question) (profile.Session, error) {
	session, err := r.GetSession(ctx, userID, id)
	if err != nil {
		return profile.Session{}, err
	}
	questions := append(append([]profile.Question{}, session.Questions...), followups...)
	questionJSON, _ := json.Marshal(questions)
	answerJSON, _ := json.Marshal(answers)
	result, err := r.database.ExecContext(ctx, `UPDATE profile_practice_sessions SET status='clarifying',questions=$3,answers=$4,clarification_count=$5 WHERE id=$1 AND user_id=$2 AND status='ready'`, id, userID, questionJSON, answerJSON, len(followups))
	if err != nil {
		return profile.Session{}, err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return profile.Session{}, profile.ErrConflict
	}
	return r.GetSession(ctx, userID, id)
}
func (r *ProfileRepository) CompleteSession(ctx context.Context, userID, id uuid.UUID, answers []profile.AnswerInput, evaluation profile.Evaluation) (profile.Session, error) {
	answerJSON, _ := json.Marshal(answers)
	evaluationJSON, _ := json.Marshal(evaluation)
	result, err := r.database.ExecContext(ctx, `UPDATE profile_practice_sessions SET status='evaluated',answers=$3,evaluation=$4,completed_at=NOW() WHERE id=$1 AND user_id=$2 AND status IN ('ready','clarifying')`, id, userID, answerJSON, evaluationJSON)
	if err != nil {
		return profile.Session{}, err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return profile.Session{}, profile.ErrConflict
	}
	return r.GetSession(ctx, userID, id)
}
func (r *ProfileRepository) ConfirmSession(ctx context.Context, userID, id uuid.UUID) (profile.Session, error) {
	tx, err := r.database.BeginTx(ctx, nil)
	if err != nil {
		return profile.Session{}, err
	}
	defer tx.Rollback()
	var abilityID uuid.UUID
	var mode, status string
	var base, target int
	var levelUpdated bool
	var raw []byte
	var createdAt time.Time
	err = tx.QueryRowContext(ctx, `SELECT ability_id,mode,status,base_level,target_level,level_updated,evaluation,created_at FROM profile_practice_sessions WHERE id=$1 AND user_id=$2 FOR UPDATE`, id, userID).Scan(&abilityID, &mode, &status, &base, &target, &levelUpdated, &raw, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return profile.Session{}, profile.ErrNotFound
	}
	if err != nil {
		return profile.Session{}, err
	}
	if status != "evaluated" || levelUpdated || mode == "review" {
		return profile.Session{}, profile.ErrConflict
	}
	var evaluation profile.Evaluation
	if err = json.Unmarshal(raw, &evaluation); err != nil {
		return profile.Session{}, err
	}
	if mode == "validation" {
		if !evaluation.Passed || evaluation.Verdict != "pass" {
			return profile.Session{}, profile.ErrPrecondition
		}
		target = base + 1
	}
	if mode == "initial" {
		if evaluation.SuggestedLevel == nil {
			return profile.Session{}, profile.ErrPrecondition
		}
		target = *evaluation.SuggestedLevel
	}
	var current int
	var manual sql.NullInt16
	var profileUpdated time.Time
	err = tx.QueryRowContext(ctx, `SELECT current_level,manual_level,updated_at FROM user_capability_profiles WHERE user_id=$1 AND ability_id=$2 FOR UPDATE`, userID, abilityID).Scan(&current, &manual, &profileUpdated)
	if errors.Is(err, sql.ErrNoRows) {
		if mode != "initial" {
			return profile.Session{}, profile.ErrConflict
		}
		current = 0
	} else if err != nil {
		return profile.Session{}, err
	}
	if current != base || mode == "initial" && manual.Valid || !profileUpdated.IsZero() && profileUpdated.After(createdAt) {
		return profile.Session{}, profile.ErrConflict
	}
	if mode == "initial" {
		_, err = tx.ExecContext(ctx, `INSERT INTO user_capability_profiles(user_id,ability_id,current_level,manual_level,manual_updated_at,level_source) VALUES($1,$2,$3,$3,NOW(),'initial') ON CONFLICT(user_id,ability_id) DO UPDATE SET current_level=$3,manual_level=$3,manual_updated_at=NOW(),level_source='initial',updated_at=NOW()`, userID, abilityID, target)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE user_capability_profiles SET current_level=$3,verified_level=GREATEST(verified_level,$3),manual_level=$3,manual_updated_at=NOW(),level_source='validation',updated_at=NOW() WHERE user_id=$1 AND ability_id=$2`, userID, abilityID, target)
	}
	if err != nil {
		return profile.Session{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO user_capability_level_events(user_id,ability_id,previous_level,new_level,source,session_id) VALUES($1,$2,$3,$4,$5,$6)`, userID, abilityID, base, target, mode, id)
	if err != nil {
		return profile.Session{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE profile_practice_sessions SET level_updated=TRUE WHERE id=$1 AND user_id=$2 AND level_updated=FALSE`, id, userID)
	if err != nil {
		return profile.Session{}, err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return profile.Session{}, profile.ErrConflict
	}
	if err = tx.Commit(); err != nil {
		return profile.Session{}, err
	}
	return r.GetSession(ctx, userID, id)
}

func refreshEvidenceLevels(ctx context.Context, tx *sql.Tx, userID uuid.UUID, abilities []uuid.UUID) error {
	for _, abilityID := range uniqueUUIDs(abilities) {
		var level int
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(e.level),0) FROM user_profile_evidence e JOIN user_profile_materials m ON m.id=e.material_id WHERE m.user_id=$1 AND e.ability_id=$2 AND m.status IN ('processing','ready')`, userID, abilityID).Scan(&level); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO user_capability_profiles(user_id,ability_id,evidence_level,current_level) VALUES($1,$2,$3,$3) ON CONFLICT(user_id,ability_id) DO UPDATE SET evidence_level=EXCLUDED.evidence_level,current_level=CASE WHEN user_capability_profiles.manual_level IS NOT NULL THEN user_capability_profiles.current_level ELSE GREATEST(user_capability_profiles.current_level,EXCLUDED.evidence_level) END,updated_at=NOW()`, userID, abilityID, level); err != nil {
			return err
		}
	}
	return nil
}
func uniqueUUIDs(values []uuid.UUID) []uuid.UUID {
	seen := map[uuid.UUID]bool{}
	result := []uuid.UUID{}
	for _, value := range values {
		if value != uuid.Nil && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].String() < result[j].String() })
	return result
}
func uuidArray(values []uuid.UUID) []string {
	result := make([]string, len(values))
	for i, value := range values {
		result[i] = value.String()
	}
	return result
}
