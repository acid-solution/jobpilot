package postgres

import (
	"encoding/json"
	"github.com/LeoninCS/jobpilot-next/backend/internal/modelusage"
	"github.com/LeoninCS/jobpilot-next/backend/internal/storage/postgres/migrations"
	"github.com/google/uuid"
	"strings"
	"testing"
	"time"
)

func TestModelUsageIsolationSharingAndImmutablePriceIntegration(t *testing.T) {
	db := agentTransactionDatabase(t)
	ctx := t.Context()
	r := NewModelUsageRepository(db)
	user, other := uuid.New(), uuid.New()
	operation := uuid.New()
	s := modelusage.Scope{UserID: user, OperationID: operation, Purpose: "agent", Payer: "user"}
	call := modelusage.Call{ID: uuid.New(), Scope: s, Provider: "deepseek", Payer: "user", Purpose: s.Purpose, StartedAt: time.Now(), RequestedModel: "test"}
	if err := r.Begin(ctx, call); err != nil {
		t.Fatal(err)
	}
	input, output, total, hit, miss := int64(10000), int64(1000), int64(11000), int64(8000), int64(2000)
	call.Tokens = modelusage.Tokens{Input: &input, Output: &output, Total: &total, CacheHit: &hit, CacheMiss: &miss}
	call.ReturnedModel = "test"
	call.Result = "provider_success"
	call.Confirmed = true
	call.UsageStatus = "known"
	price := modelusage.Pricing{Version: "test-rates", Prices: []modelusage.Price{{Provider: "deepseek", Model: "test", Currency: "CNY", Input: "1", CacheHit: "0.1", Output: "2"}}}
	price.Apply(&call)
	if err := r.Finish(ctx, call); err != nil {
		t.Fatal(err)
	}
	cost := "999"
	call.Cost = &cost
	if err := r.Finish(ctx, call); err != nil {
		t.Fatal(err)
	}
	f := modelusage.Filter{From: time.Now().Add(-time.Hour), To: time.Now().Add(time.Hour), Page: 1, PageSize: 20}
	summary, err := r.Summary(ctx, user, f)
	if err != nil || len(summary) != 1 || summary[0].Costs["CNY"] != "0.004800000000" || summary[0].CallCount != 1 {
		t.Fatal("incorrect cost/duplicate finalize", summary, err)
	}
	hidden, err := r.Calls(ctx, other, f)
	if err != nil || hidden.Total != 0 {
		t.Fatal("cross-account leak", err)
	}
	page, err := r.Calls(ctx, user, f)
	if err != nil || page.Total != 1 || page.Items[0].CacheHit == nil || *page.Items[0].CacheHit != 8000 {
		t.Fatal("nullable metadata not restored", err)
	}
	if !json.Valid(page.Items[0].PriceSnapshot) {
		t.Fatal("price snapshot missing")
	}
	// Recording metadata is independent of business result leases and rollback.
	unknown := call
	unknown.ID = uuid.New()
	unknown.Tokens = modelusage.Tokens{}
	unknown.UsageStatus = "unknown"
	unknown.Cost = nil
	if err = r.Begin(ctx, unknown); err != nil {
		t.Fatal(err)
	}
	summary, err = r.Summary(ctx, user, f)
	if err != nil || summary[0].UnknownUsageCount != 1 || summary[0].UnknownCostCount != 1 {
		t.Fatal("unknown converted into zero", err)
	}
	third := uuid.New()
	if err = r.EnsureOperation(ctx, modelusage.Scope{UserID: third, OperationID: uuid.New(), Purpose: "learning_route"}); err != nil {
		t.Fatal(err)
	}
	ops, err := r.Operations(ctx, third, f)
	if err != nil || ops.Total != 1 || ops.Items[0].CallCount != 0 {
		t.Fatal("model-free operation missing", err)
	}
	// A completed shared review linked later by another user's JD exposes the
	// same call without another supplier request or another ledger entry.
	request := uuid.New()
	_, err = db.ExecContext(ctx, `INSERT INTO ability_review_requests(id,candidate_key,normalized_name,proposed_name,initiated_by_user_id)VALUES($1,$2,'test','Test',$3)`, request, request.String(), user)
	if err != nil {
		t.Fatal(err)
	}
	shared := modelusage.Call{ID: uuid.New(), Scope: modelusage.Scope{UserID: user, OperationID: request, Purpose: "ability_review"}, Provider: "deepseek", Payer: "platform", Purpose: "ability_review", StartedAt: time.Now(), RequestedModel: "test"}
	if err = r.Begin(ctx, shared); err != nil {
		t.Fatal(err)
	}
	shared.Tokens = modelusage.Tokens{Input: &input, Output: &output}
	shared.UsageStatus = "partial"
	shared.Result = "provider_success"
	shared.Confirmed = true
	if err = r.Finish(ctx, shared); err != nil {
		t.Fatal(err)
	}
	target, jd, req := uuid.New(), uuid.New(), uuid.New()
	for _, statement := range []struct {
		q    string
		args []any
	}{
		{`INSERT INTO job_targets(id,user_id,title,employment_type)VALUES($1,$2,'后端','internship')`, []any{target, other}},
		{`INSERT INTO job_descriptions(id,user_id,target_id,raw_text,raw_text_hash,status)VALUES($1,$2,$3,'测试岗位要求',repeat('a',64),'processing')`, []any{jd, other, target}},
		{`INSERT INTO job_description_ability_requirements(id,job_description_id,operator,required_count,evidence,sort_order)VALUES($1,$2,'single',1,'Test',0)`, []any{req, jd}},
		{`INSERT INTO job_description_ability_requirement_options(requirement_id,raw_label,evidence,sort_order,review_request_id)VALUES($1,'Test','Test',0,$2)`, []any{req, request}},
	} {
		if _, err = db.ExecContext(ctx, statement.q, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	otherCalls, err := r.Calls(ctx, other, f)
	if err != nil || otherCalls.Total != 1 || otherCalls.Items[0].ID != shared.ID {
		t.Fatal("shared review not linked", err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE job_description_ability_requirement_options SET review_request_id=$1 WHERE requirement_id=$2`, request, req); err != nil {
		t.Fatal(err)
	}
	otherSummary, err := r.Summary(ctx, other, f)
	if err != nil || otherSummary[0].CallCount != 1 {
		t.Fatal("shared association duplicated consumption", err)
	}
	f.OperationID = page.Items[0].OperationID
	hidden, err = r.Calls(ctx, other, f)
	if err != nil || hidden.Total != 0 {
		t.Fatal("operation filter bypassed isolation")
	}
	t.Run("historical backfill is idempotent and preserves unknowns", func(t *testing.T) {
		legacyUser, good, bad := uuid.New(), uuid.New(), uuid.New()
		legacyRequest := uuid.New()
		_, err := db.ExecContext(ctx, `INSERT INTO ability_review_requests(id,candidate_key,normalized_name,proposed_name,initiated_by_user_id)VALUES($1,$2,'history','History',$3)`, legacyRequest, legacyRequest.String(), legacyUser)
		if err != nil {
			t.Fatal(err)
		}
		_, err = db.ExecContext(ctx, `INSERT INTO platform_model_usage(id,request_id,user_id,purpose,provider,model,status,input_tokens,output_tokens,provider_request_id) VALUES($1,$3,$4,'ability_review','deepseek','test','succeeded',7,3,'old-response'),($2,$3,$4,'ability_review','deepseek','test','failed',NULL,NULL,NULL)`, good, bad, legacyRequest, legacyUser)
		if err != nil {
			t.Fatal(err)
		}
		trackedUsage := uuid.New()
		if _, err = db.ExecContext(ctx, `INSERT INTO platform_model_usage(id,request_id,user_id,purpose,provider,model,status,input_tokens,output_tokens)VALUES($1,$2,$3,'ability_review','deepseek','test','succeeded',7,3)`, trackedUsage, legacyRequest, legacyUser); err != nil {
			t.Fatal(err)
		}
		tracked := modelusage.Call{ID: uuid.New(), Scope: modelusage.Scope{UserID: legacyUser, OperationID: legacyRequest, Purpose: "ability_review", LegacyUsageID: trackedUsage}, Provider: "deepseek", Payer: "platform", Purpose: "ability_review", StartedAt: time.Now()}
		if err = r.Begin(ctx, tracked); err != nil {
			t.Fatal(err)
		}
		tracked.Confirmed = true
		if err = r.Finish(ctx, tracked); err != nil {
			t.Fatal(err)
		}
		raw, err := migrations.Files.ReadFile("sql/00038_model_usage.sql")
		if err != nil {
			t.Fatal(err)
		}
		script := string(raw)
		script = script[strings.Index(script, "INSERT INTO model_usage_calls(id,provider,requested_model,payer"):strings.Index(script, "-- +goose Down")]
		for range 2 {
			if _, err = db.ExecContext(ctx, script); err != nil {
				t.Fatal(err)
			}
		}
		filter := f
		filter.OperationID = uuid.Nil
		calls, err := r.Calls(ctx, legacyUser, filter)
		if err != nil || calls.Total != 3 {
			t.Fatal("duplicate history backfill", err)
		}
		for _, c := range calls.Items {
			if c.ID == tracked.ID {
				if c.Legacy {
					t.Fatal("live ledger marked as history")
				}
				continue
			}
			if c.Cost != nil || c.CacheHit != nil || !c.Legacy {
				t.Fatal("historical cost/cache invented")
			}
			if c.ID == bad && (c.Confirmed || c.Input != nil) {
				t.Fatal("failed dispatch claimed as known")
			}
		}
	})
}
