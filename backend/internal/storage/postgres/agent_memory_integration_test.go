package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/LeoninCS/jobpilot-next/backend/internal/agent"
	"github.com/LeoninCS/jobpilot-next/backend/internal/agentcontext"
	"github.com/LeoninCS/jobpilot-next/backend/internal/assistantmemory"
	"github.com/google/uuid"
)

func TestAgentMemoryLifecycleAndForgetting(t *testing.T) {
	db := agentTransactionDatabase(t)
	ctx := context.Background()
	user := uuid.New()
	goal := "goal-a"
	memory := NewAssistantMemoryRepository(db)
	repo := NewAgentRepository(db)
	contexts := NewAgentContextRepository(db)
	conv, e := repo.Create(ctx, user, "legacy", "test")
	if e != nil {
		t.Fatal(e)
	}
	token, e := repo.BeginRun(ctx, user, "legacy", conv.ID)
	if e != nil {
		t.Fatal(e)
	}
	prefs, e := memory.Snapshot(ctx, user, goal)
	if e != nil {
		t.Fatal(e)
	}
	turn, e := contexts.Start(ctx, agentcontext.Turn{UserID: user, ConversationID: conv.ID, Token: token, Goal: goal, Epoch: prefs.Epoch, PreferencesHash: assistantmemory.Fingerprint(prefs)}, "以后项目优先 Go，避开商城")
	if e != nil {
		t.Fatal(e)
	}
	p, e := memory.Propose(ctx, user, goal, assistantmemory.Proposal{ConversationID: conv.ID, SourceMessageID: turn.UserMessageID, SourceQuote: "以后项目优先 Go", Write: assistantmemory.Write{Key: assistantmemory.Languages, Value: json.RawMessage(`["Go"]`)}})
	if e != nil {
		t.Fatal(e)
	}
	pending, _ := memory.Snapshot(ctx, user, goal)
	if len(pending.Memories) != 0 {
		t.Fatal("unconfirmed proposal became memory")
	}
	accepted, e := memory.Resolve(ctx, user, goal, p.ID, true, p.ContentHash)
	if e != nil {
		t.Fatal(e)
	}
	duplicate, e := memory.Resolve(ctx, user, goal, p.ID, true, p.ContentHash)
	if e != nil || duplicate.Result.ID != accepted.Result.ID {
		t.Fatal("confirmation is not idempotent", e)
	}
	if _, e = memory.Save(ctx, user, goal, &accepted.Result.ID, assistantmemory.Write{Key: assistantmemory.Languages, Value: json.RawMessage(`["Python"]`)}); !errors.Is(e, assistantmemory.ErrConflict) {
		t.Fatal("stale revision accepted", e)
	}
	if e = contexts.Finish(ctx, turn, "completed", map[string]int64{}); e != nil {
		t.Fatal(e)
	}
	_ = repo.EndRun(ctx, conv.ID, token, false)
	current, _ := memory.Snapshot(ctx, user, goal)
	if len(current.Memories) != 1 {
		t.Fatal("memory missing")
	}
	other, _ := memory.Snapshot(ctx, uuid.New(), goal)
	otherGoal, _ := memory.Snapshot(ctx, user, "goal-b")
	if len(other.Memories) != 0 || len(otherGoal.Memories) != 0 {
		t.Fatal("memory scope leak")
	}
	if e = memory.Forget(ctx, user, goal, current.Memories[0].ID, current.Memories[0].Revision, current.Memories[0].Hash); e != nil {
		t.Fatal(e)
	}
	forgotten, _ := memory.Snapshot(ctx, user, goal)
	if len(forgotten.Memories) != 0 || forgotten.Epoch.Goal != 1 {
		t.Fatal("forget did not advance epoch")
	}
	token, e = repo.BeginRun(ctx, user, "legacy", conv.ID)
	if e != nil {
		t.Fatal(e)
	}
	next, e := contexts.Start(ctx, agentcontext.Turn{UserID: user, ConversationID: conv.ID, Token: token, Goal: goal, Epoch: forgotten.Epoch, PreferencesHash: assistantmemory.Fingerprint(forgotten)}, "现在再聊聊")
	if e != nil {
		t.Fatal(e)
	}
	history, e := contexts.History(ctx, next, 0)
	if e != nil || len(history) != 1 || history[0].Content != "现在再聊聊" {
		t.Fatal("forgotten old context resurrected", history, e)
	}
	visible, e := repo.Get(ctx, user, "legacy", conv.ID)
	if e != nil || len(visible.Messages) < 2 {
		t.Fatal("forget deleted visible history", e)
	}
	if e = contexts.Publish(ctx, turn, agentcontext.Summary{Covered: 1, Dependencies: map[string]int64{}, Items: []agentcontext.Item{}}, 0, "test"); !errors.Is(e, agentcontext.ErrChanged) {
		t.Fatal("old worker published summary", e)
	}
	if e = contexts.Finish(ctx, next, "completed", map[string]int64{}); e != nil {
		t.Fatal(e)
	}
	_ = repo.EndRun(ctx, conv.ID, token, false)
	if e = repo.Delete(ctx, user, "legacy", conv.ID); e != nil {
		t.Fatal(e)
	}
}
func TestAgentContextHistoryArtifactsAndCAS(t *testing.T) {
	db := agentTransactionDatabase(t)
	ctx := context.Background()
	user := uuid.New()
	repo := NewAgentRepository(db)
	memory := NewAssistantMemoryRepository(db)
	r := NewAgentContextRepository(db)
	conv, e := repo.Create(ctx, user, "scope", "test")
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 230; i++ {
		if _, e = repo.AddMessage(ctx, conv.ID, "user", fmt.Sprintf("历史 %d", i)); e != nil {
			t.Fatal(e)
		}
	}
	token, e := repo.BeginRun(ctx, user, "scope", conv.ID)
	if e != nil {
		t.Fatal(e)
	}
	prefs, _ := memory.Snapshot(ctx, user, "goal")
	turn, e := r.Start(ctx, agentcontext.Turn{UserID: user, ConversationID: conv.ID, Token: token, Goal: "goal", Epoch: prefs.Epoch, PreferencesHash: assistantmemory.Fingerprint(prefs)}, "现在的问题")
	if e != nil {
		t.Fatal(e)
	}
	history, e := r.History(ctx, turn, 0)
	if e != nil || len(history) != 231 {
		t.Fatalf("history still limited to 200: %d %v", len(history), e)
	}
	summary := agentcontext.Summary{Covered: 100, Items: []agentcontext.Item{{Kind: "discussion", Text: "仍在比较", State: "discussed", Sources: []uuid.UUID{history[0].ID}}}, Dependencies: map[string]int64{}}
	if e = r.Publish(ctx, turn, summary, 0, "test"); e != nil {
		t.Fatal(e)
	}
	summary.Covered = 110
	summary.ExpectedCovered = 100
	if e = r.Publish(ctx, turn, summary, 1, "test"); e != nil {
		t.Fatal(e)
	}
	if e = r.Publish(ctx, turn, summary, 1, "test"); !errors.Is(e, agentcontext.ErrChanged) {
		t.Fatal("stale summary won CAS", e)
	}
	id, e := r.StoreArtifact(ctx, turn, "完整工具结果", map[string]int64{}, time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	artifact, e := r.ReadArtifact(ctx, turn, id, 0)
	if e != nil || artifact.Text != "完整工具结果" {
		t.Fatal(artifact, e)
	}
	deps, e := r.Versions(ctx, user, []string{"profile"})
	if e != nil {
		t.Fatal(e)
	}
	versioned, e := r.StoreArtifact(ctx, turn, "相同正文，但版本已往返变化", deps, time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`SELECT bump_resource_version($1,'profile'),bump_resource_version($1,'profile')`, user); e != nil {
		t.Fatal(e)
	}
	if _, e = r.ReadArtifact(ctx, turn, versioned, 0); !errors.Is(e, agentcontext.ErrArtifact) {
		t.Fatal("ABA restored an old artifact", e)
	}
	wrong := turn
	wrong.UserID = uuid.New()
	if _, e = r.ReadArtifact(ctx, wrong, id, 0); !errors.Is(e, agentcontext.ErrArtifact) {
		t.Fatal("artifact account leak", e)
	}
	_, e = db.Exec(`UPDATE agent_context_artifacts SET expires_at=now()-interval '1 second' WHERE id=$1`, id)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = r.ReadArtifact(ctx, turn, id, 0); !errors.Is(e, agentcontext.ErrArtifact) {
		t.Fatal("expired artifact accessible", e)
	}
	stale := turn
	stale.Token = uuid.New()
	ctx = agentcontext.WithState(ctx, agentcontext.NewState(stale))
	if _, e = repo.AddMessage(ctx, conv.ID, "assistant", "旧 Worker"); !errors.Is(e, agentcontext.ErrChanged) {
		t.Fatal("old worker wrote answer", e)
	}
	if _, e = r.Page(context.Background(), uuid.New(), "scope", conv.ID, 0); !errors.Is(e, agent.ErrNotFound) {
		t.Fatal("history account leak", e)
	}
	currentDeps, e := r.Versions(context.Background(), user, []string{"profile"})
	if e != nil {
		t.Fatal(e)
	}
	currentState := agentcontext.NewState(turn)
	currentState.Observe(currentDeps)
	currentCtx := agentcontext.WithState(context.Background(), currentState)
	answer, e := repo.AddMessage(currentCtx, conv.ID, "assistant", "依据当时资料的回答")
	if e != nil {
		t.Fatal(e)
	}
	if e = r.Finish(currentCtx, turn, "completed", currentDeps); e != nil {
		t.Fatal(e)
	}
	visible, e := repo.Get(context.Background(), user, "scope", conv.ID)
	if e != nil {
		t.Fatal(e)
	}
	for _, m := range visible.Messages {
		if m.ID == answer.ID && m.ContextStale {
			t.Fatal("fresh answer marked stale")
		}
	}
	if _, e = db.Exec(`SELECT bump_resource_version($1,'profile')`, user); e != nil {
		t.Fatal(e)
	}
	page, e := r.Page(context.Background(), user, "scope", conv.ID, 0)
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, m := range page.Messages {
		if m.ID == answer.ID {
			found = m.ContextStale
		}
	}
	if !found {
		t.Fatal("old display answer missing version warning")
	}
}

func TestMemoryConcurrentRevisionAndGlobalEpoch(t *testing.T) {
	db := agentTransactionDatabase(t)
	repo := NewAssistantMemoryRepository(db)
	ctx := t.Context()
	user := uuid.New()
	for _, goal := range []string{"goal-a", "goal-b"} {
		if _, e := repo.Save(ctx, user, goal, nil, assistantmemory.Write{Key: assistantmemory.Languages, Value: json.RawMessage(`["Go"]`)}); e != nil {
			t.Fatal(e)
		}
	}
	original, e := repo.Save(ctx, user, "goal-a", nil, assistantmemory.Write{Key: assistantmemory.Answer, Value: json.RawMessage(`"简短"`)})
	if e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	out := make(chan error, 2)
	for _, value := range []string{`"稍详细"`, `"更简短"`} {
		wg.Add(1)
		go func(v string) {
			defer wg.Done()
			_, e := repo.Save(ctx, user, "goal-a", &original.ID, assistantmemory.Write{Key: original.Key, Value: json.RawMessage(v), Revision: original.Revision, Hash: original.Hash})
			out <- e
		}(value)
	}
	wg.Wait()
	close(out)
	ok, conflict := 0, 0
	for e := range out {
		if e == nil {
			ok++
		} else if errors.Is(e, assistantmemory.ErrConflict) {
			conflict++
		} else {
			t.Fatal(e)
		}
	}
	if ok != 1 || conflict != 1 {
		t.Fatal("concurrent writes lost revision protection", ok, conflict)
	}
	snapshot, e := repo.Snapshot(ctx, user, "goal-a")
	if e != nil {
		t.Fatal(e)
	}
	var global assistantmemory.Memory
	for _, m := range snapshot.Memories {
		if m.Key == assistantmemory.Answer {
			global = m
		}
	}
	if e = repo.Forget(ctx, user, "goal-a", global.ID, global.Revision, global.Hash); e != nil {
		t.Fatal(e)
	}
	for _, goal := range []string{"goal-a", "goal-b"} {
		s, e := repo.Snapshot(ctx, user, goal)
		if e != nil || s.Epoch.Global != 1 || len(s.Memories) != 1 || s.Memories[0].Key != assistantmemory.Languages {
			t.Fatal("global forget lost isolation or other preference", s, e)
		}
	}
	s, _ := repo.Snapshot(ctx, user, "goal-b")
	var tomb assistantmemory.Memory
	for _, m := range s.Forgotten {
		if m.Key == assistantmemory.Answer {
			tomb = m
		}
	}
	restored, e := repo.Save(ctx, user, "goal-b", &tomb.ID, assistantmemory.Write{Key: tomb.Key, Value: json.RawMessage(`"详细"`), Revision: tomb.Revision, Hash: tomb.Hash})
	if e != nil || restored.Revision != tomb.Revision+1 {
		t.Fatal("explicit restore did not preserve tombstone revision", e)
	}
}
