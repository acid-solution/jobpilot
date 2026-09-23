package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LeoninCS/jobpilot-next/backend/internal/identity"
	"github.com/LeoninCS/jobpilot-next/backend/internal/market"
	"github.com/LeoninCS/jobpilot-next/backend/internal/target"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type targetServiceStub struct {
	current target.Target
	err     error
}

func (s targetServiceStub) Current(context.Context, uuid.UUID) (target.Target, error) {
	return s.current, s.err
}

func (s targetServiceStub) UpsertCurrent(context.Context, uuid.UUID, target.UpsertInput) (target.Target, error) {
	return s.current, s.err
}

func (s targetServiceStub) Catalog(context.Context) ([]target.Category, error) {
	return nil, s.err
}

type marketServiceStub struct {
	result market.JobDescription
	err    error
}

func (s marketServiceStub) Submit(context.Context, uuid.UUID, string) (market.JobDescription, error) {
	return s.result, s.err
}

func (s marketServiceStub) SubmitBatch(context.Context, uuid.UUID, []string) (market.BatchResult, error) {
	return market.BatchResult{}, s.err
}

func (s marketServiceStub) ListCurrent(context.Context, uuid.UUID, market.ListFilter) ([]market.JobDescription, error) {
	return []market.JobDescription{s.result}, s.err
}

func (s marketServiceStub) Detail(context.Context, uuid.UUID, uuid.UUID) (market.JobDescription, error) {
	return s.result, s.err
}

func (s marketServiceStub) Update(context.Context, uuid.UUID, uuid.UUID, string) (market.JobDescription, error) {
	return s.result, s.err
}

func (s marketServiceStub) Delete(context.Context, uuid.UUID, uuid.UUID) error { return s.err }

func (s marketServiceStub) Retry(context.Context, uuid.UUID, uuid.UUID) (market.JobDescription, error) {
	return s.result, s.err
}
func (s marketServiceStub) RetryAbilityReviews(context.Context, uuid.UUID, uuid.UUID) (market.JobDescription, error) {
	return s.result, s.err
}

func (s marketServiceStub) ProfileCurrent(context.Context, uuid.UUID) (market.Profile, error) {
	return market.Profile{}, s.err
}

func TestSubmitJDReturnsAcceptedProcessingResource(t *testing.T) {
	gin.SetMode(gin.TestMode)
	userID := uuid.New()
	jdID := uuid.New()
	router := NewRouter(Dependencies{
		IdentityResolver: identity.DevResolver{DefaultUserID: userID},
		TargetService:    targetServiceStub{},
		MarketService: marketServiceStub{result: market.JobDescription{
			ID: jdID, Status: market.StatusProcessing, JobStatus: "queued",
		}},
	})

	request := httptest.NewRequest(http.MethodPost, "/api/v1/jds", strings.NewReader(`{"raw_text":"负责 Go 后端开发和 Agent 工具调用，要求掌握 PostgreSQL 与 Redis。"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusAccepted {
		t.Fatalf("expected %d, got %d: %s", http.StatusAccepted, response.Code, response.Body.String())
	}
	var body struct {
		Data market.JobDescription `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Data.ID != jdID || body.Data.JobStatus != "queued" {
		t.Fatalf("unexpected response: %#v", body.Data)
	}
}

func TestSubmitJDWithoutTargetReturnsConflict(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := NewRouter(Dependencies{
		IdentityResolver: identity.DevResolver{DefaultUserID: uuid.New()},
		TargetService:    targetServiceStub{},
		MarketService:    marketServiceStub{err: target.ErrNotFound},
	})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/jds", strings.NewReader(`{"raw_text":"负责 Go 后端开发和 Agent 工具调用，要求掌握 PostgreSQL 与 Redis。"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusConflict {
		t.Fatalf("expected %d, got %d: %s", http.StatusConflict, response.Code, response.Body.String())
	}
}

func TestUpdateAndDeleteJDRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	userID, jdID := uuid.New(), uuid.New()
	router := NewRouter(Dependencies{
		IdentityResolver: identity.DevResolver{DefaultUserID: userID},
		TargetService:    targetServiceStub{},
		MarketService:    marketServiceStub{result: market.JobDescription{ID: jdID, Status: market.StatusProcessing, JobStatus: "queued"}},
	})

	updateRequest := httptest.NewRequest(http.MethodPut, "/api/v1/jds/"+jdID.String(), strings.NewReader(`{"raw_text":"负责 Go 后端开发，参与数据库设计、接口实现、自动化测试和线上故障排查。"}`))
	updateRequest.Header.Set("Content-Type", "application/json")
	updateResponse := httptest.NewRecorder()
	router.ServeHTTP(updateResponse, updateRequest)
	if updateResponse.Code != http.StatusAccepted {
		t.Fatalf("expected update accepted, got %d: %s", updateResponse.Code, updateResponse.Body.String())
	}

	deleteRequest := httptest.NewRequest(http.MethodDelete, "/api/v1/jds/"+jdID.String(), nil)
	deleteResponse := httptest.NewRecorder()
	router.ServeHTTP(deleteResponse, deleteRequest)
	if deleteResponse.Code != http.StatusNoContent {
		t.Fatalf("expected delete no content, got %d: %s", deleteResponse.Code, deleteResponse.Body.String())
	}
}

func TestDuplicateJDResponseIncludesExistingID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	existingID := uuid.New()
	router := NewRouter(Dependencies{
		IdentityResolver: identity.DevResolver{DefaultUserID: uuid.New()},
		TargetService:    targetServiceStub{},
		MarketService:    marketServiceStub{err: &market.DuplicateError{ExistingID: existingID}},
	})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/jds", strings.NewReader(`{"raw_text":"负责 Go 后端开发，参与数据库设计、接口实现、自动化测试和线上故障排查。"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), existingID.String()) {
		t.Fatalf("unexpected duplicate response: %d %s", response.Code, response.Body.String())
	}
}
