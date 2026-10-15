package assistantmemory

import (
	"encoding/json"
	"testing"
)

func TestPreferenceBoundary(t *testing.T) {
	for _, key := range []string{"capability.level", "weekly_hours", "api_key", "selected_project"} {
		if _, e := Validate(Write{Key: key, Value: json.RawMessage(`"Go"`)}); e == nil {
			t.Fatalf("business fact accepted: %s", key)
		}
	}
	w, e := Validate(Write{Key: Languages, Value: json.RawMessage(`[" Go ","Go","Python"]`)})
	if e != nil || string(w.Value) != `["Go","Python"]` {
		t.Fatalf("normalization: %s %v", w.Value, e)
	}
	s := Snapshot{Memories: []Memory{{Key: Languages, Status: "active"}, {Key: Learning, Status: "active"}, {Key: Answer, Status: "active"}}}
	if len(Relevant(s, "recommendations").Memories) != 1 || len(Relevant(s, "knowledge").Memories) != 1 || len(Relevant(s, "agent").Memories) != 3 {
		t.Fatal("unrelated preference leaked into generation dependencies")
	}
}
