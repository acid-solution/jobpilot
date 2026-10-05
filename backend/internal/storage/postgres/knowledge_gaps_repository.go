package postgres

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/LeoninCS/jobpilot-next/backend/internal/abilitygrading"
	"github.com/LeoninCS/jobpilot-next/backend/internal/knowledgegaps"
	"github.com/LeoninCS/jobpilot-next/backend/internal/mutationlock"
	"github.com/google/uuid"
)

type KnowledgeGapsRepository struct{ database *repositoryDatabase }

func NewKnowledgeGapsRepository(database *sql.DB) *KnowledgeGapsRepository {
	return &KnowledgeGapsRepository{newRepositoryDatabase(database)}
}

func (r *KnowledgeGapsRepository) WithinUserSnapshot(ctx context.Context, userID uuid.UUID, run func(context.Context) error) error {
	if binding, ok := ctx.Value(transactionKey{}).(transactionBinding); ok && binding.pool == r.database.pool {
		// A failed automatic refresh must not abort the surrounding Agent action.
		savepoint, err := r.database.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer savepoint.Rollback()
		if err := run(ctx); err != nil {
			return err
		}
		return savepoint.Commit()
	}
	options := &sql.TxOptions{Isolation: sql.LevelRepeatableRead}
	var tx *sql.Tx
	var err error
	if mutationlock.HeldBy(ctx, userID) {
		// The page mutation middleware holds the same lock on another connection.
		tx, err = r.database.pool.BeginTx(ctx, options)
	} else {
		// Acquire the lock BEFORE beginning the repeatable-read transaction. A
		// SELECT pg_try_advisory_xact_lock inside that transaction would establish
		// its MVCC snapshot before lock acquisition, possibly missing the report
		// just published by the previous holder.
		conn, release, lockErr := lockGapSnapshotConnection(ctx, r.database.pool, userID)
		if lockErr != nil {
			return lockErr
		}
		defer release()
		tx, err = conn.BeginTx(ctx, options)
	}
	if err != nil {
		return err
	}
	defer tx.Rollback()
	snapshotCtx := context.WithValue(ctx, transactionKey{}, transactionBinding{pool: r.database.pool, tx: tx})
	if err := run(snapshotCtx); err != nil {
		return err
	}
	return tx.Commit()
}

func lockGapSnapshotConnection(ctx context.Context, pool *sql.DB, userID uuid.UUID) (*sql.Conn, func(), error) {
	key := mutationlock.Key(userID)
	for {
		conn, err := pool.Conn(ctx)
		if err != nil {
			return nil, nil, err
		}
		release := func() {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			if _, err := conn.ExecContext(cleanupCtx, `SELECT pg_advisory_unlock($1)`, key); err != nil {
				// Never return a possibly locked session to the pool.
				_ = conn.Raw(func(any) error { return driver.ErrBadConn })
			}
			_ = conn.Close()
		}
		var acquired bool
		err = conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1)`, key).Scan(&acquired)
		if err != nil {
			release()
			return nil, nil, err
		}
		if acquired {
			return conn, release, nil
		}
		_ = conn.Close()
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, nil, ctx.Err()
		case <-timer.C:
		}
	}
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
		LEFT JOIN jd_ability_level_jobs level_job ON level_job.job_description_id=jd.id
		LEFT JOIN jd_ability_option_levels grade ON grade.option_id=option.id
		    AND grade.ability_id=option.ability_id
		    AND level_job.result_fingerprint=jd_ability_grading_fingerprint(jd.id,$3)
		WHERE jd.user_id=$1 AND jd.target_id=$2 AND jd.status='included' AND jd.validation_status='valid'
		ORDER BY jd.id,requirement.sort_order,option.sort_order`, userID, targetID, abilitygrading.PromptVersion)
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
