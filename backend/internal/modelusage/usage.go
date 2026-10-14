package modelusage

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"log/slog"
	"time"
)

var ErrUnavailable = errors.New("model usage recorder unavailable")

type Scope struct {
	UserID        uuid.UUID
	OperationID   uuid.UUID
	Purpose       string
	Payer         string
	TargetID      uuid.UUID
	ResourceID    uuid.UUID
	LegacyUsageID uuid.UUID
}
type scopeKey struct{}
type recorderKey struct{}

func WithRecorder(ctx context.Context, r Repository) context.Context {
	return context.WithValue(ctx, recorderKey{}, r)
}
func WithScope(ctx context.Context, s Scope) context.Context {
	if r, ok := ctx.Value(recorderKey{}).(Repository); ok {
		if err := r.EnsureOperation(ctx, s); err != nil {
			slog.Error("persist model operation failed", "operation_id", s.OperationID)
		}
	}
	return context.WithValue(ctx, scopeKey{}, s)
}
func From(ctx context.Context) Scope { s, _ := ctx.Value(scopeKey{}).(Scope); return s }
func For(ctx context.Context, user, operation uuid.UUID, purpose, payer string) context.Context {
	s := From(ctx)
	s.UserID = user
	s.OperationID = operation
	s.Purpose = purpose
	s.Payer = payer
	return WithScope(ctx, s)
}

func Resource(ctx context.Context, target, resource uuid.UUID) context.Context {
	s := From(ctx)
	s.TargetID, s.ResourceID = target, resource
	return context.WithValue(ctx, scopeKey{}, s)
}

type Tokens struct {
	Input     *int64 `json:"input_tokens"`
	Output    *int64 `json:"output_tokens"`
	Total     *int64 `json:"total_tokens"`
	CacheHit  *int64 `json:"cache_hit_tokens"`
	CacheMiss *int64 `json:"cache_miss_tokens"`
}
type Call struct {
	ID                uuid.UUID `json:"id"`
	Scope             Scope     `json:"-"`
	Provider          string    `json:"provider"`
	RequestedModel    string    `json:"requested_model"`
	ReturnedModel     string    `json:"returned_model"`
	ProviderRequestID string    `json:"provider_request_id,omitempty"`
	Payer             string    `json:"payer"`
	Purpose           string    `json:"purpose"`
	OperationID       uuid.UUID `json:"operation_id"`
	StartedAt         time.Time `json:"started_at"`
	DurationMS        int64     `json:"duration_ms"`
	Result            string    `json:"result"`
	UsageStatus       string    `json:"usage_status"`
	Tokens
	Currency      string          `json:"currency,omitempty"`
	Cost          *string         `json:"estimated_cost"`
	PriceSnapshot json.RawMessage `json:"price_snapshot,omitempty"`
	Legacy        bool            `json:"legacy"`
	Confirmed     bool            `json:"confirmed"`
}
type Filter struct {
	From, To       time.Time
	Purpose, Payer string
	OperationID    uuid.UUID
	Page, PageSize int
}
type Group struct {
	Payer             string            `json:"payer"`
	CallCount         int64             `json:"call_count"`
	UnknownUsageCount int64             `json:"unknown_usage_count"`
	UnknownCostCount  int64             `json:"unknown_cost_count"`
	InputTokens       int64             `json:"input_tokens"`
	OutputTokens      int64             `json:"output_tokens"`
	CacheHitTokens    int64             `json:"cache_hit_tokens"`
	CacheMissTokens   int64             `json:"cache_miss_tokens"`
	CacheSampleInput  int64             `json:"cache_sample_input"`
	CacheHitRate      *float64          `json:"cache_hit_rate"`
	Costs             map[string]string `json:"estimated_costs"`
}
type Operation struct {
	ID        uuid.UUID `json:"id"`
	Purpose   string    `json:"purpose"`
	StartedAt time.Time `json:"started_at"`
	CallCount int64     `json:"call_count"`
}
type Page[T any] struct {
	Items    []T   `json:"items"`
	Total    int64 `json:"total"`
	Page     int   `json:"page"`
	PageSize int   `json:"page_size"`
}
type Repository interface {
	Begin(context.Context, Call) error
	Finish(context.Context, Call) error
	EnsureOperation(context.Context, Scope) error
	Summary(context.Context, uuid.UUID, Filter) ([]Group, error)
	Calls(context.Context, uuid.UUID, Filter) (Page[Call], error)
	Operations(context.Context, uuid.UUID, Filter) (Page[Operation], error)
}

func OperationKey(s Scope) uuid.UUID {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(s.UserID.String()+":"+s.OperationID.String()+":"+s.Purpose))
}
