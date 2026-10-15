package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"maps"
	"time"

	"github.com/LeoninCS/jobpilot-next/backend/internal/agent"
	"github.com/LeoninCS/jobpilot-next/backend/internal/agentcontext"
	"github.com/LeoninCS/jobpilot-next/backend/internal/assistantmemory"
	"github.com/google/uuid"
)

type AgentContextRepository struct{ *AssistantMemoryRepository }

func (r *AgentContextRepository) Page(ctx context.Context, user uuid.UUID, scope string, id uuid.UUID, before int64) (agent.MessagePage, error) {
	var exists bool
	if e := r.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM agent_conversations WHERE id=$1 AND user_id=$2 AND goal_signature=$3)`, id, user, scope).Scan(&exists); e != nil {
		return agent.MessagePage{}, e
	}
	if !exists {
		return agent.MessagePage{}, agent.ErrNotFound
	}
	rows, e := r.db.QueryContext(ctx, `SELECT m.id,m.role,m.content,m.sequence,m.kind,m.created_at,`+agentMessageStaleSQL+` FROM agent_messages m LEFT JOIN agent_turns t ON m.turn_id=t.id WHERE m.conversation_id=$1 AND ($2=0 OR m.sequence<$2) ORDER BY m.sequence DESC LIMIT 101`, id, before)
	if e != nil {
		return agent.MessagePage{}, e
	}
	defer rows.Close()
	out := agent.MessagePage{Messages: []agentcontext.Message{}}
	for rows.Next() {
		var m agentcontext.Message
		if e = rows.Scan(&m.ID, &m.Role, &m.Content, &m.Sequence, &m.Kind, &m.CreatedAt, &m.ContextStale); e != nil {
			return out, e
		}
		out.Messages = append(out.Messages, m)
	}
	if len(out.Messages) > 100 {
		out.Messages = out.Messages[:100]
		n := out.Messages[99].Sequence
		out.Next = &n
	}
	for i, j := 0, len(out.Messages)-1; i < j; i, j = i+1, j-1 {
		out.Messages[i], out.Messages[j] = out.Messages[j], out.Messages[i]
	}
	return out, rows.Err()
}

func NewAgentContextRepository(db *sql.DB) *AgentContextRepository {
	return &AgentContextRepository{NewAssistantMemoryRepository(db)}
}
func (r *AgentContextRepository) Versions(ctx context.Context, user uuid.UUID, keys []string) (map[string]int64, error) {
	return r.ReadResourceVersions(ctx, user, keys)
}
func (r *AgentContextRepository) valid(ctx context.Context, t agentcontext.Turn, checkPreferences bool) error {
	var held bool
	e := r.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM agent_conversations WHERE id=$1 AND user_id=$2 AND status='running' AND run_token=$3 AND run_expires_at>now() FOR UPDATE)`, t.ConversationID, t.UserID, t.Token).Scan(&held)
	if e != nil {
		return e
	}
	if !held {
		return agentcontext.ErrChanged
	}
	s, e := r.Snapshot(ctx, t.UserID, t.Goal)
	if e != nil {
		return e
	}
	if s.Epoch != t.Epoch || (checkPreferences && assistantmemory.Fingerprint(s) != t.PreferencesHash) {
		return agentcontext.ErrChanged
	}
	return nil
}
func (r *AgentContextRepository) Start(ctx context.Context, t agentcontext.Turn, text string) (out agentcontext.Turn, err error) {
	err = r.within(ctx, t.UserID, func(tx context.Context) error {
		if e := r.valid(tx, t, true); e != nil {
			return e
		}
		if _, e := r.db.ExecContext(tx, `UPDATE agent_turns SET status='failed',finished_at=now() WHERE conversation_id=$1 AND status='running'`, t.ConversationID); e != nil {
			return e
		}
		t.ID = uuid.New()
		t.Status = "running"
		if _, e := r.db.ExecContext(tx, `UPDATE agent_conversations SET title=CASE WHEN title='新对话' THEN left($2,24) ELSE title END,updated_at=now() WHERE id=$1`, t.ConversationID, text); e != nil {
			return e
		}
		if _, e := r.db.ExecContext(tx, `INSERT INTO agent_turns(id,conversation_id,user_id,goal_key,run_token,global_epoch,goal_epoch,preferences_hash) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, t.ID, t.ConversationID, t.UserID, t.Goal, t.Token, t.Epoch.Global, t.Epoch.Goal, t.PreferencesHash); e != nil {
			return e
		}
		if e := r.db.QueryRowContext(tx, `INSERT INTO agent_messages(conversation_id,role,content,kind,turn_id) VALUES($1,'user',$2,'normal',$3) RETURNING id`, t.ConversationID, text, t.ID).Scan(&t.UserMessageID); e != nil {
			return e
		}
		_, e := r.db.ExecContext(tx, `UPDATE agent_turns SET user_message_id=$2 WHERE id=$1`, t.ID, t.UserMessageID)
		out = t
		return e
	})
	return
}
func (r *AgentContextRepository) Resume(ctx context.Context, t agentcontext.Turn) (out agentcontext.Turn, err error) {
	err = r.within(ctx, t.UserID, func(tx context.Context) error {
		oldToken := t.Token
		var storedDeps []byte
		e := r.db.QueryRowContext(tx, `SELECT id,user_message_id,global_epoch,goal_epoch,preferences_hash,dependencies FROM agent_turns WHERE conversation_id=$1 AND user_id=$2 AND goal_key=$3 AND status='awaiting_confirmation' ORDER BY started_at DESC LIMIT 1`, t.ConversationID, t.UserID, t.Goal).Scan(&t.ID, &t.UserMessageID, &t.Epoch.Global, &t.Epoch.Goal, &t.PreferencesHash, &storedDeps)
		if e == nil {
			e = json.Unmarshal(storedDeps, &t.Dependencies)
		}
		if errors.Is(e, sql.ErrNoRows) {
			return agentcontext.ErrChanged
		}
		if e != nil {
			return e
		}
		t.Token = oldToken
		if e = r.valid(tx, t, false); e != nil {
			return e
		}
		_, e = r.db.ExecContext(tx, `UPDATE agent_turns SET status='running',run_token=$2 WHERE id=$1`, t.ID, t.Token)
		out = t
		return e
	})
	return
}
func (r *AgentContextRepository) currentVersions(ctx context.Context, user uuid.UUID, encoded []byte) bool {
	var expected map[string]int64
	if json.Unmarshal(encoded, &expected) != nil {
		return false
	}
	keys := make([]string, 0, len(expected))
	for k := range expected {
		keys = append(keys, k)
	}
	actual, e := r.Versions(ctx, user, keys)
	return e == nil && maps.Equal(actual, expected)
}
func (r *AgentContextRepository) History(ctx context.Context, t agentcontext.Turn, after int64) ([]agentcontext.Message, error) {
	rows, e := r.db.QueryContext(ctx, `SELECT m.id,m.role,m.content,m.sequence,m.turn_id,m.kind,m.created_at,COALESCE(t.global_epoch,-1),COALESCE(t.goal_epoch,-1),COALESCE(t.status,''),COALESCE(t.dependencies,'{}'),COALESCE(t.preferences_hash,'') FROM agent_messages m LEFT JOIN agent_turns t ON t.id=m.turn_id WHERE m.conversation_id=$1 AND m.sequence>$2 ORDER BY m.sequence`, t.ConversationID, after)
	if e != nil {
		return nil, e
	}
	out := []agentcontext.Message{}
	type saved struct {
		m             agentcontext.Message
		epoch         assistantmemory.Epoch
		status, prefs string
		deps          []byte
	}
	pending := []saved{}
	for rows.Next() {
		var v saved
		if e = rows.Scan(&v.m.ID, &v.m.Role, &v.m.Content, &v.m.Sequence, &v.m.TurnID, &v.m.Kind, &v.m.CreatedAt, &v.epoch.Global, &v.epoch.Goal, &v.status, &v.deps, &v.prefs); e != nil {
			rows.Close()
			return nil, e
		}
		pending = append(pending, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	// Close rows before nested lookups so a one-connection transaction cannot
	// deadlock trying to issue another query on an unread result set.
	for _, v := range pending {
		if v.m.Kind == "notification" || v.m.Role == "tool" {
			continue
		}
		if v.m.TurnID == nil {
			if t.Epoch.Global == 0 && t.Epoch.Goal == 0 && v.m.Role == "user" {
				out = append(out, v.m)
			}
			continue
		}
		if v.epoch != t.Epoch {
			continue
		}
		if v.m.Role == "assistant" && (v.status != "completed" || v.prefs != t.PreferencesHash || !r.currentVersions(ctx, t.UserID, v.deps)) {
			continue
		}
		out = append(out, v.m)
	}
	return out, nil
}
func (r *AgentContextRepository) Summary(ctx context.Context, t agentcontext.Turn) (s agentcontext.Summary, err error) {
	var content, deps []byte
	var epoch assistantmemory.Epoch
	var prefs string
	err = r.db.QueryRowContext(ctx, `SELECT revision,covered_sequence,content,dependencies,global_epoch,goal_epoch,preferences_hash,partial_sequence,partial_offset FROM agent_context_summaries WHERE conversation_id=$1`, t.ConversationID).Scan(&s.Revision, &s.Covered, &content, &deps, &epoch.Global, &epoch.Goal, &prefs, &s.PartialSequence, &s.PartialOffset)
	if errors.Is(err, sql.ErrNoRows) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	s.ExpectedCovered = s.Covered
	if epoch != t.Epoch || prefs != t.PreferencesHash || !r.currentVersions(ctx, t.UserID, deps) {
		s.Covered = 0
		s.PartialSequence = 0
		s.PartialOffset = 0
		return s, nil
	}
	if err = json.Unmarshal(content, &s.Items); err != nil {
		return s, err
	}
	err = json.Unmarshal(deps, &s.Dependencies)
	return
}
func (r *AgentContextRepository) Publish(ctx context.Context, t agentcontext.Turn, s agentcontext.Summary, previous int64, model string) error {
	return r.within(ctx, t.UserID, func(tx context.Context) error {
		if e := r.valid(tx, t, true); e != nil {
			return e
		}
		b, _ := json.Marshal(s.Dependencies)
		if !r.currentVersions(tx, t.UserID, b) {
			return agentcontext.ErrChanged
		}
		// Validate original message IDs independently of the model's JSON output.
		for _, item := range s.Items {
			for _, id := range item.Sources {
				var ok bool
				if e := r.db.QueryRowContext(tx, `SELECT EXISTS(SELECT 1 FROM agent_messages WHERE id=$1 AND conversation_id=$2 AND sequence<=$3)`, id, t.ConversationID, max(s.Covered, s.PartialSequence)).Scan(&ok); e != nil {
					return e
				}
				if !ok {
					return agentcontext.ErrChanged
				}
			}
		}
		content, _ := json.Marshal(s.Items)
		result, e := r.db.ExecContext(tx, `INSERT INTO agent_context_summaries(conversation_id,revision,covered_sequence,global_epoch,goal_epoch,content,dependencies,preferences_hash,schema_version,prompt_version,model,partial_sequence,partial_offset)
   SELECT $1,1,$2,$3,$4,$5,$6,$7,'1','context-summary-v1',$8,$10,$11 WHERE $9=0
   ON CONFLICT(conversation_id) DO NOTHING`, t.ConversationID, s.Covered, t.Epoch.Global, t.Epoch.Goal, content, b, t.PreferencesHash, model, previous, s.PartialSequence, s.PartialOffset)
		// INSERT ... SELECT with a false predicate cannot take the conflict branch.
		if e != nil {
			return e
		}
		n, _ := result.RowsAffected()
		if n == 0 && previous > 0 {
			result, e = r.db.ExecContext(tx, `UPDATE agent_context_summaries SET revision=revision+1,covered_sequence=$2,global_epoch=$3,goal_epoch=$4,content=$5,dependencies=$6,preferences_hash=$7,prompt_version='context-summary-v1',model=$8,partial_sequence=$10,partial_offset=$11,updated_at=now() WHERE conversation_id=$1 AND revision=$9 AND covered_sequence=$12`, t.ConversationID, s.Covered, t.Epoch.Global, t.Epoch.Goal, content, b, t.PreferencesHash, model, previous, s.PartialSequence, s.PartialOffset, s.ExpectedCovered)
			if e != nil {
				return e
			}
			n, _ = result.RowsAffected()
		}
		if n != 1 {
			return agentcontext.ErrChanged
		}
		return nil
	})
}
func (r *AgentContextRepository) Finish(ctx context.Context, t agentcontext.Turn, status string, deps map[string]int64) error {
	b, _ := json.Marshal(deps)
	observations := []byte(`[]`)
	if state := agentcontext.From(ctx); state != nil {
		observations, _ = json.Marshal(state.Observations())
	}
	result, e := r.db.ExecContext(ctx, `UPDATE agent_turns SET status=$3,dependencies=$4,observations=$6,finished_at=now() WHERE id=$1 AND run_token=$2 AND EXISTS(SELECT 1 FROM agent_conversations WHERE id=$5 AND run_token=$2 AND status='running' AND run_expires_at>now() FOR UPDATE)`, t.ID, t.Token, status, b, t.ConversationID, observations)
	if e != nil {
		return e
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return agentcontext.ErrChanged
	}
	return nil
}
func (r *AgentContextRepository) StoreArtifact(ctx context.Context, t agentcontext.Turn, text string, deps map[string]int64, ttl time.Duration) (id uuid.UUID, err error) {
	id = uuid.New()
	err = r.within(ctx, t.UserID, func(tx context.Context) error {
		if e := r.valid(tx, t, false); e != nil {
			return e
		}
		b, _ := json.Marshal(deps)
		if !r.currentVersions(tx, t.UserID, b) {
			return agentcontext.ErrChanged
		}
		_, e := r.db.ExecContext(tx, `INSERT INTO agent_context_artifacts(id,user_id,goal_key,conversation_id,turn_id,global_epoch,goal_epoch,dependencies,content,content_hash,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,now()+$11::interval)`, id, t.UserID, t.Goal, t.ConversationID, t.ID, t.Epoch.Global, t.Epoch.Goal, b, text, assistantmemory.Hash(text), ttl.String())
		return e
	})
	return
}
func (r *AgentContextRepository) ReadArtifact(ctx context.Context, t agentcontext.Turn, id uuid.UUID, offset int) (out agentcontext.Artifact, err error) {
	if offset < 0 {
		return out, agentcontext.ErrArtifact
	}
	var text string
	var deps []byte
	var epoch assistantmemory.Epoch
	err = r.db.QueryRowContext(ctx, `SELECT content,content_hash,dependencies,global_epoch,goal_epoch FROM agent_context_artifacts WHERE id=$1 AND user_id=$2 AND goal_key=$3 AND conversation_id=$4 AND expires_at>now()`, id, t.UserID, t.Goal, t.ConversationID).Scan(&text, &out.Hash, &deps, &epoch.Global, &epoch.Goal)
	if errors.Is(err, sql.ErrNoRows) {
		return out, agentcontext.ErrArtifact
	}
	if err != nil {
		return out, err
	}
	if epoch != t.Epoch || !r.currentVersions(ctx, t.UserID, deps) {
		return out, agentcontext.ErrArtifact
	}
	runes := []rune(text)
	if offset > len(runes) {
		return out, agentcontext.ErrArtifact
	}
	end := min(len(runes), offset+1000)
	out.ID = id
	out.Offset = offset
	out.Text = string(runes[offset:end])
	if end < len(runes) {
		out.Next = &end
	}
	return
}
