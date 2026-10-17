package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LeoninCS/jobpilot-next/backend/internal/abilityidentity"
	"github.com/LeoninCS/jobpilot-next/backend/internal/abilityreview"
	"github.com/LeoninCS/jobpilot-next/backend/internal/jdanalysis"
	"github.com/LeoninCS/jobpilot-next/backend/internal/model/deepseek"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
)

func granularityJD(t *testing.T, db *sql.DB, raw string) (user, target, jd uuid.UUID) {
	t.Helper()
	user, target, jd = uuid.New(), uuid.New(), uuid.New()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO job_targets(id,user_id,title,employment_type,directions,catalog_status)VALUES($1,$2,'后端实习','internship','[]','valid')`, []any{target, user}},
		{`INSERT INTO target_directions(target_id,category_id,sort_order)SELECT $1,id,1 FROM job_categories WHERE code='backend'`, []any{target}},
		{`INSERT INTO job_descriptions(id,user_id,target_id,raw_text,raw_text_hash,status)VALUES($1,$2,$3,$4,encode(digest($4,'sha256'),'hex'),'processing')`, []any{jd, user, target, raw}},
		{`INSERT INTO analysis_jobs(id,user_id,target_id,job_description_id,job_type,status)VALUES($1,$2,$3,$4,'jd_analysis','queued')`, []any{uuid.New(), user, target, jd}},
	} {
		if _, err := db.Exec(q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	return
}

func TestJDGranularityPersistenceIsolated(t *testing.T) {
	db := agentTransactionDatabase(t)
	ctx := t.Context()
	user, _, jd := granularityJD(t, db, "负责Go服务开发，熟悉Go并发和NewFramework。")
	repo := NewAnalysisRepository(db)
	job, err := repo.Claim(ctx, time.Minute*5)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := repo.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	code := ""
	for _, a := range catalog.Abilities {
		if a.Name == "Go" {
			code = a.Code
		}
		if a.Name == "计算机网络" && len(a.IncludeSignals) == 0 {
			t.Fatal("scope metadata missing")
		}
	}
	raw := jdanalysis.Result{DocumentType: jdanalysis.DocumentJobDescription, ValidationStatus: jdanalysis.ValidationValid, Title: "后端实习", Company: "测试", EmploymentType: "internship", AbilityMentions: []jdanalysis.AbilityMention{{Name: "Go和NewFramework"}}}
	if err = repo.SaveStage(ctx, job, "extraction", "succeeded", raw, ""); err != nil {
		t.Fatal(err)
	}
	current, err := NewMarketRepository(db).FindByID(ctx, user, jd)
	if err != nil || len(current.AbilityMentions) != 0 {
		t.Fatalf("raw extraction leaked: %v %#v", err, current.AbilityMentions)
	}
	raw.VectorNormalized = true
	raw.AbilityRequirements = []jdanalysis.AbilityRequirement{
		{Operator: jdanalysis.RequirementSingle, RequiredCount: 1, Evidence: "熟悉Go并发", Options: []jdanalysis.AbilityRequirementOption{{RawLabel: "Go并发", CatalogCode: code, AbilityName: "Go", Evidence: "Go并发", NormalizationRelation: "within_scope", ScopeDetails: []jdanalysis.AbilityScopeDetail{{Label: "Go并发", Evidence: "Go并发"}}}}},
		{Operator: jdanalysis.RequirementSingle, RequiredCount: 1, Evidence: "熟悉Go并发", Options: []jdanalysis.AbilityRequirementOption{{RawLabel: "Go", CatalogCode: code, AbilityName: "Go", Evidence: "Go并发", NormalizationRelation: "equivalent"}}},
		{Operator: jdanalysis.RequirementSingle, RequiredCount: 1, Evidence: "NewFramework", Options: []jdanalysis.AbilityRequirementOption{{RawLabel: "NewFramework", AbilityName: "NewFramework", Evidence: "NewFramework"}}},
	}
	if err = repo.SaveStage(ctx, job, "ability_resolution", "succeeded", raw, ""); err != nil {
		t.Fatal(err)
	}
	current, err = NewMarketRepository(db).FindByID(ctx, user, jd)
	if err != nil || len(current.AbilityMentions) != 1 || current.AbilityMentions[0].Name != "Go" || current.AbilityReview.PendingCount != 1 {
		t.Fatalf("pending names leaked or lost: %v %#v", err, current)
	}
	var details string
	if err = db.QueryRow(`SELECT scope_details::text FROM job_description_ability_requirement_options WHERE raw_label='Go并发'`).Scan(&details); err != nil || !strings.Contains(details, "Go并发") {
		t.Fatalf("scope details not stored: %v %s", err, details)
	}
	old := raw
	old.PromptVersion = "jd-source-stages-v15"
	payload, _ := json.Marshal(stagePayload{Result: old})
	if _, err = db.Exec(`UPDATE jd_analysis_stages SET result=$2 WHERE job_description_id=$1 AND stage='extraction'`, jd, payload); err != nil {
		t.Fatal(err)
	}
	if _, found, err := repo.LoadStage(ctx, job, "extraction"); err != nil || found {
		t.Fatalf("old prompt extraction reused: %v", err)
	}
	reviews := NewAbilityReviewRepository(db)
	input, err := reviews.Claim(ctx, time.Minute*5)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`DELETE FROM job_description_ability_requirements WHERE job_description_id=$1`, jd); err != nil {
		t.Fatal(err)
	}
	if err = reviews.Complete(ctx, input, abilityreview.Result{Decision: "reuse_existing", ExistingAbilityCode: code, Reason: "迟到结果"}, uuid.Nil); !errors.Is(err, abilityreview.ErrLeaseLost) {
		t.Fatalf("orphan review accepted: %v", err)
	}
	if _, err = db.Exec(`UPDATE analysis_jobs SET status='succeeded' WHERE job_description_id=$1`, jd); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE job_descriptions SET status='included',analysis_prompt_version='jd-source-stages-v15' WHERE id=$1`, jd); err != nil {
		t.Fatal(err)
	}
	current, err = NewMarketRepository(db).FindByID(ctx, user, jd)
	if err != nil || !current.Analysis.NeedsReanalysis {
		t.Fatalf("old success had no reanalysis hint: %v", err)
	}
	current, err = NewMarketRepository(db).RetryAnalysis(ctx, user, jd)
	if err != nil || current.JobStatus != "queued" || current.Analysis.NeedsReanalysis {
		t.Fatalf("old success could not be retried: %v %#v", err, current.Analysis)
	}
}

