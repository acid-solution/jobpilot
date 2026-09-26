package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/LeoninCS/jobpilot-next/backend/internal/agent"
	"github.com/LeoninCS/jobpilot-next/backend/internal/embedding"
	"github.com/LeoninCS/jobpilot-next/backend/internal/jdanalysis"
	"github.com/google/uuid"
)

type EmbeddingRepository struct{ database *repositoryDatabase }

func NewEmbeddingRepository(db *sql.DB) *EmbeddingRepository {
	return &EmbeddingRepository{database: newRepositoryDatabase(db)}
}

func (r *EmbeddingRepository) EmbeddingsReady(ctx context.Context) (bool, error) {
	// The initial seed backfill should settle before historical normalization
	// starts. A terminal failure, or a newly reviewed dynamic ability, must not
	// stop unrelated JD and material jobs for every user.
	var pending bool
	err := r.database.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM abilities ability JOIN embedding_jobs job
		  ON job.source_type='ability' AND job.source_id=ability.id
		WHERE ability.is_active AND ability.source='seed'
		  AND (ability.embedding IS NULL OR ability.embedding_model<>$1)
		  AND job.status IN ('queued','running'))`, embedding.Model).Scan(&pending)
	return !pending, err
}

func (r *EmbeddingRepository) RecoverExpired(ctx context.Context) error {
	_, err := r.database.ExecContext(ctx, `UPDATE embedding_jobs SET status=CASE WHEN attempts<max_attempts THEN 'queued' ELSE 'failed' END,
		next_attempt_at=NOW(),lease_token=NULL,heartbeat_at=NULL,lease_expires_at=NULL,last_error='worker_interrupted',updated_at=NOW()
		WHERE status='running' AND lease_expires_at<NOW()`)
	if err != nil {
		return err
	}
	// A provider outage should not require editing database rows by hand. Retry
	// terminal ability failures at most once per day; the readiness check above
	// continues to let other work proceed while this retry is pending.
	_, err = r.database.ExecContext(ctx, `UPDATE embedding_jobs job SET status='queued',attempts=0,next_attempt_at=NOW(),
		last_error='',updated_at=NOW() FROM abilities ability
		WHERE job.source_type='ability' AND job.source_id=ability.id AND ability.is_active
		  AND job.status='failed' AND job.last_error='embedding_failed'
		  AND job.updated_at<NOW()-INTERVAL '1 day'
		  AND (ability.embedding IS NULL OR ability.embedding_model<>$1)`, embedding.Model)
	return err
}
func (r *EmbeddingRepository) Claim(ctx context.Context) (embedding.Job, error) {
	job := embedding.Job{LeaseToken: uuid.New()}
	var user sql.NullString
	err := r.database.QueryRowContext(ctx, `WITH candidate AS (
		SELECT id FROM embedding_jobs WHERE status='queued' AND next_attempt_at<=NOW()
		ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1)
		UPDATE embedding_jobs e SET status='running',attempts=e.attempts+1,lease_token=$1,
		heartbeat_at=NOW(),lease_expires_at=NOW()+INTERVAL '5 minutes',updated_at=NOW()
		FROM candidate WHERE e.id=candidate.id
		RETURNING e.id,e.source_id,e.source_type,e.source_hash,e.user_id`, job.LeaseToken).Scan(&job.ID, &job.SourceID, &job.SourceType, &job.SourceHash, &user)
	if errors.Is(err, sql.ErrNoRows) {
		return embedding.Job{}, embedding.ErrNoJob
	}
	if err != nil {
		return embedding.Job{}, err
	}
	if user.Valid {
		parsed, parseErr := uuid.Parse(user.String)
		if parseErr != nil {
			return embedding.Job{}, parseErr
		}
		job.UserID = &parsed
	}
	return job, nil
}
func (r *EmbeddingRepository) Heartbeat(ctx context.Context, job embedding.Job) error {
	result, err := r.database.ExecContext(ctx, `UPDATE embedding_jobs SET heartbeat_at=NOW(),lease_expires_at=NOW()+INTERVAL '5 minutes',updated_at=NOW() WHERE id=$1 AND status='running' AND lease_token=$2`, job.ID, job.LeaseToken)
	if err != nil {
		return err
	}
	return embeddingLease(result)
}
func (r *EmbeddingRepository) Source(ctx context.Context, job embedding.Job) (embedding.Source, error) {
	var source embedding.Source
	switch job.SourceType {
	case "ability":
		err := r.database.QueryRowContext(ctx, `SELECT name||'；别名：'||aliases::text||'；定义：'||COALESCE(definition,''),encode(digest(name||aliases::text||COALESCE(definition,''),'sha256'),'hex') FROM abilities WHERE id=$1 AND is_active`, job.SourceID).Scan(&source.Text, &source.Hash)
		return source, err
	case "jd":
		var owner uuid.UUID
		err := r.database.QueryRowContext(ctx, `SELECT jd.raw_text,jd.target_id::text,encode(digest(jd.raw_text,'sha256'),'hex'),jd.user_id FROM job_descriptions jd WHERE jd.id=$1 AND jd.user_id=$2`, job.SourceID, job.UserID).Scan(&source.Text, &source.GoalSignature, &source.Hash, &owner)
		source.UserID = &owner
		return source, err
	case "resume", "experience":
		var owner uuid.UUID
		err := r.database.QueryRowContext(ctx, `SELECT source_text,encode(digest(source_text,'sha256'),'hex'),user_id FROM user_profile_materials WHERE id=$1 AND user_id=$2 AND type=$3 AND status='ready'`, job.SourceID, job.UserID, job.SourceType).Scan(&source.Text, &source.Hash, &owner)
		source.UserID = &owner
		return source, err
	default:
		return source, errors.New("unknown embedding source")
	}
}
func (r *EmbeddingRepository) Complete(ctx context.Context, job embedding.Job, source embedding.Source, chunks []embedding.VectorChunk) error {
	tx, err := r.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var active bool
	if err = tx.QueryRowContext(ctx, `SELECT TRUE FROM embedding_jobs WHERE id=$1 AND status='running' AND lease_token=$2 FOR UPDATE`, job.ID, job.LeaseToken).Scan(&active); err != nil {
		return embedding.ErrLeaseLost
	}
	if source.Hash != job.SourceHash {
		return errors.New("embedding source changed")
	}
	if job.SourceType == "ability" {
		if len(chunks) != 1 {
			return errors.New("ability embedding must have one chunk")
		}
		_, err = tx.ExecContext(ctx, `UPDATE abilities SET embedding=$2::vector,embedding_model=$3,embedding_updated_at=NOW() WHERE id=$1`, job.SourceID, vectorLiteral(chunks[0].Vector), embedding.Model)
		if err != nil {
			return err
		}
	} else {
		_, err = tx.ExecContext(ctx, `DELETE FROM source_embeddings WHERE source_type=$1 AND source_id=$2`, job.SourceType, job.SourceID)
		if err != nil {
			return err
		}
		for i, part := range chunks {
			_, err = tx.ExecContext(ctx, `INSERT INTO source_embeddings(user_id,goal_signature,source_type,source_id,source_hash,chunk_index,start_offset,end_offset,text,embedding,embedding_model)
				VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10::vector,$11)`, source.UserID, source.GoalSignature, job.SourceType, job.SourceID, job.SourceHash, i, part.Start, part.End, part.Text, vectorLiteral(part.Vector), embedding.Model)
			if err != nil {
				return err
			}
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE embedding_jobs SET status='succeeded',lease_token=NULL,heartbeat_at=NULL,lease_expires_at=NULL,last_error='',updated_at=NOW() WHERE id=$1 AND status='running' AND lease_token=$2`, job.ID, job.LeaseToken)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (r *EmbeddingRepository) Fail(ctx context.Context, job embedding.Job, code string) error {
	result, err := r.database.ExecContext(ctx, `UPDATE embedding_jobs SET status=CASE WHEN attempts<max_attempts THEN 'queued' ELSE 'failed' END,
		next_attempt_at=NOW()+((attempts*attempts*10)*INTERVAL '1 second'),lease_token=NULL,heartbeat_at=NULL,lease_expires_at=NULL,last_error=$3,updated_at=NOW()
		WHERE id=$1 AND status='running' AND lease_token=$2`, job.ID, job.LeaseToken, code)
	if err != nil {
		return err
	}
	return embeddingLease(result)
}
func embeddingLease(result sql.Result) error {
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return embedding.ErrLeaseLost
	}
	return nil
}

