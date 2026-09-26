package postgres

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/LeoninCS/jobpilot-next/backend/internal/httpapi"
	"github.com/LeoninCS/jobpilot-next/backend/internal/identity"
	"github.com/LeoninCS/jobpilot-next/backend/internal/knowledgegaps"
	"github.com/LeoninCS/jobpilot-next/backend/internal/market"
	"github.com/LeoninCS/jobpilot-next/backend/internal/profile"
	"github.com/LeoninCS/jobpilot-next/backend/internal/target"
	"github.com/google/uuid"
)

func TestKnowledgeGapAPIReadinessWithExistingJDsIntegration(t *testing.T) {
	dsn := os.Getenv("JOBPILOT_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("JOBPILOT_TEST_DATABASE_URL is not set")
	}
	db, err := Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var userID, targetID uuid.UUID
	if err := db.QueryRow(`SELECT user_id,id FROM job_targets WHERE is_current ORDER BY updated_at DESC LIMIT 1`).Scan(&userID, &targetID); err != nil {
		t.Skip("no existing user target")
	}
	var included int
	if err := db.QueryRow(`SELECT COUNT(*) FROM job_descriptions WHERE target_id=$1 AND status='included' AND validation_status='valid'`, targetID).Scan(&included); err != nil {
		t.Fatal(err)
	}
	if included >= 10 {
		t.Skip("this live goal already meets the market gate")
	}
	targets := target.NewService(NewTargetRepository(db))
	markets := market.NewService(NewMarketRepository(db, AbilityReviewQuotaLimits{}), targets)
	profiles := profile.NewService(NewProfileRepository(db), nil)
	gaps := knowledgegaps.NewService(NewKnowledgeGapsRepository(db), targets, markets, profiles)
	router := httpapi.NewRouter(httpapi.Dependencies{IdentityResolver: identity.DevResolver{DefaultUserID: userID}, TargetService: targets, MarketService: markets, ProfileService: profiles, KnowledgeGapsService: gaps})
	read := httptest.NewRecorder()
	router.ServeHTTP(read, httptest.NewRequest(http.MethodGet, "/api/v1/knowledge-gaps", nil))
	if read.Code != http.StatusOK {
		t.Fatalf("readiness GET returned %d: %s", read.Code, read.Body.String())
	}
	if !strings.Contains(read.Body.String(), `"code":"market_incomplete"`) {
		t.Fatal("expected insufficient-real-JD state")
	}
	analyze := httptest.NewRecorder()
	router.ServeHTTP(analyze, httptest.NewRequest(http.MethodPost, "/api/v1/knowledge-gaps/analyze", nil))
	if analyze.Code != http.StatusConflict {
		t.Fatalf("incomplete market should block report: %d", analyze.Code)
	}
}
