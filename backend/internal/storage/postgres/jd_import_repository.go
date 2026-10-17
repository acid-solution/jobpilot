package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/LeoninCS/jobpilot-next/backend/internal/jdimport"
	"github.com/google/uuid"
)

type JDImportRepository struct{ database *repositoryDatabase }

func NewJDImportRepository(db *sql.DB) *JDImportRepository {
	return &JDImportRepository{newRepositoryDatabase(db)}
}
func sourceHash(raw string) string { h := sha256.Sum256([]byte(raw)); return hex.EncodeToString(h[:]) }

func (r *JDImportRepository) Create(ctx context.Context, user, target uuid.UUID, raw string, replacement *uuid.UUID) (jdimport.Session, error) {
	if replacement != nil {
		var ok bool
		if e := r.database.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM job_descriptions WHERE id=$1 AND user_id=$2 AND target_id=$3)`, *replacement, user, target).Scan(&ok); e != nil {
			return jdimport.Session{}, e
		}
		if !ok {
			return jdimport.Session{}, jdimport.ErrNotFound
		}
	}
	var id uuid.UUID
	err := r.database.QueryRowContext(ctx, `INSERT INTO jd_imports(id,user_id,target_id,raw_text,source_hash,replacement_jd_id) VALUES($1,$2,$3,$4,$5,$6)
 ON CONFLICT(user_id,target_id,source_hash,(COALESCE(replacement_jd_id,'00000000-0000-0000-0000-000000000000'::uuid))) DO UPDATE SET updated_at=jd_imports.updated_at RETURNING id`, uuid.New(), user, target, raw, sourceHash(raw), replacement).Scan(&id)
	if err != nil {
		return jdimport.Session{}, fmt.Errorf("create JD import: %w", err)
	}
	session, err := r.Get(ctx, user, id)
	if err != nil || (session.Status != "confirmed" && session.Status != "ready") || session.ReplacementID != nil {
		return session, err
	}
	if len(session.Candidates) == 1 {
		// Reuse recognition, but a deleted or edited formal JD is no longer a duplicate.
		return r.Confirm(ctx, user, id, []uuid.UUID{session.Candidates[0].ID})
	}
	return r.reopenSelection(ctx, user, id)
}

// Re-pasting a multi-job material lets the user select remaining/deleted candidates
// again. Never re-import them automatically during deletion or worker recovery.
func (r *JDImportRepository) reopenSelection(ctx context.Context, user, id uuid.UUID) (jdimport.Session, error) {
	tx, err := r.database.BeginTx(ctx, nil)
	if err != nil {
		return jdimport.Session{}, err
	}
	defer tx.Rollback()
	if err = lockUserMutationTx(ctx, tx.Tx, user); err != nil {
		return jdimport.Session{}, err
	}
	session, err := scanImport(tx.QueryRowContext(ctx, `SELECT `+importColumns+` FROM jd_imports WHERE id=$1 AND user_id=$2 FOR UPDATE`, id, user))
	if err != nil {
		return session, err
	}
	available := false
	for i := range session.Candidates {
		candidate := &session.Candidates[i]
		if candidate.JDID != nil {
			var exists bool
			if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM job_descriptions WHERE id=$1 AND user_id=$2 AND target_id=$3 AND raw_text_hash=$4)`, *candidate.JDID, user, session.TargetID, sourceHash(candidate.RawText)).Scan(&exists); err != nil {
				return session, err
			}
			if !exists {
				candidate.JDID = nil
				candidate.Duplicate = false
			}
		}
		available = available || candidate.JDID == nil
	}
	if available && (session.Status == "confirmed" || session.Status == "ready") {
		data, err := json.Marshal(session.Candidates)
		if err != nil {
			return session, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE jd_imports SET status='ready',candidates=$3,updated_at=now() WHERE id=$1 AND user_id=$2`, id, user, data); err != nil {
			return session, err
		}
	}
	if err = tx.Commit(); err != nil {
		return session, err
	}
	return r.Get(ctx, user, id)
}

const importColumns = `id,target_id,replacement_jd_id,status,candidates,last_error,updated_at`

type importScanner interface{ Scan(...any) error }

func scanImport(row importScanner) (jdimport.Session, error) {
	var out jdimport.Session
	var data []byte
	var replacement uuid.NullUUID
	if err := row.Scan(&out.ID, &out.TargetID, &replacement, &out.Status, &data, &out.ErrorCode, &out.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return out, jdimport.ErrNotFound
		}
		return out, err
	}
	if replacement.Valid {
		out.ReplacementID = &replacement.UUID
	}
	out.Candidates = []jdimport.Candidate{}
	if err := json.Unmarshal(data, &out.Candidates); err != nil {
		return out, err
	}
	out.HasPending = out.Status == "queued" || out.Status == "running"
	out.NextPollMS = 2000
	return out, nil
}
func (r *JDImportRepository) Get(ctx context.Context, user, id uuid.UUID) (jdimport.Session, error) {
	return scanImport(r.database.QueryRowContext(ctx, `SELECT `+importColumns+` FROM jd_imports WHERE user_id=$1 AND id=$2`, user, id))
}
func (r *JDImportRepository) List(ctx context.Context, user, target uuid.UUID) ([]jdimport.Session, error) {
	rows, e := r.database.QueryContext(ctx, `SELECT `+importColumns+` FROM jd_imports WHERE user_id=$1 AND target_id=$2 AND status<>'confirmed' ORDER BY created_at DESC LIMIT 50`, user, target)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []jdimport.Session{}
	for rows.Next() {
		s, e := scanImport(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
func (r *JDImportRepository) Retry(ctx context.Context, user, id uuid.UUID) (jdimport.Session, error) {
	_, e := r.database.ExecContext(ctx, `UPDATE jd_imports SET status='queued',attempts=0,last_error='',next_attempt_at=now(),lease_token=NULL,lease_expires_at=NULL,updated_at=now() WHERE id=$1 AND user_id=$2 AND status='failed'`, id, user)
	if e != nil {
		return jdimport.Session{}, e
	}
	return r.Get(ctx, user, id)
}
func (r *JDImportRepository) Claim(ctx context.Context) (jdimport.Job, error) {
	var j jdimport.Job
	e := r.database.QueryRowContext(ctx, `WITH next AS(SELECT id FROM jd_imports WHERE status='queued' AND attempts<max_attempts AND next_attempt_at<=now() ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1)
 UPDATE jd_imports i SET status='running',attempts=attempts+1,lease_token=$1,lease_expires_at=now()+interval '5 minutes',updated_at=now() FROM next WHERE i.id=next.id RETURNING i.id,i.user_id,i.target_id,i.raw_text,i.lease_token,i.attempts`, uuid.New()).Scan(&j.ID, &j.UserID, &j.TargetID, &j.RawText, &j.LeaseToken, &j.Attempts)
	if errors.Is(e, sql.ErrNoRows) {
		e = jdimport.ErrNoJob
	}
	return j, e
}
func (r *JDImportRepository) Heartbeat(ctx context.Context, j jdimport.Job) error {
	res, e := r.database.ExecContext(ctx, `UPDATE jd_imports SET lease_expires_at=now()+interval '5 minutes' WHERE id=$1 AND status='running' AND lease_token=$2`, j.ID, j.LeaseToken)
	if e != nil {
		return e
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return jdimport.ErrLeaseLost
	}
	return nil
}
func (r *JDImportRepository) LoadWindow(ctx context.Context, j jdimport.Job, index int) ([]jdimport.Candidate, bool, error) {
	var data []byte
	err := r.database.QueryRowContext(ctx, `SELECT candidates FROM jd_import_windows WHERE import_id=$1 AND window_index=$2`, j.ID, index).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var out []jdimport.Candidate
	err = json.Unmarshal(data, &out)
	return out, err == nil, err
}
func (r *JDImportRepository) SaveWindow(ctx context.Context, j jdimport.Job, index int, candidates []jdimport.Candidate) error {
	data, err := json.Marshal(candidates)
	if err != nil {
		return err
	}
	res, err := r.database.ExecContext(ctx, `WITH held AS(SELECT id FROM jd_imports WHERE id=$1 AND lease_token=$2 AND status='running' FOR UPDATE) INSERT INTO jd_import_windows(import_id,window_index,candidates) SELECT id,$3,$4::jsonb FROM held ON CONFLICT(import_id,window_index) DO UPDATE SET candidates=EXCLUDED.candidates`, j.ID, j.LeaseToken, index, data)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return jdimport.ErrLeaseLost
	}
	return nil
}
func (r *JDImportRepository) Recover(ctx context.Context) error {
	_, e := r.database.ExecContext(ctx, `UPDATE jd_imports SET status=CASE WHEN attempts<max_attempts THEN 'queued' ELSE 'failed' END,last_error='worker_interrupted',lease_token=NULL,lease_expires_at=NULL,next_attempt_at=now(),updated_at=now() WHERE status='running' AND lease_expires_at<now()`)
	if e != nil {
		return e
	}
	rows, e := r.database.QueryContext(ctx, `SELECT id,user_id,candidates FROM jd_imports WHERE status='ready' AND jsonb_array_length(candidates)=1`)
	if e != nil {
		return e
	}
	type selection struct {
		id, user  uuid.UUID
		candidate uuid.UUID
	}
	pending := []selection{}
	for rows.Next() {
		var v selection
		var data []byte
		if e = rows.Scan(&v.id, &v.user, &data); e != nil {
			rows.Close()
			return e
		}
		var cs []jdimport.Candidate
		if e = json.Unmarshal(data, &cs); e != nil {
			rows.Close()
			return e
		}
		v.candidate = cs[0].ID
		pending = append(pending, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, v := range pending {
		if _, e = r.Confirm(ctx, v.user, v.id, []uuid.UUID{v.candidate}); e != nil && !errors.Is(e, jdimport.ErrConflict) {
			return e
		}
	}
	return nil
}
func (r *JDImportRepository) Complete(ctx context.Context, j jdimport.Job, candidates []jdimport.Candidate) error {
	if len(candidates) > 50 {
		return errors.New("too many JD candidates")
	}
	for i := range candidates {
		candidates[i].ID = uuid.NewSHA1(j.ID, []byte(candidates[i].RawText))
	}
	status := "ready"
	if len(candidates) == 0 {
		status = "empty"
	}
	if candidates == nil {
		candidates = []jdimport.Candidate{}
	}
	data, e := json.Marshal(candidates)
	if e != nil {
		return e
	}
	res, e := r.database.ExecContext(ctx, `UPDATE jd_imports SET status=$3,candidates=$4,last_error='',lease_token=NULL,lease_expires_at=NULL,updated_at=now() WHERE id=$1 AND lease_token=$2 AND status='running'`, j.ID, j.LeaseToken, status, data)
	if e != nil {
		return e
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return jdimport.ErrLeaseLost
	}
	return nil
}
func (r *JDImportRepository) Fail(ctx context.Context, j jdimport.Job, code string, retry bool) error {
	status := "failed"
	if retry && j.Attempts < 3 {
		status = "queued"
	}
	_, e := r.database.ExecContext(ctx, `UPDATE jd_imports SET status=$3,last_error=$4,next_attempt_at=$5,lease_token=NULL,lease_expires_at=NULL,updated_at=now() WHERE id=$1 AND lease_token=$2 AND status='running'`, j.ID, j.LeaseToken, status, code, time.Now().Add(time.Duration(j.Attempts*j.Attempts*10)*time.Second))
	return e
}

func (r *JDImportRepository) Confirm(ctx context.Context, user, id uuid.UUID, ids []uuid.UUID) (jdimport.Session, error) {
	tx, e := r.database.BeginTx(ctx, nil)
	if e != nil {
		return jdimport.Session{}, e
	}
	defer tx.Rollback()
	if e = lockUserMutationTx(ctx, tx.Tx, user); e != nil {
		return jdimport.Session{}, e
	}
	s, e := scanImport(tx.QueryRowContext(ctx, `SELECT `+importColumns+` FROM jd_imports WHERE id=$1 AND user_id=$2 FOR UPDATE`, id, user))
	if e != nil {
		return s, e
	}
	if s.Status != "ready" && s.Status != "confirmed" {
		return s, jdimport.ErrConflict
	}
	if s.ReplacementID != nil && len(ids) != 1 {
		return s, jdimport.ErrConflict
	}
	if s.ReplacementID != nil && s.Status == "confirmed" {
		for _, candidate := range s.Candidates {
			if candidate.JDID != nil && candidate.ID != ids[0] {
				return s, jdimport.ErrConflict
			}
		}
	}
	wanted := map[uuid.UUID]bool{}
	for _, v := range ids {
		wanted[v] = true
	}
	if len(wanted) == 0 || len(wanted) != len(ids) {
		return s, jdimport.ErrConflict
	}
	for _, c := range s.Candidates {
		delete(wanted, c.ID)
	}
	if len(wanted) > 0 {
		return s, jdimport.ErrConflict
	}
	wanted = map[uuid.UUID]bool{}
	for _, v := range ids {
		wanted[v] = true
	}
	for i := range s.Candidates {
		c := &s.Candidates[i]
		if !wanted[c.ID] {
			continue
		}
		if c.JDID != nil {
			var exists bool
			if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM job_descriptions WHERE id=$1 AND user_id=$2 AND target_id=$3 AND raw_text_hash=$4)`, *c.JDID, user, s.TargetID, sourceHash(c.RawText)).Scan(&exists); e != nil {
				return s, e
			}
			if exists {
				continue
			}
			c.JDID = nil
			c.Duplicate = false
		}
		var existing uuid.UUID
		e = tx.QueryRowContext(ctx, `SELECT id FROM job_descriptions WHERE user_id=$1 AND target_id=$2 AND raw_text_hash=$3`, user, s.TargetID, sourceHash(c.RawText)).Scan(&existing)
		if e == nil && (s.ReplacementID == nil || existing != *s.ReplacementID) {
			c.JDID = &existing
			c.Duplicate = true
			continue
		}
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return s, e
		}
		jdID := uuid.New()
		if s.ReplacementID != nil {
			jdID = *s.ReplacementID
			var matches bool
			if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM job_descriptions WHERE id=$1 AND user_id=$2 AND target_id=$3)`, jdID, user, s.TargetID).Scan(&matches); e != nil {
				return s, e
			}
			if !matches {
				return s, jdimport.ErrNotFound
			}
			_, e = tx.ExecContext(ctx, `UPDATE job_descriptions SET raw_text=$3,raw_text_hash=$4,import_id=$5,status='processing',validation_status='pending',updated_at=now() WHERE id=$1 AND user_id=$2`, jdID, user, c.RawText, sourceHash(c.RawText), id)
		} else {
			_, e = tx.ExecContext(ctx, `INSERT INTO job_descriptions(id,user_id,target_id,raw_text,raw_text_hash,status,import_id) VALUES($1,$2,$3,$4,$5,'processing',$6)`, jdID, user, s.TargetID, c.RawText, sourceHash(c.RawText), id)
		}
		if e != nil {
			return s, e
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO analysis_jobs(id,user_id,target_id,job_description_id,job_type,status,max_attempts) VALUES($1,$2,$3,$4,'jd_analysis','queued',12) ON CONFLICT(job_description_id,job_type) DO UPDATE SET status='queued',attempts=0,max_attempts=12,next_attempt_at=now(),lease_token=NULL,lease_expires_at=NULL,heartbeat_at=NULL,last_error=NULL,preserve_previous_result=FALSE,updated_at=now()`, uuid.New(), user, s.TargetID, jdID)
		if e != nil {
			return s, e
		}
		c.JDID = &jdID
	}
	data, e := json.Marshal(s.Candidates)
	if e != nil {
		return s, e
	}
	_, e = tx.ExecContext(ctx, `UPDATE jd_imports SET candidates=$3,status='confirmed',updated_at=now() WHERE id=$1 AND user_id=$2`, id, user, data)
	if e != nil {
		return s, e
	}
	if e = tx.Commit(); e != nil {
		return s, e
	}
	return r.Get(ctx, user, id)
}