func vectorLiteral(vector []float32) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, v := range vector {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(v), 'g', -1, 32))
	}
	b.WriteByte(']')
	return b.String()
}

func (r *EmbeddingRepository) SearchSources(ctx context.Context, userID, targetID uuid.UUID, vector []float32, limit int) ([]agent.SourceMatch, error) {
	if limit < 1 || limit > 10 {
		return nil, fmt.Errorf("invalid source search limit")
	}
	rows, err := r.database.QueryContext(ctx, `SELECT e.source_type,e.source_id,e.start_offset,e.end_offset,e.text FROM source_embeddings e
		LEFT JOIN job_descriptions jd ON e.source_type='jd' AND e.source_id=jd.id
		LEFT JOIN user_profile_materials m ON e.source_type IN ('resume','experience') AND e.source_id=m.id
		WHERE e.user_id=$1 AND (
		  (e.source_type='jd' AND jd.user_id=$1 AND jd.target_id=$2 AND e.source_hash=encode(digest(jd.raw_text,'sha256'),'hex')) OR
		  (e.source_type IN ('resume','experience') AND m.user_id=$1 AND m.status='ready' AND e.source_hash=encode(digest(m.source_text,'sha256'),'hex'))
		)
		ORDER BY e.embedding <=> $3::vector LIMIT $4`, userID, targetID, vectorLiteral(vector), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var matches []agent.SourceMatch
	for rows.Next() {
		var value agent.SourceMatch
		if err := rows.Scan(&value.SourceType, &value.SourceID, &value.Start, &value.End, &value.Quote); err != nil {
			return nil, err
		}
		matches = append(matches, value)
	}
	return matches, rows.Err()
}

func (r *EmbeddingRepository) SearchAbilities(ctx context.Context, vector []float32, limit int) ([]jdanalysis.AbilityMatch, error) {
	if limit < 1 || limit > 20 {
		return nil, errors.New("invalid ability search limit")
	}
	rows, err := r.database.QueryContext(ctx, `SELECT a.code,a.name,c.code,a.aliases,COALESCE(a.definition,'') FROM abilities a JOIN ability_categories c ON c.id=a.category_id
		WHERE a.is_active AND a.embedding IS NOT NULL ORDER BY a.embedding <=> $1::vector LIMIT $2`, vectorLiteral(vector), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var values []jdanalysis.AbilityMatch
	for rows.Next() {
		var value jdanalysis.AbilityMatch
		var aliases []byte
		if err := rows.Scan(&value.Code, &value.Name, &value.CategoryCode, &aliases, &value.Definition); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(aliases, &value.Aliases); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (r *EmbeddingRepository) SearchAbilityIDs(ctx context.Context, vector []float32, limit int) ([]uuid.UUID, error) {
	if limit < 1 || limit > 30 {
		return nil, errors.New("invalid ability search limit")
	}
	rows, err := r.database.QueryContext(ctx, `SELECT id FROM abilities WHERE is_active AND embedding IS NOT NULL ORDER BY embedding <=> $1::vector LIMIT $2`, vectorLiteral(vector), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var values []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		values = append(values, id)
	}
	return values, rows.Err()
}