// Opt-in uses the user's supplied public JD, never production account data.
func TestDASJDGranularityLive(t *testing.T) {
	if os.Getenv("RUN_JD_GRANULARITY_LIVE") != "1" {
		t.Skip("explicit opt-in for the user-provided public JD")
	}
	env, err := godotenv.Read("../../../.env.local")
	if err != nil || env["PLATFORM_DEEPSEEK_API_KEY"] == "" {
		t.Fatal("local platform model configuration unavailable")
	}
	key, model, base := env["PLATFORM_DEEPSEEK_API_KEY"], env["PLATFORM_REVIEW_MODEL"], env["DEEPSEEK_BASE_URL"]
	if model == "" {
		model = "deepseek-flash"
	}
	if base == "" {
		base = "https://api.deepseek.com"
	}
	source, err := os.ReadFile("../../model/deepseek/testdata/dbapp_go_intern.txt")
	if err != nil {
		t.Fatal(err)
	}
	db := agentTransactionDatabase(t)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	user, _, jd := granularityJD(t, db, string(source))
	transport := &corpusTransport{base: http.DefaultTransport, redact: key}
	client := deepseek.NewClient(base, &http.Client{Transport: &boundedJDTransport{base: transport}, Timeout: 150 * time.Second})
	repo := NewAnalysisRepository(db)
	worker := jdanalysis.NewWorker(repo, liveCorpusCredentials{key, model}, client, time.Second, jdanalysis.ClassificationReviewConfig{Enabled: true, Reviewer: client, APIKey: key, Model: model})
	worker.SetNormalizer(&jdanalysis.VectorNormalizer{Model: client})
	if worked, err := worker.ProcessOnce(ctx); err != nil || !worked {
		t.Fatalf("worker: %v worked=%v", err, worked)
	}
	current, err := NewMarketRepository(db).FindByID(ctx, user, jd)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("status=%s model=%s error=%s pending=%d calls=%d tokens=%d+%d", current.JobStatus, model, current.JobErrorCode, current.AbilityReview.PendingCount, transport.Calls, transport.Input, transport.Output)
	if current.JobStatus != "succeeded" {
		t.Fatalf("analysis did not succeed: stages=%#v", current.Analysis.Stages)
	}
	if current.Status != "included" {
		t.Errorf("JD was not included: %s", current.Status)
	}
	for _, c := range current.Analysis.Classifications {
		if c.SpecialtyCode == "ai-agent-application" {
			t.Error("generic AI prototype responsibility was misclassified as Agent")
		}
	}
	names := map[string]bool{}
	for _, m := range current.AbilityMentions {
		if !abilityidentity.IsAtomicLabel(m.Name) || m.CatalogCode == "" {
			t.Errorf("invalid public ability %q", m.Name)
		}
		names[m.Name] = true
	}
	for _, name := range []string{"Go", "Linux", "计算机网络", "操作系统", "数据结构与算法", "MySQL", "PostgreSQL", "Redis", "软件设计", "大模型基础"} {
		if !names[name] {
			t.Errorf("missing complete ability %s", name)
		}
	}
	for _, r := range current.Analysis.Requirements {
		for _, o := range r.Options {
			if !abilityidentity.IsAtomicLabel(o.RawLabel) {
				t.Errorf("composite extraction %q", o.RawLabel)
			}
			if o.RawLabel == "UML" && r.RequirementKind != "preferred" {
				t.Error("UML preference was lost")
			}
			if strings.Contains(o.Qualifier, "基础认知") && r.RequirementKind == "preferred" {
				t.Errorf("basic awareness incorrectly marked preferred: %s", o.RawLabel)
			}
		}
	}
	t.Logf("confirmed abilities: %v", names)
	var proposed string
	rows, err := db.QueryContext(ctx, `SELECT proposed_name FROM ability_review_requests WHERE review_type='ability' ORDER BY proposed_name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		if err = rows.Scan(&proposed); err != nil {
			t.Fatal(err)
		}
		t.Logf("pending candidate: %s", proposed)
		if !abilityidentity.IsAtomicLabel(proposed) {
			t.Errorf("composite reached review: %s", proposed)
		}
	}
}

type boundedJDTransport struct {
	base       http.RoundTripper
	dispatched atomic.Int32
}

func (t *boundedJDTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.dispatched.Add(1) > 8 {
		return nil, errors.New("live JD acceptance request budget exhausted")
	}
	return t.base.RoundTrip(req)
}
