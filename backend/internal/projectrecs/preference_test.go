package projectrecs

import (
	"context"
	"encoding/json"
	"github.com/LeoninCS/jobpilot-next/backend/internal/assistantmemory"
	"github.com/LeoninCS/jobpilot-next/backend/internal/target"
	"github.com/google/uuid"
	"testing"
)

type preferenceFixture struct{ s assistantmemory.Snapshot }

func (p *preferenceFixture) Snapshot(context.Context, uuid.UUID, string) (assistantmemory.Snapshot, error) {
	return p.s, nil
}
func TestPreferenceChangesOnlyFutureGeneration(t *testing.T) {
	sources := &workerSources{target: target.Target{ID: uuid.New(), EmploymentType: "internship"}, ready: "ready", marketHash: "unchanged", level: 2}
	service := NewService(nil, sources, sources, nil)
	p := &preferenceFixture{s: assistantmemory.Snapshot{Memories: []assistantmemory.Memory{{Key: assistantmemory.Languages, Value: json.RawMessage(`["Go"]`), Revision: 1, Status: "active"}}}}
	service.SetPreferences(p)
	first, e := service.Snapshot(context.Background(), uuid.New())
	if e != nil {
		t.Fatal(e)
	}
	p.s.Memories[0].Value = json.RawMessage(`["Python"]`)
	p.s.Memories[0].Revision++
	next, e := service.Snapshot(context.Background(), uuid.New())
	if e != nil {
		t.Fatal(e)
	}
	if first.SourceHash == next.SourceHash {
		t.Fatal("new generation did not capture preference revision")
	}
	if first.BusinessSourceHash != next.BusinessSourceHash || ProjectContextHash(first.Input) != ProjectContextHash(next.Input) {
		t.Fatal("preference change invalidated existing project")
	}
	p.s.Memories = append(p.s.Memories, assistantmemory.Memory{Key: assistantmemory.Answer, Value: json.RawMessage(`"简短"`), Status: "active"})
	styleOnly, e := service.Snapshot(context.Background(), uuid.New())
	if e != nil {
		t.Fatal(e)
	}
	if styleOnly.SourceHash != next.SourceHash {
		t.Fatal("response style invalidated recommendation generation")
	}
}
