package agent

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

var ErrNotFound = errors.New("agent conversation not found")
var ErrBusy = errors.New("agent conversation is busy")
var ErrStale = errors.New("agent action is stale")
var ErrActionClosed = errors.New("agent action already resolved")
var ErrRunLost = errors.New("agent run lease lost")

type Conversation struct {
	ID            uuid.UUID `json:"id"`
	Title         string    `json:"title"`
	Status        string    `json:"status"`
	UpdatedAt     time.Time `json:"updated_at"`
	Messages      []Message `json:"messages,omitempty"`
	PendingAction *Action   `json:"pending_action,omitempty"`
}
type Message struct {
	ID        uuid.UUID `json:"id"`
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}
type Action struct {
	ID               uuid.UUID        `json:"id"`
	Kind             string           `json:"kind"`
	Arguments        json.RawMessage  `json:"arguments"`
	Summary          string           `json:"summary"`
	Status           string           `json:"status"`
	InterruptID      string           `json:"-"`
	ExpectedHash     string           `json:"-"`
	ExpectedVersions map[string]int64 `json:"-"`
}

// Snapshot keeps content checks independent of monotonic resource revisions.
// A revision still changes when a resource is edited back to its old contents.
type Snapshot struct {
	Hash     string
	Versions map[string]int64
}
type Event struct {
	Type     string  `json:"type"`
	Text     string  `json:"text,omitempty"`
	Tool     string  `json:"tool,omitempty"`
	Action   *Action `json:"action,omitempty"`
	Citation any     `json:"citation,omitempty"`
	Code     string  `json:"code,omitempty"`
}

type Repository interface {
	List(context.Context, uuid.UUID, string) ([]Conversation, error)
	Create(context.Context, uuid.UUID, string, string) (Conversation, error)
	Get(context.Context, uuid.UUID, string, uuid.UUID) (Conversation, error)
	Delete(context.Context, uuid.UUID, string, uuid.UUID) error
	BeginRun(context.Context, uuid.UUID, string, uuid.UUID) (uuid.UUID, error)
	BeginResume(context.Context, uuid.UUID, string, uuid.UUID) (uuid.UUID, error)
	HeartbeatRun(context.Context, uuid.UUID, uuid.UUID) error
	EndRun(context.Context, uuid.UUID, uuid.UUID, bool) error
	AddMessage(context.Context, uuid.UUID, string, string) (Message, error)
	ReadResourceVersions(context.Context, uuid.UUID, []string) (map[string]int64, error)
	CreateAction(context.Context, uuid.UUID, uuid.UUID, string, string, json.RawMessage, string, Snapshot) (Action, error)
	SetInterrupt(context.Context, uuid.UUID, uuid.UUID, string) error
	GetAction(context.Context, uuid.UUID, string, uuid.UUID) (Action, error)
	ClaimAction(context.Context, uuid.UUID, uuid.UUID) (bool, error)
	FinishAction(context.Context, uuid.UUID, string, json.RawMessage, string) error
	CancelAction(context.Context, uuid.UUID) error
	StaleAction(context.Context, uuid.UUID) error
}

type CheckpointStore interface {
	Get(context.Context, string) ([]byte, bool, error)
	Set(context.Context, string, []byte) error
	Delete(context.Context, string) error
}
