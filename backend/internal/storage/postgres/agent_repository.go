package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/LeoninCS/jobpilot-next/backend/internal/agent"
	"github.com/google/uuid"
)

type AgentRepository struct{ db *repositoryDatabase }

func NewAgentRepository(db *sql.DB) *AgentRepository {
	return &AgentRepository{db: newRepositoryDatabase(db)}
}

func (r *AgentRepository) RecoverRuns(ctx context.Context) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `WITH interrupted AS (
		UPDATE agent_actions a SET status='failed',error_code='execution_interrupted_unknown',updated_at=NOW()
		FROM agent_conversations c WHERE a.conversation_id=c.id AND a.status='executing'
		  AND c.status='running' AND c.run_expires_at<NOW()
		RETURNING a.conversation_id,a.summary
	) INSERT INTO agent_messages(conversation_id,role,content)
	  SELECT conversation_id,'assistant',
	    '操作“'||summary||'”执行中断，结果尚不能确认。请先在相应页面核对实际资料，再决定是否重新发起。'
	  FROM interrupted`)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE agent_actions a SET status='failed',error_code='checkpoint_missing',updated_at=NOW()
		FROM agent_conversations c WHERE a.conversation_id=c.id AND a.status='pending' AND a.interrupt_id='' AND c.status='running' AND c.run_expires_at<NOW()`)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE agent_conversations c SET status=CASE WHEN EXISTS(SELECT 1 FROM agent_actions a WHERE a.conversation_id=c.id AND a.status='pending') THEN 'awaiting_confirmation' ELSE 'idle' END,
		run_token=NULL,run_expires_at=NULL,updated_at=NOW() WHERE c.status='running' AND c.run_expires_at<NOW()`)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (r *AgentRepository) List(ctx context.Context, user uuid.UUID, scope string) ([]agent.Conversation, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id,title,status,updated_at FROM agent_conversations WHERE user_id=$1 AND goal_signature=$2 ORDER BY updated_at DESC LIMIT 100`, user, scope)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []agent.Conversation{}
	for rows.Next() {
		var v agent.Conversation
		if err = rows.Scan(&v.ID, &v.Title, &v.Status, &v.UpdatedAt); err != nil {
			return nil, err
		}
		values = append(values, v)
	}
	return values, rows.Err()
}
func (r *AgentRepository) Create(ctx context.Context, user uuid.UUID, scope, title string) (agent.Conversation, error) {
	var v agent.Conversation
	err := r.db.QueryRowContext(ctx, `INSERT INTO agent_conversations(user_id,goal_signature,title) VALUES($1,$2,$3) RETURNING id,title,status,updated_at`, user, scope, title).Scan(&v.ID, &v.Title, &v.Status, &v.UpdatedAt)
	return v, err
}
func (r *AgentRepository) Get(ctx context.Context, user uuid.UUID, scope string, id uuid.UUID) (agent.Conversation, error) {
	var v agent.Conversation
	err := r.db.QueryRowContext(ctx, `SELECT id,title,status,updated_at FROM agent_conversations WHERE id=$1 AND user_id=$2 AND goal_signature=$3`, id, user, scope).Scan(&v.ID, &v.Title, &v.Status, &v.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return v, agent.ErrNotFound
	}
	if err != nil {
		return v, err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id,role,content,created_at FROM (
		SELECT id,role,content,created_at FROM agent_messages WHERE conversation_id=$1
		ORDER BY created_at DESC,id DESC LIMIT 200
	) recent ORDER BY created_at,id`, id)
	if err != nil {
		return v, err
	}
	defer rows.Close()
	for rows.Next() {
		var m agent.Message
		if err = rows.Scan(&m.ID, &m.Role, &m.Content, &m.CreatedAt); err != nil {
			return v, err
		}
		v.Messages = append(v.Messages, m)
	}
	if err = rows.Err(); err != nil {
		return v, err
	}
	var action agent.Action
	var versions []byte
	err = r.db.QueryRowContext(ctx, `SELECT id,kind,arguments,summary,status,interrupt_id,expected_hash,expected_versions FROM agent_actions WHERE conversation_id=$1 AND status='pending'`, id).Scan(&action.ID, &action.Kind, &action.Arguments, &action.Summary, &action.Status, &action.InterruptID, &action.ExpectedHash, &versions)
	if err == nil {
		if err := json.Unmarshal(versions, &action.ExpectedVersions); err != nil {
			return v, err
		}
		v.PendingAction = &action
	} else if !errors.Is(err, sql.ErrNoRows) {
		return v, err
	}
	return v, nil
}
func (r *AgentRepository) Delete(ctx context.Context, user uuid.UUID, scope string, id uuid.UUID) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `DELETE FROM agent_conversations WHERE id=$1 AND user_id=$2 AND goal_signature=$3 AND status<>'running'`, id, user, scope)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return agent.ErrNotFound
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM agent_checkpoints WHERE key=$1`, "agent-"+id.String()); err != nil {
		return err
	}
	return tx.Commit()
}
func (r *AgentRepository) BeginRun(ctx context.Context, user uuid.UUID, scope string, id uuid.UUID) (uuid.UUID, error) {
	token := uuid.New()
	result, err := r.db.ExecContext(ctx, `UPDATE agent_conversations SET status='running',run_token=$4,run_expires_at=NOW()+INTERVAL '5 minutes',updated_at=NOW()
		WHERE id=$1 AND user_id=$2 AND goal_signature=$3 AND (status='idle' OR (status='running' AND run_expires_at<NOW()))
		AND NOT EXISTS(SELECT 1 FROM agent_actions WHERE conversation_id=$1 AND status IN ('pending','executing'))`, id, user, scope, token)
	if err != nil {
		return uuid.Nil, err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return uuid.Nil, agent.ErrBusy
	}
	return token, nil
}
func (r *AgentRepository) BeginResume(ctx context.Context, user uuid.UUID, scope string, id uuid.UUID) (uuid.UUID, error) {
	token := uuid.New()
	result, err := r.db.ExecContext(ctx, `UPDATE agent_conversations SET status='running',run_token=$4,run_expires_at=NOW()+INTERVAL '5 minutes',updated_at=NOW()
		WHERE id=$1 AND user_id=$2 AND goal_signature=$3 AND status='awaiting_confirmation'`, id, user, scope, token)
	if err != nil {
		return uuid.Nil, err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return uuid.Nil, agent.ErrBusy
	}
	return token, nil
}
func (r *AgentRepository) EndRun(ctx context.Context, id, token uuid.UUID, _ bool) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var held bool
	err = tx.QueryRowContext(ctx, `SELECT TRUE FROM agent_conversations WHERE id=$1 AND run_token=$2 AND status='running' FOR UPDATE`, id, token).Scan(&held)
	if errors.Is(err, sql.ErrNoRows) {
		return agent.ErrRunLost
	}
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE agent_actions SET status='failed',error_code='checkpoint_missing',updated_at=NOW()
		WHERE conversation_id=$1 AND status='pending' AND interrupt_id=''`, id); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE agent_conversations c SET
		status=CASE WHEN EXISTS(SELECT 1 FROM agent_actions a WHERE a.conversation_id=c.id AND a.status='pending') THEN 'awaiting_confirmation' ELSE 'idle' END,
		run_token=NULL,run_expires_at=NULL,updated_at=NOW() WHERE c.id=$1 AND c.run_token=$2`, id, token)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (r *AgentRepository) HeartbeatRun(ctx context.Context, id, token uuid.UUID) error {
	result, err := r.db.ExecContext(ctx, `UPDATE agent_conversations SET run_expires_at=NOW()+INTERVAL '5 minutes' WHERE id=$1 AND run_token=$2 AND status='running'`, id, token)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return agent.ErrRunLost
	}
	return nil
}
func (r *AgentRepository) AddMessage(ctx context.Context, conversation uuid.UUID, role, content string) (agent.Message, error) {
	var m agent.Message
	err := r.db.QueryRowContext(ctx, `INSERT INTO agent_messages(conversation_id,role,content) VALUES($1,$2,$3) RETURNING id,role,content,created_at`, conversation, role, content).Scan(&m.ID, &m.Role, &m.Content, &m.CreatedAt)
	if err == nil {
		_, _ = r.db.ExecContext(ctx, `UPDATE agent_conversations SET title=CASE WHEN title='新对话' AND $2='user' THEN LEFT($3,24) ELSE title END,updated_at=NOW() WHERE id=$1`, conversation, role, content)
	}
	return m, err
}
func (r *AgentRepository) ReadResourceVersions(ctx context.Context, user uuid.UUID, keys []string) (map[string]int64, error) {
	versions := make(map[string]int64, len(keys))
	for _, key := range keys {
		versions[key] = 0
	}
	if len(keys) == 0 {
		return versions, nil
	}
	encoded, err := json.Marshal(keys)
	if err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT resource_key,version FROM resource_versions
		WHERE user_id=$1 AND resource_key IN (SELECT jsonb_array_elements_text($2::jsonb))`, user, encoded)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var version int64
		if err := rows.Scan(&key, &version); err != nil {
			return nil, err
		}
		versions[key] = version
	}
	return versions, rows.Err()
}

