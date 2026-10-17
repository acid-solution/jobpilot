package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LeoninCS/jobpilot-next/backend/internal/identity"
	"github.com/LeoninCS/jobpilot-next/backend/internal/modelusage"
	"github.com/LeoninCS/jobpilot-next/backend/internal/mutationlock"
	"github.com/LeoninCS/jobpilot-next/backend/internal/target"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type contextTargetService struct {
	TargetService
	contexts []context.Context
	users    []uuid.UUID
	held     []bool
}

func (s *contextTargetService) record(ctx context.Context, user uuid.UUID) (target.Target, error) {
	s.contexts = append(s.contexts, ctx)
	s.users = append(s.users, user)
	s.held = append(s.held, mutationlock.HeldBy(ctx, user))
	return target.Target{}, nil
}
func (s *contextTargetService) Current(ctx context.Context, user uuid.UUID) (target.Target, error) {
	return s.record(ctx, user)
}
func (s *contextTargetService) UpsertCurrent(ctx context.Context, user uuid.UUID, _ target.UpsertInput) (target.Target, error) {
	return s.record(ctx, user)
}

func TestBusinessContextsRemainStableAfterGinReusesRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)
	user, operation := uuid.New(), uuid.New()
	service := &contextTargetService{}
	router := NewRouter(Dependencies{TargetService: service, IdentityResolver: identity.DevResolver{DefaultUserID: user}, MutationLocker: &mutationLockerRecorder{}})
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	ctx = modelusage.For(ctx, user, operation, "context_test", "user")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/targets/current", nil).WithContext(ctx))
	if response.Code != http.StatusOK || len(service.contexts) != 1 {
		t.Fatalf("read failed: %d %s", response.Code, response.Body.String())
	}
	retained := service.contexts[0]
	if _, pooled := retained.(*gin.Context); pooled {
		t.Fatal("business service retained a pooled Gin context")
	}
	deadline, ok := retained.Deadline()
	wantDeadline, _ := ctx.Deadline()
	if !ok || !deadline.Equal(wantDeadline) || modelusage.From(retained).OperationID != operation || service.users[0] != user {
		t.Fatal("request deadline, usage scope or identity was lost")
	}
	cancel()
	// Force another request through the same router/pool. The first business
	// context must still refer to the first request, including its cancellation.
	response = httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/api/v1/targets/current", strings.NewReader(`{"employment_type":"internship","directions":[]}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || len(service.contexts) != 2 || !service.held[1] {
		t.Fatalf("write lost its account lock: %d %+v", response.Code, service.held)
	}
	if retained.Err() != context.Canceled || modelusage.From(retained).OperationID != operation {
		t.Fatal("a later request changed the first business context")
	}
	if mutationlock.HeldBy(service.contexts[1], user) {
		t.Fatal("request completion left a retained lock scope active")
	}
}
