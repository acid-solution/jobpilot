package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/LeoninCS/jobpilot-next/backend/internal/jdanalysis"
	"github.com/LeoninCS/jobpilot-next/backend/internal/jdimport"
	"github.com/LeoninCS/jobpilot-next/backend/internal/modelconfig"
	"github.com/google/uuid"
)

func TestJDImportSelectionIsolationAndIdempotency(t *testing.T) {
	dsn := os.Getenv("JOBPILOT_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires isolated PostgreSQL")
	}
	ctx := t.Context()
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	user, target := uuid.New(), uuid.New()
	if _, err = db.ExecContext(ctx, `INSERT INTO job_targets(id,user_id,title,employment_type,directions,catalog_status)VALUES($1,$2,'导入测试','internship','[]','valid')`, target, user); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM job_descriptions WHERE user_id=$1`, user)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM job_targets WHERE id=$1`, target)
	}()
	r := NewJDImportRepository(db)
	session, err := r.Create(ctx, user, target, "甲公司后端岗位\n负责 Go 服务开发。\n乙公司前端岗位\n负责 React 页面开发。", nil)
	if err != nil {
		t.Fatal(err)
	}
	job, err := r.Claim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	candidates := []jdimport.Candidate{{Title: "后端岗位", RawText: "甲公司后端岗位\n负责 Go 服务开发。"}, {Title: "前端岗位", RawText: "乙公司前端岗位\n负责 React 页面开发。"}}
	if err = r.Complete(ctx, job, candidates); err != nil {
		t.Fatal(err)
	}
	var before int
	if err = db.QueryRowContext(ctx, `SELECT count(*)FROM job_descriptions WHERE user_id=$1`, user).Scan(&before); err != nil || before != 0 {
		t.Fatal("discovery created formal JDs before selection")
	}
	if _, err = r.Confirm(ctx, uuid.New(), session.ID, []uuid.UUID{candidates[0].ID}); !errors.Is(err, jdimport.ErrNotFound) {
		t.Fatalf("foreign confirmation: %v", err)
	}
	first, err := r.Confirm(ctx, user, session.ID, []uuid.UUID{candidates[1].ID})
	if err != nil {
		t.Fatal(err)
	}
	second, err := r.Confirm(ctx, user, session.ID, []uuid.UUID{candidates[1].ID})
	if err != nil {
		t.Fatal(err)
	}
	if first.Candidates[0].JDID != nil || first.Candidates[1].JDID == nil || *first.Candidates[1].JDID != *second.Candidates[1].JDID {
		t.Fatalf("selection or idempotency lost: %#v", second)
	}
	var count int
	_ = db.QueryRowContext(ctx, `SELECT count(*)FROM job_descriptions WHERE user_id=$1`, user).Scan(&count)
	if count != 1 {
		t.Fatalf("duplicate formal rows: %d", count)
	}
}

type stagedIntegrationAnalyzer struct {
	extractions, classifications int
	class                        jdanalysis.JobClassification
}

