package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/LeoninCS/jobpilot-next/backend/internal/jdanalysis"
	"github.com/google/uuid"
)

type historicalStageAnalyzer struct{ calls int }

func (a *historicalStageAnalyzer) ExtractJD(context.Context, string, string, string) (jdanalysis.Result, error) {
	a.calls++
	return jdanalysis.Result{}, errors.New("historical extraction must be reused")
}
func (a *historicalStageAnalyzer) ClassifyJD(context.Context, string, string, string, jdanalysis.Result, jdanalysis.Catalog) ([]jdanalysis.JobClassification, error) {
	a.calls++
	return nil, errors.New("historical classification must be reused")
}

type historicalStageNormalizer struct {
	code  string
	fail  bool
	calls int
}

func (n *historicalStageNormalizer) Normalize(_ context.Context, _ uuid.UUID, _, _ string, _ jdanalysis.Catalog, result jdanalysis.Result) (jdanalysis.Result, error) {
	n.calls++
	if n.fail {
		return result, jdanalysis.NewError("model_timeout", true, errors.New("timeout"))
	}
	for ri := range result.AbilityRequirements {
		for oi := range result.AbilityRequirements[ri].Options {
			option := &result.AbilityRequirements[ri].Options[oi]
			option.CatalogCode, option.AbilityName = n.code, "Go"
		}
	}
	result.AbilityMentions = []jdanalysis.AbilityMention{{Name: "Go", CatalogCode: n.code, Evidence: "Go"}}
	result.VectorNormalized = true
	return result, nil
}

func TestHistoricalJDNormalizationUsesExistingStagesIsolated(t *testing.T) {
	db := agentTransactionDatabase(t)
	ctx := context.Background()
	user, target := uuid.New(), uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO job_targets(id,user_id,title,employment_type,catalog_status) VALUES($1,$2,'后端开发','internship','valid');`, target, user); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO target_directions(target_id,category_id,sort_order) SELECT $1,id,1 FROM job_categories WHERE code='backend'`, target); err != nil {
		t.Fatal(err)
	}
	raw := "Go 后端开发实习生，负责后端服务开发，要求熟悉 Go。"
	markets := NewMarketRepository(db)
	jd, err := markets.CreateWithAnalysisJob(ctx, user, target, raw, sourceHash(raw))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE job_descriptions SET status='included',validation_status='valid',document_type='job_description',title='Go 后端开发实习生',employment_type='internship',responsibilities='["负责后端服务开发"]',analysis_prompt_version='old-parser' WHERE id=$1`, jd.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE analysis_jobs SET status='succeeded' WHERE job_description_id=$1`, jd.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO job_description_classifications(job_description_id,category_id,specialty_id,relation,evidence,reason) SELECT $1,category.id,specialty.id,'primary','负责后端服务开发','交付业务服务' FROM job_categories category JOIN job_specialties specialty ON specialty.category_id=category.id WHERE specialty.code='backend-business'`, jd.ID); err != nil {
		t.Fatal(err)
	}
	var requirement uuid.UUID
	if err = db.QueryRowContext(ctx, `INSERT INTO job_description_ability_requirements(job_description_id,operator,required_count,requirement_kind,evidence,sort_order) VALUES($1,'single',1,'required','熟悉 Go',1) RETURNING id`, jd.ID).Scan(&requirement); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO job_description_ability_requirement_options(requirement_id,ability_id,raw_label,evidence,sort_order,resolution_status) SELECT $1,id,'Go','Go',1,'resolved' FROM abilities WHERE name='Go' AND is_active`, requirement); err != nil {
		t.Fatal(err)
	}
	var option uuid.UUID
	var code string
	if err = db.QueryRowContext(ctx, `SELECT option.id,ability.code FROM job_description_ability_requirement_options option JOIN abilities ability ON ability.id=option.ability_id WHERE requirement_id=$1`, requirement).Scan(&option, &code); err != nil {
		t.Fatal(err)
	}
	var legacyJobs int
	if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM jd_normalization_jobs WHERE job_description_id=$1`, jd.ID).Scan(&legacyJobs); err != nil || legacyJobs != 0 {
		t.Fatalf("retired trigger still enqueues: %d %v", legacyJobs, err)
	}
	if _, err = markets.RetryAnalysis(ctx, user, jd.ID); err != nil {
		t.Fatal(err)
	}
	analyzer := &historicalStageAnalyzer{}
	normalizer := &historicalStageNormalizer{code: code, fail: true}
	worker := jdanalysis.NewWorker(NewAnalysisRepository(db), transactionTestCredentials{}, analyzer, time.Second)
	worker.SetNormalizer(normalizer)
	if worked, err := worker.ProcessOnce(ctx); err != nil || !worked {
		t.Fatalf("failed attempt: %v %v", worked, err)
	}
	var kept bool
	if err = db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM job_description_ability_requirement_options WHERE id=$1 AND resolution_status='resolved')`, option).Scan(&kept); err != nil || !kept {
		t.Fatalf("failed normalization discarded previous result: %v", err)
	}
	if _, err = markets.RetryAnalysis(ctx, user, jd.ID); err != nil {
		t.Fatal(err)
	}
	normalizer.fail = false
	if worked, err := worker.ProcessOnce(ctx); err != nil || !worked {
		t.Fatalf("successful retry: %v %v", worked, err)
	}
	var status, version string
	if err = db.QueryRowContext(ctx, `SELECT status,normalization_prompt_version FROM job_descriptions WHERE id=$1`, jd.ID).Scan(&status, &version); err != nil {
		t.Fatal(err)
	}
	if analyzer.calls != 0 || normalizer.calls != 2 || status != "included" || version != jdanalysis.NormalizationPromptVersion {
		t.Fatalf("replayed wrong stages: analyzer=%d normalization=%d status=%s version=%s", analyzer.calls, normalizer.calls, status, version)
	}
	var job jdanalysis.Job
	if err = db.QueryRowContext(ctx, `SELECT id FROM analysis_jobs WHERE job_description_id=$1`, jd.ID).Scan(&job.ID); err != nil {
		t.Fatal(err)
	}
	job.UserID, job.JobDescriptionID, job.TargetID, job.RawText, job.LeaseToken = user, jd.ID, target, raw, uuid.New()
	if err = NewAnalysisRepository(db).SaveStage(ctx, job, "ability_resolution", "succeeded", jdanalysis.Result{VectorNormalized: true}, ""); !errors.Is(err, jdanalysis.ErrLeaseLost) {
		t.Fatalf("old lease could replace current mapping: %v", err)
	}
}
