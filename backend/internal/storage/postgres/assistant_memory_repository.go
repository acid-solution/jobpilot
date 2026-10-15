package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/LeoninCS/jobpilot-next/backend/internal/assistantmemory"
	"github.com/google/uuid"
)

type AssistantMemoryRepository struct{ *AgentRepository }

func NewAssistantMemoryRepository(db *sql.DB) *AssistantMemoryRepository {
	return &AssistantMemoryRepository{NewAgentRepository(db)}
}
func (r *AssistantMemoryRepository) within(ctx context.Context, user uuid.UUID, fn func(context.Context) error) error {
	if b, ok := ctx.Value(transactionKey{}).(transactionBinding); ok && b.pool == r.db.pool {
		return fn(ctx)
	}
	return r.WithinUserTransaction(ctx, user, fn)
}
func (r *AssistantMemoryRepository) Snapshot(ctx context.Context, user uuid.UUID, goal string) (assistantmemory.Snapshot, error) {
	s := assistantmemory.Snapshot{Memories: []assistantmemory.Memory{}}
	rows, err := r.db.QueryContext(ctx, `SELECT id,scope,memory_key,value,revision,content_hash,status,source_message_id,source_quote,updated_at FROM agent_memories WHERE user_id=$1 AND scope IN('global',$2) ORDER BY scope,memory_key`, user, goal)
	if err != nil {
		return s, err
	}
	for rows.Next() {
		var m assistantmemory.Memory
		if err = rows.Scan(&m.ID, &m.Scope, &m.Key, &m.Value, &m.Revision, &m.Hash, &m.Status, &m.SourceMessageID, &m.SourceQuote, &m.UpdatedAt); err != nil {
			rows.Close()
			return s, err
		}
		if m.Status == "active" {
			s.Memories = append(s.Memories, m)
		} else {
			s.Forgotten = append(s.Forgotten, m)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return s, err
	}
	err = r.db.QueryRowContext(ctx, `SELECT COALESCE((SELECT epoch FROM agent_context_scopes WHERE user_id=$1 AND scope='global'),0),COALESCE((SELECT epoch FROM agent_context_scopes WHERE user_id=$1 AND scope=$2),0)`, user, goal).Scan(&s.Epoch.Global, &s.Epoch.Goal)
	return s, err
}
func (r *AssistantMemoryRepository) Save(ctx context.Context, user uuid.UUID, goal string, id *uuid.UUID, w assistantmemory.Write) (m assistantmemory.Memory, err error) {
	w, err = assistantmemory.Validate(w)
	if err != nil {
		return m, err
	}
	if goal == "" {
		return m, assistantmemory.ErrInvalid
	}
	err = r.within(ctx, user, func(tx context.Context) error { return r.save(tx, user, goal, id, w, nil, "", &m) })
	return
}
func (r *AssistantMemoryRepository) save(ctx context.Context, user uuid.UUID, goal string, id *uuid.UUID, w assistantmemory.Write, source *uuid.UUID, quote string, out *assistantmemory.Memory) error {
	scope := assistantmemory.Scope(w.Key, goal)
	var existing assistantmemory.Memory
	err := r.db.QueryRowContext(ctx, `SELECT id,revision,content_hash,status FROM agent_memories WHERE user_id=$1 AND scope=$2 AND memory_key=$3 FOR UPDATE`, user, scope, w.Key).Scan(&existing.ID, &existing.Revision, &existing.Hash, &existing.Status)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if id != nil && *id != existing.ID {
		return assistantmemory.ErrNotFound
	}
	if existing.Revision != w.Revision || existing.Hash != w.Hash {
		return assistantmemory.ErrConflict
	}
	memoryID := existing.ID
	if memoryID == uuid.Nil {
		memoryID = uuid.New()
	}
	revision := existing.Revision + 1
	digest := assistantmemory.Hash(struct {
		Scope, Key string
		Value      json.RawMessage
		Revision   int64
	}{scope, w.Key, w.Value, revision})
	err = r.db.QueryRowContext(ctx, `INSERT INTO agent_memories(id,user_id,scope,memory_key,value,revision,content_hash,source_message_id,source_quote) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)
 ON CONFLICT(user_id,scope,memory_key) DO UPDATE SET value=EXCLUDED.value,revision=EXCLUDED.revision,content_hash=EXCLUDED.content_hash,status='active',source_message_id=EXCLUDED.source_message_id,source_quote=EXCLUDED.source_quote,updated_at=now()
 RETURNING id,scope,memory_key,value,revision,content_hash,status,source_message_id,source_quote,updated_at`, memoryID, user, scope, w.Key, []byte(w.Value), revision, digest, source, quote).Scan(&out.ID, &out.Scope, &out.Key, &out.Value, &out.Revision, &out.Hash, &out.Status, &out.SourceMessageID, &out.SourceQuote, &out.UpdatedAt)
	if err != nil {
		return err
	}
	if _, err = r.db.ExecContext(ctx, `INSERT INTO agent_memory_revisions(memory_id,revision,value,status) VALUES($1,$2,$3,'active')`, out.ID, revision, []byte(out.Value)); err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO agent_context_scopes(user_id,scope,revision) VALUES($1,$2,1) ON CONFLICT(user_id,scope) DO UPDATE SET revision=agent_context_scopes.revision+1`, user, scope)
	return err
}
func (r *AssistantMemoryRepository) Forget(ctx context.Context, user uuid.UUID, goal string, id uuid.UUID, revision int64, digest string) error {
	return r.within(ctx, user, func(tx context.Context) error {
		var scope string
		err := r.db.QueryRowContext(tx, `UPDATE agent_memories SET status='forgotten',value='null',source_message_id=NULL,source_quote='',revision=revision+1,content_hash='',updated_at=now() WHERE id=$1 AND user_id=$2 AND scope IN('global',$3) AND revision=$4 AND content_hash=$5 AND status='active' RETURNING scope`, id, user, goal, revision, digest).Scan(&scope)
		if errors.Is(err, sql.ErrNoRows) {
			return assistantmemory.ErrConflict
		}
		if err != nil {
			return err
		}
		// Retain only a tombstone, never a copy of the forgotten value in revisions.
		if _, err = r.db.ExecContext(tx, `UPDATE agent_memory_revisions SET value='null',status='forgotten' WHERE memory_id=$1`, id); err != nil {
			return err
		}
		if _, err = r.db.ExecContext(tx, `INSERT INTO agent_context_scopes(user_id,scope,revision,epoch) VALUES($1,$2,1,1) ON CONFLICT(user_id,scope) DO UPDATE SET revision=agent_context_scopes.revision+1,epoch=agent_context_scopes.epoch+1`, user, scope); err != nil {
			return err
		}
		filter := `user_id=$1 AND ($2='global' OR goal_key=$2)`
		if _, err = r.db.ExecContext(tx, `UPDATE agent_memory_proposals SET status='stale' WHERE status='pending' AND `+filter, user, scope); err != nil {
			return err
		}
		if _, err = r.db.ExecContext(tx, `DELETE FROM agent_context_artifacts WHERE `+filter, user, scope); err != nil {
			return err
		}
		// Pending actions have not been authorized. Accepted external requests are
		// deliberately untouched and remain recoverable by their durable receipt.
		if _, err = r.db.ExecContext(tx, `UPDATE agent_actions SET status='stale',error_code='context_forgotten' WHERE status='pending' AND conversation_id IN(SELECT conversation_id FROM agent_turns WHERE `+filter+`)`, user, scope); err != nil {
			return err
		}
		_, err = r.db.ExecContext(tx, `UPDATE agent_conversations c SET status='idle' WHERE c.status='awaiting_confirmation' AND NOT EXISTS(SELECT 1 FROM agent_actions a WHERE a.conversation_id=c.id AND a.status='pending') AND c.user_id=$1`, user)
		return err
	})
}
func (r *AssistantMemoryRepository) Propose(ctx context.Context, user uuid.UUID, goal string, p assistantmemory.Proposal) (out assistantmemory.Proposal, err error) {
	p.Write, err = assistantmemory.Validate(p.Write)
	if err != nil {
		return out, err
	}
	if p.SourceQuote == "" || len([]rune(p.SourceQuote)) > 500 {
		return out, assistantmemory.ErrInvalid
	}
	err = r.within(ctx, user, func(tx context.Context) error {
		var text string
		var epoch assistantmemory.Epoch
		e := r.db.QueryRowContext(tx, `SELECT m.content,t.global_epoch,t.goal_epoch FROM agent_messages m JOIN agent_turns t ON m.turn_id=t.id JOIN agent_conversations c ON t.conversation_id=c.id
   WHERE m.id=$1 AND t.user_message_id=m.id AND t.conversation_id=$2 AND t.user_id=$3 AND t.goal_key=$4 AND t.status='running' AND c.status='running' AND c.run_token=t.run_token AND c.run_expires_at>now()`, p.SourceMessageID, p.ConversationID, user, goal).Scan(&text, &epoch.Global, &epoch.Goal)
		if errors.Is(e, sql.ErrNoRows) {
			return assistantmemory.ErrConflict
		}
		if e != nil {
			return e
		}
		if !strings.Contains(text, p.SourceQuote) || strings.Contains(p.SourceQuote, "这次") || strings.Contains(p.SourceQuote, "本次") {
			return assistantmemory.ErrInvalid
		}
		snap, e := r.Snapshot(tx, user, goal)
		if e != nil {
			return e
		}
		if snap.Epoch != epoch {
			return assistantmemory.ErrConflict
		}
		p.Scope = assistantmemory.Scope(p.Key, goal)
		p.Epoch = epoch
		p.Status = "pending"
		for _, m := range snap.Memories {
			if m.Scope == p.Scope && m.Key == p.Key {
				p.Revision = m.Revision
				p.Hash = m.Hash
				p.Before = m.Value
				if string(m.Value) == string(p.Value) {
					return assistantmemory.ErrConflict
				}
			}
		}
		// Include forgotten revisions too, so a genuinely new user statement can
		// explicitly restore a preference without bypassing its tombstone.
		if p.Revision == 0 {
			e = r.db.QueryRowContext(tx, `SELECT revision,content_hash FROM agent_memories WHERE user_id=$1 AND scope=$2 AND memory_key=$3`, user, p.Scope, p.Key).Scan(&p.Revision, &p.Hash)
			if e != nil && !errors.Is(e, sql.ErrNoRows) {
				return e
			}
		}
		var count int
		if e = r.db.QueryRowContext(tx, `SELECT count(*) FROM agent_memory_proposals WHERE source_message_id=$1`, p.SourceMessageID).Scan(&count); e != nil {
			return e
		}
		if count >= 3 {
			return assistantmemory.ErrInvalid
		}
		var duplicate bool
		if e = r.db.QueryRowContext(tx, `SELECT EXISTS(SELECT 1 FROM agent_memory_proposals WHERE source_message_id=$1 AND memory_key=$2 AND value=$3::jsonb)`, p.SourceMessageID, p.Key, []byte(p.Value)).Scan(&duplicate); e != nil {
			return e
		}
		if duplicate {
			return assistantmemory.ErrConflict
		}
		if _, e = r.db.ExecContext(tx, `UPDATE agent_memory_proposals SET status='stale' WHERE user_id=$1 AND scope=$2 AND memory_key=$3 AND status='pending'`, user, p.Scope, p.Key); e != nil {
			return e
		}
		p.ID = uuid.New()
		p.ContentHash = assistantmemory.Hash(struct {
			ID    uuid.UUID
			Write assistantmemory.Write
			Epoch assistantmemory.Epoch
			Quote string
		}{p.ID, p.Write, p.Epoch, p.SourceQuote})
		_, e = r.db.ExecContext(tx, `INSERT INTO agent_memory_proposals(id,user_id,goal_key,scope,conversation_id,source_message_id,source_quote,memory_key,value,before_value,expected_revision,expected_hash,content_hash,global_epoch,goal_epoch) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`, p.ID, user, goal, p.Scope, p.ConversationID, p.SourceMessageID, p.SourceQuote, p.Key, []byte(p.Value), nullableJSON(p.Before), p.Revision, p.Hash, p.ContentHash, p.Epoch.Global, p.Epoch.Goal)
		out = p
		return e
	})
	return
}
func nullableJSON(v json.RawMessage) any {
	if len(v) == 0 {
		return nil
	}
	return []byte(v)
}

const proposalColumns = `id,conversation_id,source_message_id,source_quote,scope,memory_key,value,before_value,expected_revision,expected_hash,content_hash,global_epoch,goal_epoch,status,result`

func scanProposal(row interface{ Scan(...any) error }) (p assistantmemory.Proposal, err error) {
	var before, result []byte
	err = row.Scan(&p.ID, &p.ConversationID, &p.SourceMessageID, &p.SourceQuote, &p.Scope, &p.Key, &p.Value, &before, &p.Revision, &p.Hash, &p.ContentHash, &p.Epoch.Global, &p.Epoch.Goal, &p.Status, &result)
	p.Before = before
	if len(result) > 0 {
		err = errors.Join(err, json.Unmarshal(result, &p.Result))
	}
	return
}
func (r *AssistantMemoryRepository) Proposals(ctx context.Context, user uuid.UUID, goal string) ([]assistantmemory.Proposal, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+proposalColumns+` FROM agent_memory_proposals WHERE user_id=$1 AND goal_key=$2 AND status='pending' ORDER BY created_at,id LIMIT 30`, user, goal)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []assistantmemory.Proposal{}
	for rows.Next() {
		p, e := scanProposal(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (r *AssistantMemoryRepository) Resolve(ctx context.Context, user uuid.UUID, goal string, id uuid.UUID, approve bool, digest string) (out assistantmemory.Proposal, err error) {
	return r.ResolveEdited(ctx, user, goal, id, approve, digest, nil)
}
func (r *AssistantMemoryRepository) ResolveEdited(ctx context.Context, user uuid.UUID, goal string, id uuid.UUID, approve bool, digest string, edited json.RawMessage) (out assistantmemory.Proposal, err error) {
	err = r.within(ctx, user, func(tx context.Context) error {
		p, e := scanProposal(r.db.QueryRowContext(tx, `SELECT `+proposalColumns+` FROM agent_memory_proposals WHERE id=$1 AND user_id=$2 AND goal_key=$3 FOR UPDATE`, id, user, goal))
		if errors.Is(e, sql.ErrNoRows) {
			return assistantmemory.ErrNotFound
		}
		if e != nil {
			return e
		}
		if p.ContentHash != digest {
			return assistantmemory.ErrConflict
		}
		if len(edited) > 0 {
			w, e := assistantmemory.Validate(assistantmemory.Write{Key: p.Key, Value: edited})
			if e != nil {
				return e
			}
			if p.Status == "accepted" && p.Result != nil && string(p.Result.Value) != string(w.Value) {
				return assistantmemory.ErrConflict
			}
			p.Value = w.Value
		}
		if p.Status != "pending" {
			if (approve && p.Status == "accepted") || (!approve && p.Status == "rejected") {
				out = p
				return nil
			}
			return assistantmemory.ErrConflict
		}
		snap, e := r.Snapshot(tx, user, goal)
		if e != nil {
			return e
		}
		if snap.Epoch != p.Epoch {
			return assistantmemory.ErrConflict
		}
		p.Status = "rejected"
		if approve {
			var m assistantmemory.Memory
			if e = r.save(tx, user, goal, nil, p.Write, &p.SourceMessageID, p.SourceQuote, &m); e != nil {
				return e
			}
			p.Result = &m
			p.Status = "accepted"
		}
		b, _ := json.Marshal(p.Result)
		_, e = r.db.ExecContext(tx, `UPDATE agent_memory_proposals SET status=$2,result=$3 WHERE id=$1`, id, p.Status, b)
		out = p
		return e
	})
	return
}
