package postgres

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/LeoninCS/jobpilot-next/backend/internal/jdanalysis"
	"github.com/pressly/goose/v3"
)

func TestFailedExtractionPreservesFactsAndRepairsLegacyMetadataIsolated(t *testing.T) {
	db := agentTransactionDatabase(t, 46)
	ctx := t.Context()
	user, _, jd := granularityJD(t, db, "后端实习\n负责Go服务开发，熟悉Go。")
	repo := NewAnalysisRepository(db)
	job, err := repo.Claim(ctx, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	old := jdanalysis.Result{DocumentType: jdanalysis.DocumentJobDescription, ValidationStatus: jdanalysis.ValidationValid, Title: "后端实习", Company: "测试", EmploymentType: "internship", Responsibilities: []string{"负责Go服务开发，熟悉Go。"}, Conditions: []string{"在校生"}, PromptVersion: "jd-source-stages-v15"}
	for _, stage := range []string{"extraction_window_0", "extraction"} {
		if err = repo.SaveStage(ctx, job, stage, "succeeded", old, ""); err != nil {
			t.Fatal(err)
		}
	}
	failed := jdanalysis.Result{DocumentType: jdanalysis.DocumentNonJobDescription, ValidationStatus: jdanalysis.ValidationInvalid, ValidationReason: "未识别到有效岗位职责或招聘要求", Title: "未明确", Company: "未明确", EmploymentType: "unknown", Responsibilities: []string{}, Conditions: []string{}}
	if err = repo.SaveStage(ctx, job, "extraction", "failed", failed, "model_invalid_response", true); err != nil {
		t.Fatal(err)
	}
	current, err := NewMarketRepository(db).FindByID(ctx, user, jd)
	if err != nil || current.ValidationStatus != "valid" || current.Title != old.Title || len(current.Responsibilities) != 1 || current.Conditions != "在校生" {
		t.Fatalf("failed stage overwrote successful facts: %v %#v", err, current)
	}
	job.PreservePreviousResult = true
	if err = repo.Fail(ctx, job, "model_invalid_response", false); err != nil {
		t.Fatal(err)
	}
	// Reproduce the pre-fix corruption without dispatching a model request.
	if _, err = db.Exec(`UPDATE job_descriptions SET title='未明确',company='未明确',responsibilities='[]',conditions='',document_type='non_job_description',validation_status='invalid',validation_reason='未识别到有效岗位职责或招聘要求',status='included' WHERE id=$1`, jd); err != nil {
		t.Fatal(err)
	}
	_, _, noPrevious := granularityJD(t, db, "另一份后端招聘\n负责后端服务开发。")
	_, _, genuinelyInvalid := granularityJD(t, db, "这是一份用于对照的非招聘测试内容")
	data, _ := json.Marshal(stagePayload{Result: failed})
	for _, item := range []struct {
		id     string
		status string
	}{{noPrevious.String(), "failed"}, {genuinelyInvalid.String(), "succeeded"}} {
		if _, err = db.Exec(`UPDATE job_descriptions SET validation_status='invalid',document_type='non_job_description' WHERE id=$1`, item.id); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(`UPDATE analysis_jobs SET status=$2,last_error='model_invalid_response' WHERE job_description_id=$1`, item.id, item.status); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(`INSERT INTO jd_analysis_stages(job_description_id,source_hash,stage,status,result,error_code,retryable) SELECT id,encode(digest(raw_text,'sha256'),'hex'),'extraction',$2,$3,'model_invalid_response',true FROM job_descriptions WHERE id=$1`, item.id, item.status, data); err != nil {
			t.Fatal(err)
		}
	}
	if err = goose.UpTo(db, "sql", 47); err != nil {
		t.Fatal(err)
	}
	current, err = NewMarketRepository(db).FindByID(ctx, user, jd)
	if err != nil || current.ValidationStatus != "valid" || current.Title != old.Title || current.Company != old.Company || len(current.Responsibilities) != 1 || current.Conditions != "在校生" || current.JobStatus != "failed" {
		t.Fatalf("legacy success was not restored or failure was hidden: %v %#v", err, current)
	}
	var pending, invalid string
	if err = db.QueryRow(`SELECT validation_status FROM job_descriptions WHERE id=$1`, noPrevious).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT validation_status FROM job_descriptions WHERE id=$1`, genuinelyInvalid).Scan(&invalid); err != nil {
		t.Fatal(err)
	}
	if pending != "pending" || invalid != "invalid" {
		t.Fatalf("fabricated a result or undid a genuine negative decision: %s %s", pending, invalid)
	}
	var queued int
	if err = db.QueryRow(`SELECT count(*) FROM analysis_jobs WHERE status IN('queued','running')`).Scan(&queued); err != nil || queued != 0 {
		t.Fatalf("repair requeued paid work: %v count=%d", err, queued)
	}
}
