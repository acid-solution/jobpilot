package postgres

import (
	"context"
	"encoding/base64"
	"os"
	"testing"
	"time"

	"github.com/LeoninCS/jobpilot-next/backend/internal/knowledgegaps"
	"github.com/LeoninCS/jobpilot-next/backend/internal/market"
	"github.com/LeoninCS/jobpilot-next/backend/internal/model/deepseek"
	"github.com/LeoninCS/jobpilot-next/backend/internal/modelconfig"
	"github.com/LeoninCS/jobpilot-next/backend/internal/profile"
	"github.com/LeoninCS/jobpilot-next/backend/internal/projectrecs"
	"github.com/LeoninCS/jobpilot-next/backend/internal/secure"
	"github.com/LeoninCS/jobpilot-next/backend/internal/target"
	"github.com/google/uuid"
)

type countedRecommendationModel struct {
	client *deepseek.Client
	calls  int
}

func (m *countedRecommendationModel) GenerateJSON(ctx context.Context, key, model, system, prompt string, out any) error {
	m.calls++
	return m.client.GenerateJSON(ctx, key, model, system, prompt, out)
}

func TestProjectRecommendationLiveReadiness(t *testing.T) {
	if os.Getenv("JOBPILOT_REAL_MODEL_TEST") != "1" {
		t.Skip("live acceptance disabled")
	}
	dsn := os.Getenv("JOBPILOT_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("database is not configured")
	}
	ctx := context.Background()
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	targets := target.NewService(NewTargetRepository(db))
	markets := market.NewService(NewMarketRepository(db, AbilityReviewQuotaLimits{}), targets)
	profiles := profile.NewService(NewProfileRepository(db), nil)
	gaps := knowledgegaps.NewService(NewKnowledgeGapsRepository(db), targets, markets, profiles)
	service := projectrecs.NewService(NewProjectRecommendationRepository(db), targets, markets, profiles, gaps, nil)
	rows, err := db.QueryContext(ctx, `SELECT DISTINCT user_id FROM job_targets WHERE is_current`)
	if err != nil {
		t.Fatal(err)
	}
	users := []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		users = append(users, id)
	}
	rows.Close()
	if len(users) == 0 {
		t.Skip("no current target")
	}
	for _, id := range users {
		ss, err := service.Snapshot(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("current profile: readiness=%s included_jds=%d market_abilities=%d user_abilities=%d", ss.Readiness.Code, ss.Input.IncludedJDCount, len(ss.Input.MarketAbilities), len(ss.Input.UserAbilities))
		if ss.Readiness.Code == "ready" {
			return
		}
	}
	t.Skip("no complete market and user profile available for live recommendation")
}

func TestProjectRecommendationLiveEndToEnd(t *testing.T) {
	if os.Getenv("JOBPILOT_REAL_MODEL_TEST") != "1" {
		t.Skip("live acceptance disabled")
	}
	dsn := os.Getenv("JOBPILOT_TEST_DATABASE_URL")
	encodedKey := os.Getenv("CREDENTIAL_ENCRYPTION_KEY")
	if dsn == "" || encodedKey == "" {
		t.Skip("database or credential encryption key is not configured")
	}
	key, err := base64.StdEncoding.DecodeString(encodedKey)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := secure.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	targets := target.NewService(NewTargetRepository(db))
	markets := market.NewService(NewMarketRepository(db, AbilityReviewQuotaLimits{}), targets)
	profiles := profile.NewService(NewProfileRepository(db), nil)
	gaps := knowledgegaps.NewService(NewKnowledgeGapsRepository(db), targets, markets, profiles)
	deepseekClient := deepseek.NewClient("https://api.deepseek.com", nil)
	models := modelconfig.NewService(NewModelConfigRepository(db), cipher, deepseekClient)
	repo := NewProjectRecommendationRepository(db)
	service := projectrecs.NewService(repo, targets, markets, profiles, gaps, models)
	rows, err := db.QueryContext(ctx, `SELECT DISTINCT user_id FROM job_targets WHERE is_current`)
	if err != nil {
		t.Fatal(err)
	}
	users := []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		if err = rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		users = append(users, id)
	}
	rows.Close()
	var user uuid.UUID
	for _, id := range users {
		ss, snapshotErr := service.Snapshot(ctx, id)
		if snapshotErr != nil {
			t.Fatal(snapshotErr)
		}
		if ss.Readiness.Code != "ready" {
			continue
		}
		config, configErr := models.Get(ctx, id)
		if configErr != nil {
			t.Fatal(configErr)
		}
		if !config.Configured {
			continue
		}
		if _, configErr = models.Credentials(ctx, id); configErr != nil {
			t.Fatal(configErr)
		}
		user = id
		break
	}
	if user == uuid.Nil {
		t.Skip("no ready profile with a decryptable user DeepSeek key")
	}
	if _, err = service.Generate(ctx, user, ""); err != nil {
		t.Fatal(err)
	}
	counted := &countedRecommendationModel{client: deepseekClient}
	worker := projectrecs.NewWorker(repo, service, models, counted, projectrecs.NewGitHubClient("https://api.github.com", os.Getenv("GITHUB_TOKEN"), nil), time.Second)
	for attempt := 0; attempt < 3; attempt++ {
		worked, workErr := worker.ProcessOnce(ctx)
		if workErr != nil {
			t.Fatal(workErr)
		}
		if !worked {
			t.Fatal("new recommendation job was not claimed")
		}
		view, viewErr := service.Get(ctx, user)
		if viewErr != nil {
			t.Fatal(viewErr)
		}
		if view.Job != nil && view.Job.Status == "succeeded" {
			if view.Report == nil || len(view.Report.Projects) > 3 {
				t.Fatalf("invalid report: %+v", view.Report)
			}
			for _, p := range view.Report.Projects {
				if len(p.References) > 2 {
					t.Fatal("more than two references")
				}
			}
			t.Logf("live recommendation: projects=%d model_calls=%d", len(view.Report.Projects), counted.calls)
			return
		}
		if view.Job == nil || view.Job.Status == "failed" {
			t.Fatalf("recommendation failed: %+v", view.Job)
		}
		time.Sleep(time.Duration(10*(1<<attempt)) * time.Second)
	}
	t.Fatal("recommendation did not complete after retries")
}

