package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/LeoninCS/jobpilot-next/backend/internal/jdanalysis"
	"github.com/LeoninCS/jobpilot-next/backend/internal/market"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

type queryCounter struct{ calls atomic.Int32 }

func (q *queryCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	q.calls.Add(1)
	return ctx
}
func (*queryCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestJDListUsesConstantQueriesAndMatchesDetailIsolated(t *testing.T) {
	db := agentTransactionDatabase(t)
	ctx := t.Context()
	user, target := uuid.New(), uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO job_targets(id,user_id,title,employment_type) VALUES($1,$2,'批量查询测试','internship')`, target, user); err != nil {
		t.Fatal(err)
	}
	for i := range 10 {
		id := uuid.New()
		raw := fmt.Sprintf("岗位%d：负责 Go 服务开发", i)
		if _, err := db.ExecContext(ctx, `INSERT INTO job_descriptions(id,user_id,target_id,raw_text,raw_text_hash,status,validation_status,title,company) VALUES($1,$2,$3,$4,encode(digest($4,'sha256'),'hex'),'included','valid','Go 后端',$5)`, id, user, target, raw, fmt.Sprintf("公司%d", i)); err != nil {
			t.Fatal(err)
		}
		status := "succeeded"
		if i == 0 {
			status = "failed"
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO analysis_jobs(id,user_id,target_id,job_description_id,job_type,status,attempts,max_attempts) VALUES($1,$2,$3,$4,'jd_analysis',$5,1,3)`, uuid.New(), user, target, id, status); err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(stagePayload{Result: jdanalysis.Result{Warnings: []string{"原文保留"}, AbilityRequirements: []jdanalysis.AbilityRequirement{{Operator: "single", RequiredCount: 1, Evidence: raw}}}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.ExecContext(ctx, `INSERT INTO jd_analysis_stages(job_description_id,source_hash,stage,status,result) VALUES($1,$2,'extraction','succeeded',$3)`, id, sourceHash(raw), data); err != nil {
			t.Fatal(err)
		}
	}
	var name string
	if err := db.QueryRowContext(ctx, `SELECT current_database()`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(os.Getenv("JOBPILOT_ADMIN_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	config, err := pgx.ParseConfig(u.String())
	if err != nil {
		t.Fatal(err)
	}
	counter := &queryCounter{}
	config.Tracer = counter
	traced := stdlib.OpenDB(*config)
	t.Cleanup(func() { traced.Close() })
	if err := traced.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	repo := NewMarketRepository(traced)
	for _, tc := range []struct {
		name   string
		filter market.ListFilter
		count  int
	}{{"one", market.ListFilter{Company: "公司0"}, 1}, {"ten", market.ListFilter{}, 10}} {
		t.Run(tc.name, func(t *testing.T) {
			counter.calls.Store(0)
			items, err := repo.ListByTarget(ctx, user, target, tc.filter)
			if err != nil || len(items) != tc.count {
				t.Fatalf("list count=%d: %v", len(items), err)
			}
			if got := counter.calls.Load(); got != 5 {
				t.Fatalf("%d JDs executed %d queries, want 5", len(items), got)
			}
			for _, item := range items {
				detail, err := repo.FindByID(ctx, user, item.ID)
				if err != nil || !reflect.DeepEqual(item, detail) {
					t.Fatalf("batch/detail mismatch for %s: %v", item.ID, err)
				}
				if item.AbilityGrading.Status != "not_started" || len(item.Analysis.Requirements) != 1 {
					t.Fatal("bulk projection lost defaults or extracted content")
				}
				if item.Company == "公司0" && !item.Analysis.HasPending {
					t.Fatal("retryable technical failure stopped polling")
				}
			}
		})
	}
	items, err := repo.ListByTarget(ctx, uuid.New(), target, market.ListFilter{})
	if err != nil || len(items) != 0 {
		t.Fatal("batch query exposed another account")
	}
}
