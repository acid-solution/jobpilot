package agentcontext

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/LeoninCS/jobpilot-next/backend/internal/assistantmemory"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
)

var ErrChanged = errors.New("agent context changed")
var ErrBudget = errors.New("agent context input exceeds safe budget")
var ErrArtifact = errors.New("context artifact unavailable or expired")

type Config struct {
	Enabled, MemoryEnabled                                                      bool
	InputMax, CompactAt, Recent, SummaryMax, OutputReserve, Safety, ModelWindow int
	ArtifactTTL                                                                 time.Duration
}

func DefaultConfig() Config {
	return Config{true, true, 48000, 32000, 8000, 2500, 4096, 8192, 65536, 168 * time.Hour}
}
func (c Config) Limit() int { return min(c.InputMax, c.ModelWindow-c.OutputReserve-c.Safety) }
func (c Config) Validate() error {
	if c.InputMax < 1024 || c.CompactAt < 512 || c.Recent < 128 || c.SummaryMax < 128 || c.OutputReserve < 1 || c.Safety < 1 || c.ArtifactTTL <= 0 || c.CompactAt >= c.Limit() || c.Recent+c.SummaryMax >= c.CompactAt {
		return errors.New("invalid Agent context budgets: recent+summary < compact < input <= model window-output-safety required")
	}
	return nil
}

// Estimate deliberately counts UTF-8 bytes plus protocol overhead instead of
// assuming that Chinese, JSON and English all have four characters per token.
// It is conservative, not a claim to be the provider's exact tokenizer.
func Estimate(v any) int { b, _ := json.Marshal(v); return len(b) + 256 }
func Count(messages []*schema.Message, tools []*schema.ToolInfo) int {
	return Estimate(messages) + Estimate(tools) + 64*len(messages)
}

type Turn struct {
	Dependencies                                     map[string]int64
	ID, UserID, ConversationID, Token, UserMessageID uuid.UUID
	Goal                                             string
	Epoch                                            assistantmemory.Epoch
	PreferencesHash                                  string
	Status                                           string
}
type Message struct {
	ContextStale bool       `json:"context_stale,omitempty"`
	ID           uuid.UUID  `json:"id"`
	Role         string     `json:"role"`
	Content      string     `json:"content"`
	Sequence     int64      `json:"sequence"`
	TurnID       *uuid.UUID `json:"turn_id,omitempty"`
	Kind         string     `json:"kind"`
	CreatedAt    time.Time  `json:"created_at"`
}
type Item struct {
	Kind    string      `json:"kind"`
	Text    string      `json:"text"`
	State   string      `json:"state"`
	Sources []uuid.UUID `json:"source_message_ids"`
}
type Summary struct {
	ExpectedCovered int64            `json:"-"`
	PartialSequence int64            `json:"-"`
	PartialOffset   int              `json:"-"`
	Revision        int64            `json:"revision"`
	Covered         int64            `json:"covered_sequence"`
	Items           []Item           `json:"items"`
	Dependencies    map[string]int64 `json:"-"`
}
type Artifact struct {
	ID     uuid.UUID `json:"artifact_id"`
	Text   string    `json:"text"`
	Offset int       `json:"offset"`
	Next   *int      `json:"next_offset"`
	Hash   string    `json:"content_hash"`
}
type Repository interface {
	Start(context.Context, Turn, string) (Turn, error)
	Resume(context.Context, Turn) (Turn, error)
	History(context.Context, Turn, int64) ([]Message, error)
	Summary(context.Context, Turn) (Summary, error)
	Publish(context.Context, Turn, Summary, int64, string) error
	Finish(context.Context, Turn, string, map[string]int64) error
	Versions(context.Context, uuid.UUID, []string) (map[string]int64, error)
	StoreArtifact(context.Context, Turn, string, map[string]int64, time.Duration) (uuid.UUID, error)
	ReadArtifact(context.Context, Turn, uuid.UUID, int) (Artifact, error)
}
type State struct {
	Overhead          int
	Turn              Turn
	mu                sync.Mutex
	deps              map[string]int64
	Changed           bool
	SummaryAttempted  bool
	resetObservations bool
	expectedGoal      string
	observations      []Observation
}

type Observation struct {
	Resource string           `json:"resource"`
	Versions map[string]int64 `json:"versions"`
	Hash     string           `json:"result_hash"`
	Retired  bool             `json:"retired,omitempty"`
}

func (s *State) RecordRead(resource, result string, versions map[string]int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	hash := sha256.Sum256([]byte(result))
	copy := map[string]int64{}
	for k, v := range versions {
		copy[k] = v
	}
	s.observations = append(s.observations, Observation{Resource: resource, Versions: copy, Hash: hex.EncodeToString(hash[:])})
}
func (s *State) Observations() []Observation {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Observation(nil), s.observations...)
}

func NewState(t Turn) *State {
	s := &State{Turn: t, deps: map[string]int64{}}
	s.Observe(t.Dependencies)
	return s
}
func (s *State) Observe(v map[string]int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, n := range v {
		if old, ok := s.deps[k]; ok && old != n {
			s.Changed = true
		}
		s.deps[k] = n
	}
}
func (s *State) Dependencies() map[string]int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]int64{}
	for k, n := range s.deps {
		out[k] = n
	}
	return out
}
func (s *State) IsChanged() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.Changed }

// AcceptMutation is used only after the confirmed business write commits. Old
// observations must be removed before the next model call, never re-labelled as
// fresh observations. Unrelated concurrent changes are checked under the lock.
func (s *State) AcceptMutation(versions map[string]int64, goal string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deps = versions
	s.expectedGoal = goal
	s.resetObservations = true
	for i := range s.observations {
		s.observations[i].Retired = true
	}
}
func (s *State) ExpectedGoal() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.expectedGoal != "" {
		return s.expectedGoal
	}
	return s.Turn.Goal
}
func (s *State) TakeObservationReset() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.resetObservations
	s.resetObservations = false
	return v
}

type stateKey struct{}
type kindKey struct{}

func WithMessageKind(ctx context.Context, kind string) context.Context {
	return context.WithValue(ctx, kindKey{}, kind)
}
func MessageKind(ctx context.Context) string {
	if k, ok := ctx.Value(kindKey{}).(string); ok {
		return k
	}
	return "legacy"
}

func WithState(ctx context.Context, s *State) context.Context {
	return context.WithValue(ctx, stateKey{}, s)
}
func From(ctx context.Context) *State { s, _ := ctx.Value(stateKey{}).(*State); return s }
