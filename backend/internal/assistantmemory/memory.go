// Package assistantmemory owns confirmed preferences shared by the assistant,
// project recommendations and learning routes. It never owns profile facts.
package assistantmemory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

var ErrConflict = errors.New("memory revision or context changed")
var ErrInvalid = errors.New("invalid assistant preference")
var ErrNotFound = errors.New("memory not found")

const Languages = "project.languages"
const Excluded = "project.excluded_types"
const Architecture = "project.architecture"
const Learning = "learning.style"
const Answer = "answer.detail"

type Memory struct {
	ID              uuid.UUID       `json:"id"`
	Scope           string          `json:"scope"`
	Key             string          `json:"key"`
	Value           json.RawMessage `json:"value"`
	Revision        int64           `json:"revision"`
	Hash            string          `json:"content_hash"`
	Status          string          `json:"status"`
	SourceMessageID *uuid.UUID      `json:"source_message_id,omitempty"`
	SourceQuote     string          `json:"source_quote,omitempty"`
	UpdatedAt       time.Time       `json:"updated_at"`
}
type Epoch struct {
	Global int64 `json:"global"`
	Goal   int64 `json:"goal"`
}
type Snapshot struct {
	Memories  []Memory `json:"memories"`
	Forgotten []Memory `json:"forgotten,omitempty"`
	Epoch     Epoch    `json:"epoch"`
}
type Write struct {
	Key      string          `json:"key"`
	Value    json.RawMessage `json:"value"`
	Revision int64           `json:"expected_revision"`
	Hash     string          `json:"expected_hash"`
}
type Proposal struct {
	ID              uuid.UUID `json:"id"`
	ConversationID  uuid.UUID `json:"conversation_id"`
	SourceMessageID uuid.UUID `json:"source_message_id"`
	SourceQuote     string    `json:"source_quote"`
	Write
	Scope       string          `json:"scope"`
	Before      json.RawMessage `json:"before,omitempty"`
	ContentHash string          `json:"content_hash"`
	Epoch       Epoch           `json:"epoch"`
	Status      string          `json:"status"`
	Result      *Memory         `json:"result,omitempty"`
}
type Repository interface {
	Snapshot(context.Context, uuid.UUID, string) (Snapshot, error)
	Save(context.Context, uuid.UUID, string, *uuid.UUID, Write) (Memory, error)
	Forget(context.Context, uuid.UUID, string, uuid.UUID, int64, string) error
	Propose(context.Context, uuid.UUID, string, Proposal) (Proposal, error)
	Proposals(context.Context, uuid.UUID, string) ([]Proposal, error)
	Resolve(context.Context, uuid.UUID, string, uuid.UUID, bool, string) (Proposal, error)
}
type Provider interface {
	Snapshot(context.Context, uuid.UUID, string) (Snapshot, error)
}

func Scope(key, goal string) string {
	if key == Answer {
		return "global"
	}
	return goal
}
func Validate(w Write) (Write, error) {
	var list []string
	switch w.Key {
	case Languages, Excluded:
		if json.Unmarshal(w.Value, &list) != nil || len(list) < 1 || len(list) > 8 {
			return w, ErrInvalid
		}
		seen := map[string]bool{}
		out := []string{}
		for _, v := range list {
			v = strings.TrimSpace(v)
			if v == "" || len([]rune(v)) > 40 {
				return w, ErrInvalid
			}
			if !seen[strings.ToLower(v)] {
				out = append(out, v)
				seen[strings.ToLower(v)] = true
			}
		}
		w.Value, _ = json.Marshal(out)
	case Architecture, Learning, Answer:
		var value string
		if json.Unmarshal(w.Value, &value) != nil {
			return w, ErrInvalid
		}
		value = strings.TrimSpace(value)
		if value == "" || len([]rune(value)) > 160 {
			return w, ErrInvalid
		}
		w.Value, _ = json.Marshal(value)
	default:
		return w, ErrInvalid
	}
	if strings.Contains(strings.ToLower(string(w.Value)), "sk-") {
		return w, ErrInvalid
	}
	return w, nil
}
func Hash(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// Relevant excludes response style from generation dependencies. A preference
// revision participates only in the generation that actually consumed it.
func Relevant(s Snapshot, purpose string) Snapshot {
	out := Snapshot{Memories: []Memory{}}
	for _, m := range s.Memories {
		if m.Status != "active" {
			continue
		}
		keep := purpose == "agent" || (purpose == "recommendations" && strings.HasPrefix(m.Key, "project.")) ||
			(purpose == "route" && m.Key != Answer && m.Key != Excluded) || (purpose == "knowledge" && m.Key == Learning)
		if keep {
			out.Memories = append(out.Memories, m)
		}
	}
	return out
}
func Fingerprint(s Snapshot) string { return Hash(s.Memories) }
