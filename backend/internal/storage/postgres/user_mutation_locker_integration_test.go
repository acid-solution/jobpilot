package postgres

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/LeoninCS/jobpilot-next/backend/internal/httpapi"
	"github.com/LeoninCS/jobpilot-next/backend/internal/identity"
	"github.com/LeoninCS/jobpilot-next/backend/internal/market"
	"github.com/LeoninCS/jobpilot-next/backend/internal/target"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func TestHTTPMutationsDoNotWaitOnTheirOwnLockIsolated(t *testing.T) {
	db := agentTransactionDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var databaseName string
	if err := db.QueryRowContext(ctx, `SELECT current_database()`).Scan(&databaseName); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(os.Getenv("JOBPILOT_ADMIN_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + databaseName
	locker, err := NewUserMutationLocker(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer locker.Close()
	user := uuid.New()
	targets := target.NewService(NewTargetRepository(db))
	markets := market.NewService(NewMarketRepository(db), targets)
	catalog, err := targets.Catalog(ctx)
	if err != nil || len(catalog) == 0 {
		t.Fatalf("catalog: %v", err)
	}
	gin.SetMode(gin.TestMode)
	router := httpapi.NewRouter(httpapi.Dependencies{
		IdentityResolver: identity.DevResolver{DefaultUserID: user},
		MutationLocker:   locker,
		TargetService:    targets,
		MarketService:    markets,
	})
	put := func(t *testing.T, path string, body any, wantStatus int) {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		requestCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		request := httptest.NewRequest(http.MethodPut, path, strings.NewReader(string(raw))).WithContext(requestCtx)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if err := requestCtx.Err(); err != nil {
			t.Fatalf("%s waited until the request deadline released its outer lock: %v", path, err)
		}
		if response.Code != wantStatus {
			t.Fatalf("%s returned %d: %s", path, response.Code, response.Body.String())
		}
	}
	t.Run("save target", func(t *testing.T) {
		put(t, "/api/v1/targets/current", target.UpsertInput{
			EmploymentType: "internship", Directions: []target.DirectionInput{{CategoryID: catalog[0].ID}},
		}, http.StatusOK)
	})
	// Seed separately so the JD regression also runs when saving the target fails.
	if _, err := targets.UpsertCurrent(ctx, user, target.UpsertInput{
		EmploymentType: "internship", Directions: []target.DirectionInput{{CategoryID: catalog[0].ID}},
	}); err != nil {
		t.Fatal(err)
	}
	jd, err := markets.Submit(ctx, user, "招聘 Go 后端实习生，负责接口开发，要求熟悉 Go 和 PostgreSQL。")
	if err != nil {
		t.Fatal(err)
	}
	t.Run("edit JD", func(t *testing.T) {
		put(t, "/api/v1/jds/"+jd.ID.String(), map[string]string{"raw_text": "招聘 Java 后端实习生，负责接口开发，要求熟悉 Java 和 PostgreSQL。"}, http.StatusAccepted)
	})
}