func (r *AgentRepository) CreateAction(ctx context.Context, conversation, user uuid.UUID, scope, kind string, args json.RawMessage, summary string, snapshot agent.Snapshot) (agent.Action, error) {
	var a agent.Action
	if len(snapshot.Versions) == 0 || snapshot.Hash == "" {
		return a, agent.ErrStale
	}
	versions, err := json.Marshal(snapshot.Versions)
	if err != nil {
		return a, err
	}
	err = r.db.QueryRowContext(ctx, `INSERT INTO agent_actions(conversation_id,user_id,goal_signature,kind,arguments,summary,expected_hash,expected_versions)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id,kind,arguments,summary,status`, conversation, user, scope, kind, args, summary, snapshot.Hash, versions).Scan(&a.ID, &a.Kind, &a.Arguments, &a.Summary, &a.Status)
	a.ExpectedHash, a.ExpectedVersions = snapshot.Hash, snapshot.Versions
	return a, err
}
func (r *AgentRepository) SetInterrupt(ctx context.Context, conversation, action uuid.UUID, interrupt string) error {
	result, err := r.db.ExecContext(ctx, `UPDATE agent_actions SET interrupt_id=$3,updated_at=NOW() WHERE id=$1 AND conversation_id=$2 AND status='pending'`, action, conversation, interrupt)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return agent.ErrActionClosed
	}
	return nil
}
func (r *AgentRepository) GetAction(ctx context.Context, user uuid.UUID, scope string, id uuid.UUID) (agent.Action, error) {
	var a agent.Action
	var versions []byte
	err := r.db.QueryRowContext(ctx, `SELECT id,kind,arguments,summary,status,interrupt_id,expected_hash,expected_versions FROM agent_actions WHERE id=$1 AND user_id=$2 AND goal_signature=$3`, id, user, scope).Scan(&a.ID, &a.Kind, &a.Arguments, &a.Summary, &a.Status, &a.InterruptID, &a.ExpectedHash, &versions)
	if errors.Is(err, sql.ErrNoRows) {
		return a, agent.ErrNotFound
	}
	if err == nil {
		err = json.Unmarshal(versions, &a.ExpectedVersions)
	}
	return a, err
}
func (r *AgentRepository) ClaimAction(ctx context.Context, id, conversation uuid.UUID) (bool, error) {
	result, err := r.db.ExecContext(ctx, `UPDATE agent_actions SET status='executing',updated_at=NOW() WHERE id=$1 AND conversation_id=$2 AND status='pending'`, id, conversation)
	if err != nil {
		return false, err
	}
	n, _ := result.RowsAffected()
	return n == 1, nil
}
func (r *AgentRepository) FinishAction(ctx context.Context, id uuid.UUID, status string, result json.RawMessage, errorCode string) error {
	updated, err := r.db.ExecContext(ctx, `UPDATE agent_actions SET status=$2,result=$3,error_code=$4,updated_at=NOW() WHERE id=$1 AND status='executing'`, id, status, result, errorCode)
	if err != nil {
		return err
	}
	rows, err := updated.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return agent.ErrActionClosed
	}
	return nil
}
func (r *AgentRepository) CancelAction(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `UPDATE agent_actions SET status='cancelled',updated_at=NOW() WHERE id=$1 AND status='pending'`, id)
	return err
}
func (r *AgentRepository) StaleAction(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `UPDATE agent_actions SET status='stale',updated_at=NOW() WHERE id=$1 AND status='pending'`, id)
	return err
}

type AgentCheckpointStore struct{ db *sql.DB }

func NewAgentCheckpointStore(db *sql.DB) *AgentCheckpointStore { return &AgentCheckpointStore{db: db} }
func (s *AgentCheckpointStore) Get(ctx context.Context, key string) ([]byte, bool, error) {
	var b []byte
	err := s.db.QueryRowContext(ctx, `SELECT value FROM agent_checkpoints WHERE key=$1`, key).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	return b, err == nil, err
}
func (s *AgentCheckpointStore) Set(ctx context.Context, key string, value []byte) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO agent_checkpoints(key,value) VALUES($1,$2) ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value,updated_at=NOW()`, key, value)
	return err
}
func (s *AgentCheckpointStore) Delete(ctx context.Context, key string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := s.db.ExecContext(ctx, `DELETE FROM agent_checkpoints WHERE key=$1`, key)
	return err
}
