package postgres

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
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
	mode := os.Getenv("RUN_JD_GRANULARITY_LIVE")
	if mode != "1" && mode != "followup" && mode != "normalization_only" {
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
	endpoint, err := url.Parse(base)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host != "api.deepseek.com" || endpoint.User != nil {
		t.Fatal("live JD acceptance is authorized only for https://api.deepseek.com")
	}
	limit := 8
	if configured := os.Getenv("JD_GRANULARITY_REQUEST_LIMIT"); configured != "" {
		limit, err = strconv.Atoi(configured)
		if err != nil || limit < 1 || limit > 8 {
			t.Fatal("JD_GRANULARITY_REQUEST_LIMIT must be between 1 and 8")
		}
	}
	if mode != "1" {
		limit = min(limit, 3)
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
	budget := &boundedJDTransport{base: transport, limit: int32(limit)}
	defer func() {
		t.Logf("HTTP requests sent=%d/%d input_tokens=%d output_tokens=%d", min(budget.dispatched.Load(), int32(limit)), limit, transport.Input, transport.Output)
		if t.Failed() {
			for i, content := range budget.responses {
				t.Logf("model response %d: %s", i+1, strings.ReplaceAll(content, key, "[redacted]"))
			}
		}
	}()
	client := deepseek.NewClient(base, &http.Client{Transport: budget, Timeout: 150 * time.Second})
	repo := NewAnalysisRepository(db)
	if mode != "1" {
		testDASJDGranularityFollowup(t, ctx, repo, client, key, model, string(source), mode == "followup")
		return
	}
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
		t.Logf("classification: %s/%s %s: %s", c.CategoryCode, c.SpecialtyCode, c.Relation, c.Reason)
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
			t.Logf("requirement: %s -> %s [%s] qualifier=%s", o.RawLabel, o.AbilityName, r.RequirementKind, o.Qualifier)
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

// Follow-up rechecks only the two failed stages and five unresolved labels from
// the first live run. It is not a second complete extraction/pipeline evaluation.
func testDASJDGranularityFollowup(t *testing.T, ctx context.Context, repo *AnalysisRepository, client *deepseek.Client, key, model, raw string, classify bool) {
	t.Helper()
	catalog, err := repo.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	quote := func(needle string) string {
		t.Helper()
		for _, line := range strings.Split(raw, "\n") {
			if strings.Contains(line, needle) {
				return strings.TrimSpace(line)
			}
		}
		t.Fatalf("fixture is missing source clause %q", needle)
		return ""
	}
	input := jdanalysis.Result{Title: "go开发实习", DocumentType: jdanalysis.DocumentJobDescription, ValidationStatus: jdanalysis.ValidationValid}
	for _, phrase := range []string{"参与防火墙产品", "负责产品功能模块", "完成功能模块", "协助团队探索AI"} {
		input.Responsibilities = append(input.Responsibilities, quote(phrase))
	}
	if classify {
		classes, err := client.ClassifyJD(ctx, key, model, raw, input, catalog)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range classes {
			t.Logf("follow-up classification: %s/%s %s: %s", c.CategoryCode, c.SpecialtyCode, c.Relation, c.Reason)
			if c.SpecialtyCode == "ai-agent-application" {
				t.Error("generic AI prototype still misclassified as Agent")
			}
		}
	}
	for _, item := range []struct{ label, source, qualifier, kind string }{
		{"API开发", "熟悉前后端分离架构", "掌握API开发思想及相关开发工具", "required"},
		{"数通知识", "具备扎实的数通知识", "具备扎实者优先", "preferred"},
		{"AI接口调用", "了解大语言模型", "有实践者优先", "preferred"},
		{"AI", "了解大语言模型", "在网络安全、流量识别、日志分析、异常检测等方向有基础认知", "required"},
		{"机器学习", "了解大语言模型", "了解基础概念", "required"},
	} {
		evidence := quote(item.source)
		if !strings.Contains(evidence, item.label) {
			t.Fatalf("follow-up label %s is not in its source", item.label)
		}
		input.AbilityRequirements = append(input.AbilityRequirements, jdanalysis.AbilityRequirement{
			Operator: jdanalysis.RequirementSingle, RequiredCount: 1, RequirementKind: item.kind, Evidence: evidence,
			Options: []jdanalysis.AbilityRequirementOption{{RawLabel: item.label, AbilityName: item.label, Qualifier: item.qualifier, Evidence: evidence}},
		})
	}
	normalizer := &jdanalysis.VectorNormalizer{Model: client}
	result, err := normalizer.Normalize(ctx, uuid.New(), key, model, catalog, input)
	if err != nil {
		t.Fatal(err)
	}
	resolved := map[string]string{}
	for _, r := range result.AbilityRequirements {
		for _, o := range r.Options {
			resolved[o.RawLabel] = o.AbilityName
			if o.CatalogCode == "" {
				resolved[o.RawLabel] = "pending_review"
			}
			t.Logf("follow-up normalization: %s -> %s [%s, %s] %s", o.RawLabel, resolved[o.RawLabel], o.NormalizationRelation, r.RequirementKind, o.NormalizationReason)
		}
	}
	for rawLabel, expected := range map[string]string{"API开发": "软件设计", "数通知识": "计算机网络"} {
		if resolved[rawLabel] != expected {
			t.Errorf("scope not resolved: %s -> %s, expected %s", rawLabel, resolved[rawLabel], expected)
		}
	}
}

type boundedJDTransport struct {
	base       http.RoundTripper
	limit      int32
	dispatched atomic.Int32
	responses  []string
}

func (t *boundedJDTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme != "https" || req.URL.Host != "api.deepseek.com" {
		return nil, errors.New("unauthorized live JD acceptance destination")
	}
	if t.dispatched.Add(1) > t.limit {
		return nil, errors.New("live JD acceptance request budget exhausted")
	}
	response, err := t.base.RoundTrip(req)
	if err != nil {
		return response, err
	}
	data, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		return nil, err
	}
	response.Body = io.NopCloser(bytes.NewReader(data))
	var value struct {
		Choices []struct {
			Message struct{ Content string }
		}
	}
	if json.Unmarshal(data, &value) == nil && len(value.Choices) > 0 {
		t.responses = append(t.responses, value.Choices[0].Message.Content)
	}
	return response, nil
}
