package httpapi

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/LeoninCS/jobpilot-next/backend/internal/identity"
	"github.com/LeoninCS/jobpilot-next/backend/internal/modelusage"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type usageReaderStub struct {
	modelusage.Repository
	user   uuid.UUID
	filter modelusage.Filter
}

func (s *usageReaderStub) Summary(_ context.Context, u uuid.UUID, f modelusage.Filter) ([]modelusage.Group, error) {
	s.user, s.filter = u, f
	return []modelusage.Group{}, nil
}
func (s *usageReaderStub) Calls(_ context.Context, u uuid.UUID, f modelusage.Filter) (modelusage.Page[modelusage.Call], error) {
	s.user, s.filter = u, f
	return modelusage.Page[modelusage.Call]{}, nil
}
func (s *usageReaderStub) Operations(_ context.Context, u uuid.UUID, f modelusage.Filter) (modelusage.Page[modelusage.Operation], error) {
	s.user, s.filter = u, f
	return modelusage.Page[modelusage.Operation]{}, nil
}
func TestUsageQueriesUseAuthenticatedAccountAndShanghaiDates(t *testing.T) {
	gin.SetMode(gin.TestMode)
	user := uuid.New()
	reader := &usageReaderStub{}
	router := gin.New()
	router.ContextWithFallback = true
	group := router.Group("/api/v1")
	group.Use(identity.Middleware(identity.DevResolver{DefaultUserID: user}))
	registerModelUsage(group, reader)
	for _, endpoint := range []string{"summary", "calls", "operations"} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/model-usage/"+endpoint+"?from=2026-10-01&to=2026-10-14&page=2&page_size=10&user_id="+uuid.NewString(), nil))
		if w.Code != 200 || reader.user != user || reader.filter.Page != 2 || reader.filter.PageSize != 10 {
			t.Fatal("account or pagination bypassed")
		}
		if reader.filter.From.UTC().Format(time.RFC3339) != "2026-09-30T16:00:00Z" || reader.filter.To.UTC().Format(time.RFC3339) != "2026-10-14T16:00:00Z" {
			t.Fatal("incorrect natural-day bounds")
		}
	}
	for _, query := range []string{"from=invalid", "payer=someone", "page=-1", "page_size=101", "operation_id=invalid", "from=2024-01-01&to=2026-01-01"} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/model-usage/calls?"+query, nil))
		if w.Code != 400 {
			t.Fatal("unsafe filter accepted", query)
		}
	}
}
