package agentcontext

import (
	"github.com/google/uuid"
	"strings"
	"testing"
)

func TestLongMessageChunkProgressNeverSkipsTurn(t *testing.T) {
	turn := uuid.New()
	original := strings.Repeat("早期限制和明确纠正", 2000)
	older := []Message{{ID: uuid.New(), Sequence: 1, Role: "user", Content: original, TurnID: &turn}, {ID: uuid.New(), Sequence: 2, Role: "assistant", Content: "尚未决定", TurnID: &turn}}
	summary := Summary{}
	var consumed strings.Builder
	calls := 0
	for summary.Covered < 2 {
		source, next := SummarySource(summary, older, 4000)
		if len(source) == 0 {
			t.Fatal("chunk made no progress")
		}
		for _, m := range source {
			if m.Sequence == 1 {
				consumed.WriteString(m.Content)
			}
		}
		if next.Covered == 1 {
			t.Fatal("advanced across an incomplete turn")
		}
		summary = next
		calls++
		if calls > 100 {
			t.Fatal("chunk cursor did not converge")
		}
	}
	if consumed.String() != original || calls < 2 || summary.PartialSequence != 0 {
		t.Fatal("source skipped or repeated")
	}
}