func TestJDImportReopensDeletedAndUnselectedCandidates(t *testing.T) {
	db := agentTransactionDatabase(t)
	ctx := t.Context()
	user, target := uuid.New(), uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO job_targets(id,user_id,title,employment_type,directions,catalog_status) VALUES($1,$2,'重复导入','internship','[]','valid')`, target, user); err != nil {
		t.Fatal(err)
	}
	repo := NewJDImportRepository(db)
	for _, count := range []int{1, 2} {
		raw := fmt.Sprintf("重新粘贴材料 %d", count)
		session, err := repo.Create(ctx, user, target, raw, nil)
		if err != nil {
			t.Fatal(err)
		}
		job, err := repo.Claim(ctx)
		if err != nil {
			t.Fatal(err)
		}
		candidates := make([]jdimport.Candidate, count)
		for i := range candidates {
			candidates[i] = jdimport.Candidate{Title: fmt.Sprintf("岗位%d", i), RawText: fmt.Sprintf("材料%d岗位%d负责接口开发。", count, i)}
		}
		if err = repo.Complete(ctx, job, candidates); err != nil {
			t.Fatal(err)
		}
		first, err := repo.Confirm(ctx, user, session.ID, []uuid.UUID{candidates[0].ID})
		if err != nil {
			t.Fatal(err)
		}
		original := *first.Candidates[0].JDID
		if count == 2 {
			reopened, err := repo.Create(ctx, user, target, raw, nil)
			if err != nil || reopened.Status != "ready" || reopened.Candidates[0].JDID == nil || reopened.Candidates[1].JDID != nil {
				t.Fatalf("unselected candidate unavailable: %#v %v", reopened, err)
			}
			if _, err = repo.Confirm(ctx, user, session.ID, []uuid.UUID{candidates[1].ID}); err != nil {
				t.Fatal(err)
			}
		}
		if _, err = db.ExecContext(ctx, `DELETE FROM job_descriptions WHERE id=$1 AND user_id=$2`, original, user); err != nil {
			t.Fatal(err)
		}
		reopened, err := repo.Create(ctx, user, target, raw, nil)
		if err != nil {
			t.Fatal(err)
		}
		if reopened.ID != session.ID {
			t.Fatal("recognition checkpoint was discarded")
		}
		if count == 2 {
			if reopened.Status != "ready" || reopened.Candidates[0].JDID != nil || reopened.Candidates[1].JDID == nil {
				t.Fatalf("deleted candidate not selectable: %#v", reopened)
			}
			reopened, err = repo.Confirm(ctx, user, session.ID, []uuid.UUID{candidates[0].ID})
			if err != nil {
				t.Fatal(err)
			}
		}
		if reopened.Status != "confirmed" || reopened.Candidates[0].JDID == nil || *reopened.Candidates[0].JDID == original {
			t.Fatalf("deleted JD not re-created: %#v", reopened)
		}
		repeat, err := repo.Create(ctx, user, target, raw, nil)
		if err != nil || *repeat.Candidates[0].JDID != *reopened.Candidates[0].JDID {
			t.Fatalf("replay created another JD: %#v %v", repeat, err)
		}
		var total int
		if err = db.QueryRowContext(ctx, `SELECT count(*) FROM job_descriptions WHERE user_id=$1 AND raw_text LIKE $2`, user, fmt.Sprintf("材料%d%%", count)).Scan(&total); err != nil || total != count {
			t.Fatalf("formal count=%d want=%d err=%v", total, count, err)
		}
	}
}

func (a *stagedIntegrationAnalyzer) AnalyzeJD(context.Context, string, string, string, jdanalysis.Catalog) (jdanalysis.Result, error) {
	panic("legacy analyzer must not run")
}
func (a *stagedIntegrationAnalyzer) ExtractJD(context.Context, string, string, string) (jdanalysis.Result, error) {
	a.extractions++
	return jdanalysis.Result{DocumentType: jdanalysis.DocumentJobDescription, ValidationStatus: jdanalysis.ValidationValid, Title: "后端实习", Company: "测试公司", EmploymentType: "internship", PromptVersion: jdanalysis.PromptVersion, Responsibilities: []string{"负责 Go 服务开发"}, Conditions: []string{}, AbilityRequirements: []jdanalysis.AbilityRequirement{{Operator: jdanalysis.RequirementSingle, RequiredCount: 1, RequirementKind: "required", Evidence: "熟悉 Go", Options: []jdanalysis.AbilityRequirementOption{{RawLabel: "Go", AbilityName: "Go", Evidence: "Go"}}}}}, nil
}
func (a *stagedIntegrationAnalyzer) ClassifyJD(context.Context, string, string, string, jdanalysis.Result, jdanalysis.Catalog) ([]jdanalysis.JobClassification, error) {
	a.classifications++
	if a.classifications == 1 {
		return nil, jdanalysis.NewError("model_rate_limited", true, errors.New("synthetic rate limit"))
	}
	return []jdanalysis.JobClassification{a.class}, nil
}

type stagedIntegrationCredentials struct{}

func (stagedIntegrationCredentials) Credentials(context.Context, uuid.UUID) (modelconfig.Credentials, error) {
	return modelconfig.Credentials{APIKey: "local-mock", Model: "local-mock"}, nil
}

type unreachableNormalizationModel struct{}

func (unreachableNormalizationModel) GenerateJSON(context.Context, string, string, string, string, any) error {
	return errors.New("exact Go match must not call model")
}

func TestStagedWorkerPersistsAbilitiesAndRetriesOnlyClassification(t *testing.T) {
	db := agentTransactionDatabase(t)
	ctx := t.Context()
	user, target := uuid.New(), uuid.New()
	repo := NewAnalysisRepository(db)
	catalog, err := repo.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	category := catalog.JobCategories[0]
	specialty := category.Specialties[0]
	_, err = db.ExecContext(ctx, `INSERT INTO job_targets(id,user_id,title,employment_type,directions,catalog_status)VALUES($1,$2,'阶段恢复','internship','[]','valid')`, target, user)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO target_directions(target_id,category_id,specialty_id,sort_order)SELECT $1,category.id,specialty.id,1 FROM job_categories category JOIN job_specialties specialty ON specialty.category_id=category.id WHERE specialty.code=$2`, target, specialty.Code)
	if err != nil {
		t.Fatal(err)
	}
	marketRepo := NewMarketRepository(db)
	raw := "测试公司招聘后端实习，负责 Go 服务开发，熟悉 Go"
	jd, err := marketRepo.CreateWithAnalysisJob(ctx, user, target, raw, sourceHash(raw))
	if err != nil {
		t.Fatal(err)
	}
	analyzer := &stagedIntegrationAnalyzer{class: jdanalysis.JobClassification{CategoryCode: category.Code, SpecialtyCode: specialty.Code, Relation: "primary", Evidence: "负责 Go 服务开发", Reason: "测试职责证据"}}
	worker := jdanalysis.NewWorker(repo, stagedIntegrationCredentials{}, analyzer, time.Second)
	worker.SetNormalizer(&jdanalysis.VectorNormalizer{Model: unreachableNormalizationModel{}})
	if _, err = worker.ProcessOnce(ctx); err != nil {
		t.Fatal(err)
	}
	stored, err := marketRepo.FindByID(ctx, user, jd.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Title != "后端实习" || len(stored.AbilityMentions) != 1 || !stored.Analysis.HasPending {
		t.Fatalf("partial facts or automatic retry lost: %#v", stored)
	}
	var links int
	if err = db.QueryRowContext(ctx, `SELECT count(*)FROM job_description_ability_requirements WHERE job_description_id=$1`, jd.ID).Scan(&links); err != nil || links != 1 {
		t.Fatalf("independent abilities not saved: %d %v", links, err)
	}
	if _, err = marketRepo.RetryAnalysis(ctx, user, jd.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = worker.ProcessOnce(ctx); err != nil {
		t.Fatal(err)
	}
	stored, err = marketRepo.FindByID(ctx, user, jd.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.JobStatus != "succeeded" || analyzer.extractions != 1 || analyzer.classifications != 2 {
		t.Fatalf("stage retry replayed success: extractions=%d classifications=%d result=%#v", analyzer.extractions, analyzer.classifications, stored)
	}
}

func TestImportRecoverySameSourceReplacementAndEmptyMaterial(t *testing.T) {
	db := agentTransactionDatabase(t)
	ctx := t.Context()
	user, target := uuid.New(), uuid.New()
	_, err := db.ExecContext(ctx, `INSERT INTO job_targets(id,user_id,title,employment_type,directions,catalog_status)VALUES($1,$2,'恢复测试','internship','[]','valid')`, target, user)
	if err != nil {
		t.Fatal(err)
	}
	marketRepo := NewMarketRepository(db)
	raw := "招聘 Go 后端实习，负责服务开发，熟悉 Go"
	jd, err := marketRepo.CreateWithAnalysisJob(ctx, user, target, raw, sourceHash(raw))
	if err != nil {
		t.Fatal(err)
	}
	analysis := NewAnalysisRepository(db)
	old, err := analysis.Claim(ctx, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	imports := NewJDImportRepository(db)
	session, err := imports.Create(ctx, user, target, raw, &jd.ID)
	if err != nil {
		t.Fatal(err)
	}
	job, err := imports.Claim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cs := []jdimport.Candidate{{Title: "后端实习", RawText: raw}}
	if err = imports.SaveWindow(ctx, job, 0, cs); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE jd_imports SET lease_expires_at=now()-interval '1 minute' WHERE id=$1`, job.ID); err != nil {
		t.Fatal(err)
	}
	if err = imports.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	next, err := imports.Claim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := imports.LoadWindow(ctx, next, 0); err != nil || !found {
		t.Fatal("successful source window lost after restart")
	}
	if err = imports.SaveWindow(ctx, job, 0, cs); !errors.Is(err, jdimport.ErrLeaseLost) {
		t.Fatalf("stale import window accepted: %v", err)
	}
	if err = imports.Complete(ctx, next, cs); err != nil {
		t.Fatal(err)
	}
	if err = imports.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	done, err := imports.Get(ctx, user, session.ID)
	if err != nil || done.Status != "confirmed" || done.Candidates[0].JDID == nil || *done.Candidates[0].JDID != jd.ID {
		t.Fatalf("auto-confirm recovery changed ID: %v %#v", err, done)
	}
	if err = analysis.SaveStage(ctx, old, "extraction", "succeeded", jdanalysis.Result{}, ""); !errors.Is(err, jdanalysis.ErrLeaseLost) {
		t.Fatalf("replacement failed to fence old worker: %v", err)
	}
	if _, err = imports.Confirm(ctx, user, session.ID, []uuid.UUID{cs[0].ID}); err != nil {
		t.Fatal(err)
	}
	var count int
	_ = db.QueryRowContext(ctx, `SELECT count(*)FROM job_descriptions WHERE user_id=$1`, user).Scan(&count)
	if count != 1 {
		t.Fatal("replacement or repeated confirmation created extra JD")
	}
	empty, err := imports.Create(ctx, user, target, "小说：夜雨打湿了旧城，归人推开门，故事尚未结束。", nil)
	if err != nil {
		t.Fatal(err)
	}
	emptyJob, err := imports.Claim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = imports.Complete(ctx, emptyJob, nil); err != nil {
		t.Fatal(err)
	}
	empty, err = imports.Get(ctx, user, empty.ID)
	if err != nil || empty.Status != "empty" || empty.HasPending {
		t.Fatalf("non JD must be terminal normal result: %v %#v", err, empty)
	}
	if _, err = imports.Confirm(ctx, user, empty.ID, []uuid.UUID{uuid.New()}); !errors.Is(err, jdimport.ErrConflict) {
		t.Fatalf("negative material created a JD: %v", err)
	}
}

func TestJDCheckpointSurvivesFailureAndRejectsStaleWrites(t *testing.T) {
	dsn := os.Getenv("JOBPILOT_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires isolated PostgreSQL")
	}
	ctx := t.Context()
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	user, target := uuid.New(), uuid.New()
	_, err = db.ExecContext(ctx, `INSERT INTO job_targets(id,user_id,title,employment_type,directions,catalog_status)VALUES($1,$2,'阶段测试','internship','[]','valid')`, target, user)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM job_descriptions WHERE user_id=$1`, user)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM job_targets WHERE id=$1`, target)
	}()
	marketRepo := NewMarketRepository(db)
	raw := "阶段测试后端岗位，负责开发订单接口，熟悉 Go。"
	jd, err := marketRepo.CreateWithAnalysisJob(ctx, user, target, raw, sourceHash(raw))
	if err != nil {
		t.Fatal(err)
	}
	r := NewAnalysisRepository(db)
	job, err := r.Claim(ctx, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if job.JobDescriptionID != jd.ID {
		t.Fatal("unexpected test job")
	}
	result := jdanalysis.Result{Title: "后端岗位", Company: "测试公司", DocumentType: jdanalysis.DocumentJobDescription, ValidationStatus: jdanalysis.ValidationValid, EmploymentType: "internship", Responsibilities: []string{"负责开发订单接口"}, Conditions: []string{}}
	if err = r.SaveStage(ctx, job, "extraction", "succeeded", result, ""); err != nil {
		t.Fatal(err)
	}
	if err = r.Fail(ctx, job, "classification_unresolved", false); err != nil {
		t.Fatal(err)
	}
	stored, err := marketRepo.FindByID(ctx, user, jd.ID)
	if err != nil || stored.Title != "后端岗位" || len(stored.Responsibilities) != 1 {
		t.Fatalf("successful content discarded: %v %#v", err, stored)
	}
	if _, err = marketRepo.RetryAnalysis(ctx, user, jd.ID); err != nil {
		t.Fatal(err)
	}
	next, err := r.Claim(ctx, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	_, found, err := r.LoadStage(ctx, next, "extraction")
	if err != nil || !found {
		t.Fatal("checkpoint was not reused")
	}
	if err = r.SaveStage(ctx, job, "classification", "succeeded", result, ""); !errors.Is(err, jdanalysis.ErrLeaseLost) {
		t.Fatalf("stale writer accepted: %v", err)
	}
}