// Read-only acceptance when no complete profile has configured a personal key.
// It checks one real-profile concept and its GitHub evidence without publishing
// a report or changing the user's model settings.
func TestProjectRecommendationLiveModelAndGitHub(t *testing.T) {
	if os.Getenv("JOBPILOT_REAL_MODEL_TEST") != "1" {
		t.Skip("live acceptance disabled")
	}
	dsn, key := os.Getenv("JOBPILOT_TEST_DATABASE_URL"), os.Getenv("PLATFORM_DEEPSEEK_API_KEY")
	if dsn == "" || key == "" {
		t.Skip("database or test model key missing")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	targets := target.NewService(NewTargetRepository(db))
	markets := market.NewService(NewMarketRepository(db, AbilityReviewQuotaLimits{}), targets)
	profiles := profile.NewService(NewProfileRepository(db), nil)
	gaps := knowledgegaps.NewService(NewKnowledgeGapsRepository(db), targets, markets, profiles)
	service := projectrecs.NewService(NewProjectRecommendationRepository(db), targets, markets, profiles, gaps, nil)
	rows, err := db.QueryContext(ctx, `SELECT DISTINCT user_id FROM job_targets WHERE is_current`)
	if err != nil {
		t.Fatal(err)
	}
	users := []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		if err = rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		users = append(users, id)
	}
	rows.Close()
	var ss projectrecs.Snapshot
	for _, id := range users {
		candidate, snapshotErr := service.Snapshot(ctx, id)
		if snapshotErr != nil {
			t.Fatal(snapshotErr)
		}
		if candidate.Readiness.Code == "ready" {
			ss = candidate
			break
		}
	}
	if ss.Readiness.Code != "ready" {
		t.Skip("no complete profile")
	}
	model := deepseek.NewClient("https://api.deepseek.com", nil)
	modelName := os.Getenv("PLATFORM_REVIEW_MODEL")
	if modelName == "" {
		modelName = "deepseek-chat"
	}
	drafts, empty, err := projectrecs.GenerateDrafts(ctx, model, key, modelName, ss.Input, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(drafts) == 0 {
		t.Fatalf("real profile returned no project: %s", empty)
	}
	search := projectrecs.NewGitHubClient("https://api.github.com", os.Getenv("GITHUB_TOKEN"), nil)
	research := make([]projectrecs.Research, 0, len(drafts))
	for _, draft := range drafts {
		item, searchErr := search.Research(ctx, draft)
		if searchErr != nil {
			t.Fatal(searchErr)
		}
		names := []string{}
		for _, repo := range item.Repositories {
			names = append(names, repo.FullName)
		}
		t.Logf("GitHub candidates: title=%s queries=%v repos=%v incomplete=%v", draft.Title, item.Queries, names, item.Incomplete)
		research = append(research, item)
	}
	checks, err := projectrecs.Evaluate(ctx, model, key, modelName, drafts, research, false)
	if err != nil {
		t.Fatal(err)
	}
	revised := []projectrecs.Draft{}
	for _, check := range checks {
		if check.TooSimilar && check.Revised != nil {
			candidate := *check.Revised
			candidate.ID = check.DraftID
			revised = append(revised, candidate)
		}
	}
	if len(revised) > 0 {
		revisedResearch := make([]projectrecs.Research, 0, len(revised))
		for _, draft := range revised {
			item, searchErr := search.Research(ctx, draft)
			if searchErr != nil {
				t.Fatal(searchErr)
			}
			revisedResearch = append(revisedResearch, item)
		}
		final, compareErr := projectrecs.Evaluate(ctx, model, key, modelName, revised, revisedResearch, true)
		if compareErr != nil {
			t.Fatal(compareErr)
		}
		for _, draft := range revised {
			for i := range drafts {
				if drafts[i].ID == draft.ID {
					drafts[i] = draft
				}
			}
		}
		for _, item := range revisedResearch {
			for i := range research {
				if research[i].DraftID == item.DraftID {
					research[i] = item
				}
			}
		}
		for _, item := range final {
			for i := range checks {
				if checks[i].DraftID == item.DraftID {
					checks[i] = item
				}
			}
		}
	}
	projects := projectrecs.BuildProjects(drafts, research, checks)
	if len(projects) == 0 {
		t.Fatal("real profile produced no viable resume project")
	}
	if len(projects) > 3 {
		t.Fatalf("invalid comparison: %+v", projects)
	}
	for _, project := range projects {
		if len(project.References) > 2 {
			t.Fatalf("too many references: %s", project.Title)
		}
		t.Logf("real profile project checked: title=%s references=%d searched_queries=%d", project.Title, len(project.References), len(project.SearchedDirections))
		t.Logf("project review: problem=%s scope=%v fit=%v assumptions=%v", project.Problem, project.Scope, project.FitReasons, project.Assumptions)
		for _, ref := range project.References {
			t.Logf("reference review: project=%s repo=%s differences=%v project_limits=%v", project.Title, ref.FullName, ref.Comparison.Differences, ref.Comparison.ProjectLimits)
		}
	}
}
