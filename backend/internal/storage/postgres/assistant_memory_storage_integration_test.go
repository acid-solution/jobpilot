package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	"github.com/LeoninCS/jobpilot-next/backend/internal/agentcontext"
	"github.com/LeoninCS/jobpilot-next/backend/internal/assistantmemory"
	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
)

func memoryProposalSource(t *testing.T, db *sql.DB, user uuid.UUID, goal string) assistantmemory.Proposal {
	t.Helper()
	ctx := t.Context()
	repo := NewAgentRepository(db)
	conv, err := repo.Create(ctx, user, goal, "preference test")
	if err != nil {
		t.Fatal(err)
	}
	token, err := repo.BeginRun(ctx, user, goal, conv.ID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := NewAssistantMemoryRepository(db).Snapshot(ctx, user, goal)
	if err != nil {
		t.Fatal(err)
	}
	quote := "以后项目优先 Go，也可以使用 Python，偏好模块化单体"
	turn, err := NewAgentContextRepository(db).Start(ctx, agentcontext.Turn{UserID: user, ConversationID: conv.ID, Token: token, Goal: goal, Epoch: snapshot.Epoch, PreferencesHash: assistantmemory.Fingerprint(snapshot)}, quote)
	if err != nil {
		t.Fatal(err)
	}
	return assistantmemory.Proposal{ConversationID: conv.ID, SourceMessageID: turn.UserMessageID, SourceQuote: quote, Write: assistantmemory.Write{Key: assistantmemory.Languages, Value: json.RawMessage(`["Python"]`)}}
}

func assertProposalMinimized(t *testing.T, db *sql.DB, id uuid.UUID, status string) {
	t.Helper()
	var minimized bool
	err := db.QueryRow(`SELECT status=$2 AND value='null'::jsonb AND before_value IS NULL AND source_quote='' AND length(value_fingerprint)=64 AND (result IS NULL OR NOT(result ?| ARRAY['value','source_quote','content_hash'])) FROM agent_memory_proposals WHERE id=$1`, id, status).Scan(&minimized)
	if err != nil || !minimized {
		t.Fatalf("proposal retained preference history or lost receipt: %v, %v", minimized, err)
	}
}

func TestMemoryStoresOnlyCurrentValueAndMinimalReceipts(t *testing.T) {
	db := agentTransactionDatabase(t)
	ctx := t.Context()
	user, goal := uuid.New(), "goal-current-only"
	repo := NewAssistantMemoryRepository(db)
	original, err := repo.Save(ctx, user, goal, nil, assistantmemory.Write{Key: assistantmemory.Languages, Value: json.RawMessage(`["Go"]`)})
	if err != nil {
		t.Fatal(err)
	}
	source := memoryProposalSource(t, db, user, goal)
	p, err := repo.Propose(ctx, user, goal, source)
	if err != nil || string(p.Before) != `["Go"]` {
		t.Fatal("pending preview lost current preference", p, err)
	}
	edited := json.RawMessage(`["Python","Go"]`)
	accepted, err := repo.ResolveEdited(ctx, user, goal, p.ID, true, p.ContentHash, edited)
	if err != nil || accepted.Result == nil || accepted.Result.ID != original.ID || accepted.Result.Revision != 2 || string(accepted.Value) != "null" || len(accepted.Before) != 0 || accepted.SourceQuote != "" {
		t.Fatal("confirmation did not return a minimal receipt", accepted, err)
	}
	assertProposalMinimized(t, db, p.ID, "accepted")
	for _, value := range []json.RawMessage{nil, edited, json.RawMessage(`[ "Python", "Go" ]`)} {
		repeated, err := repo.ResolveEdited(ctx, user, goal, p.ID, true, p.ContentHash, value)
		if err != nil || repeated.Result == nil || *repeated.Result != *accepted.Result || string(repeated.Value) != "null" {
			t.Fatal("confirmation replay lost idempotency", repeated, err)
		}
	}
	if _, err := repo.ResolveEdited(ctx, user, goal, p.ID, true, p.ContentHash, json.RawMessage(`["Rust"]`)); !errors.Is(err, assistantmemory.ErrConflict) {
		t.Fatal("different replay was accepted", err)
	}
	snapshot, err := repo.Snapshot(ctx, user, goal)
	if err != nil || len(snapshot.Memories) != 1 || snapshot.Memories[0].Revision != 2 {
		t.Fatal("replayed confirmation wrote a new revision", snapshot, err)
	}
	current := snapshot.Memories[0]
	if _, err := repo.Save(ctx, user, goal, &original.ID, assistantmemory.Write{Key: original.Key, Value: json.RawMessage(`["Rust"]`), Revision: original.Revision, Hash: original.Hash}); !errors.Is(err, assistantmemory.ErrConflict) {
		t.Fatal("stale revision was accepted", err)
	}
	updated, err := repo.Save(ctx, user, goal, &current.ID, assistantmemory.Write{Key: current.Key, Value: json.RawMessage(`["Rust"]`), Revision: current.Revision, Hash: current.Hash})
	if err != nil || updated.Revision != 3 {
		t.Fatal(updated, err)
	}
	// Replaying an older receipt must never restore its historical value.
	if _, err := repo.ResolveEdited(ctx, user, goal, p.ID, true, p.ContentHash, edited); err != nil {
		t.Fatal(err)
	}
	snapshot, err = repo.Snapshot(ctx, user, goal)
	if err != nil || string(snapshot.Memories[0].Value) != `["Rust"]` || snapshot.Memories[0].Revision != 3 {
		t.Fatal("old receipt restored a value", snapshot, err)
	}
	if err := repo.Forget(ctx, user, goal, updated.ID, updated.Revision, updated.Hash); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Resolve(ctx, user, goal, p.ID, true, p.ContentHash); err != nil {
		t.Fatal(err)
	}
	snapshot, err = repo.Snapshot(ctx, user, goal)
	if err != nil || len(snapshot.Memories) != 0 || len(snapshot.Forgotten) != 1 || snapshot.Epoch.Goal != 1 || snapshot.Forgotten[0].Revision != 4 || string(snapshot.Forgotten[0].Value) != "null" || snapshot.Forgotten[0].SourceQuote != "" {
		t.Fatal("forget lost its minimal tombstone", snapshot, err)
	}
	var removed bool
	if err := db.QueryRow(`SELECT to_regclass('public.agent_memory_revisions') IS NULL`).Scan(&removed); err != nil || !removed {
		t.Fatal("revision history table still exists", err)
	}
}

func TestMemoryTerminalSuggestionsDiscardValuesButKeepDedupe(t *testing.T) {
	db := agentTransactionDatabase(t)
	ctx := t.Context()
	user, goal := uuid.New(), "goal-suggestion-cleanup"
	repo := NewAssistantMemoryRepository(db)
	source := memoryProposalSource(t, db, user, goal)
	rejected, err := repo.Propose(ctx, user, goal, source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.Resolve(ctx, user, goal, rejected.ID, false, rejected.ContentHash); err != nil {
		t.Fatal(err)
	}
	assertProposalMinimized(t, db, rejected.ID, "rejected")
	if _, err = repo.Resolve(ctx, user, goal, rejected.ID, false, rejected.ContentHash); err != nil {
		t.Fatal("rejection replay failed", err)
	}
	if _, err = repo.Propose(ctx, user, goal, source); !errors.Is(err, assistantmemory.ErrConflict) {
		t.Fatal("discarded suggestion was proposed again from same source", err)
	}
	// A genuinely new source can propose the same preference again.
	newSource := memoryProposalSource(t, db, user, goal)
	stale, err := repo.Propose(ctx, user, goal, newSource)
	if err != nil {
		t.Fatal(err)
	}
	newSource.Value = json.RawMessage(`["Go"]`)
	replacement, err := repo.Propose(ctx, user, goal, newSource)
	if err != nil {
		t.Fatal(err)
	}
	assertProposalMinimized(t, db, stale.ID, "stale")
	// A direct panel save invalidates and clears a pending preview atomically.
	saved, err := repo.Save(ctx, user, goal, nil, assistantmemory.Write{Key: assistantmemory.Languages, Value: json.RawMessage(`["Rust"]`)})
	if err != nil {
		t.Fatal(err)
	}
	assertProposalMinimized(t, db, replacement.ID, "stale")
	if _, err = repo.Resolve(ctx, user, goal, replacement.ID, true, replacement.ContentHash); !errors.Is(err, assistantmemory.ErrConflict) {
		t.Fatal("superseded preview was accepted", err)
	}
	newSource = memoryProposalSource(t, db, user, goal)
	forgotten, err := repo.Propose(ctx, user, goal, newSource)
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.Forget(ctx, user, goal, saved.ID, saved.Revision, saved.Hash); err != nil {
		t.Fatal(err)
	}
	assertProposalMinimized(t, db, forgotten.ID, "stale")
}

func TestMemoryHistoryRemovalMigration(t *testing.T) {
	db := agentTransactionDatabase(t)
	ctx := context.Background()
	if err := goose.DownTo(db, "sql", 41); err != nil {
		t.Fatal(err)
	}
	user, goal := uuid.New(), "goal-migration"
	agents := NewAgentRepository(db)
	conv, err := agents.Create(ctx, user, goal, "migration fixture")
	if err != nil {
		t.Fatal(err)
	}
	message, err := agents.AddMessage(ctx, conv.ID, "user", "以后项目优先 Go，也可以使用 Python")
	if err != nil {
		t.Fatal(err)
	}
	memoryID, accepted, rejected, pending, stale := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO agent_memories(id,user_id,scope,memory_key,value,revision,content_hash,source_message_id,source_quote) VALUES($1,$2,$3,'project.languages','["Python"]',2,'current-hash',$4,'当前来源')`, memoryID, user, goal, message.ID)
	exec(`INSERT INTO agent_context_scopes(user_id,scope,revision,epoch) VALUES($1,$2,7,4)`, user, goal)
	exec(`INSERT INTO agent_memory_revisions(memory_id,revision,value,status) VALUES($1,1,'["Go"]','active'),($1,2,'["Python"]','active')`, memoryID)
	for _, fixture := range []struct {
		id          uuid.UUID
		status, key string
		revision    int64
		hash        string
	}{
		{accepted, "accepted", assistantmemory.Languages, 0, ""},
		{rejected, "rejected", assistantmemory.Architecture, 0, ""},
		{pending, "pending", assistantmemory.Languages, 2, "current-hash"},
		{stale, "pending", assistantmemory.Architecture, 3, "superseded-hash"},
	} {
		exec(`INSERT INTO agent_memory_proposals(id,user_id,goal_key,scope,conversation_id,source_message_id,source_quote,memory_key,value,before_value,expected_revision,expected_hash,content_hash,global_epoch,goal_epoch,status,result) VALUES($1,$2,$3,$3,$4,$5,'旧来源',$6,'["Go"]','["Python"]',$7,$8,'proposal-hash',0,4,$9,CASE WHEN $9='accepted' THEN jsonb_build_object('id',$10::text,'revision',1,'value','["Go"]'::jsonb,'source_quote','旧来源') ELSE NULL END)`, fixture.id, user, goal, conv.ID, message.ID, fixture.key, fixture.revision, fixture.hash, fixture.status, memoryID)
	}
	if err := goose.Up(db, "sql"); err != nil {
		t.Fatal(err)
	}
	assertProposalMinimized(t, db, accepted, "accepted")
	assertProposalMinimized(t, db, rejected, "rejected")
	assertProposalMinimized(t, db, stale, "stale")
	var preserved bool
	if err := db.QueryRow(`SELECT value='["Go"]'::jsonb AND before_value='["Python"]'::jsonb AND source_quote='旧来源' FROM agent_memory_proposals WHERE id=$1`, pending).Scan(&preserved); err != nil || !preserved {
		t.Fatal("migration discarded an actionable pending preview", err)
	}
	repo := NewAssistantMemoryRepository(db)
	snapshot, err := repo.Snapshot(ctx, user, goal)
	if err != nil || len(snapshot.Memories) != 1 || snapshot.Memories[0].Revision != 2 || snapshot.Memories[0].Hash != "current-hash" || snapshot.Memories[0].SourceQuote != "当前来源" || string(snapshot.Memories[0].Value) != `["Python"]` || snapshot.Epoch.Goal != 4 {
		t.Fatal("migration changed current preference or forgetting guards", snapshot, err)
	}
	replayed, err := repo.ResolveEdited(ctx, user, goal, accepted, true, "proposal-hash", json.RawMessage(`["Go"]`))
	if err != nil || replayed.Result == nil || replayed.Result.ID != memoryID || replayed.Result.Revision != 1 {
		t.Fatal("migrated receipt cannot be replayed", replayed, err)
	}
	var retained bool
	if err := db.QueryRow(`SELECT to_regclass('public.agent_memory_revisions') IS NULL AND EXISTS(SELECT 1 FROM agent_messages WHERE id=$1 AND content='以后项目优先 Go，也可以使用 Python')`, message.ID).Scan(&retained); err != nil || !retained {
		t.Fatal("migration kept history table or removed chat", err)
	}
}
