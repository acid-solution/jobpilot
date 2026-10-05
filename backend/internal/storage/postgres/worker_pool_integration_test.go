package postgres

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/LeoninCS/jobpilot-next/backend/internal/abilityreview"
	"github.com/LeoninCS/jobpilot-next/backend/internal/jdanalysis"
	"github.com/google/uuid"
)

type concurrentAnalyzer struct {
	started chan string
	release <-chan struct{}
	mu      sync.Mutex
	calls   map[string]int
}

func (a *concurrentAnalyzer) AnalyzeJD(ctx context.Context, _, _, text string, _ jdanalysis.Catalog) (jdanalysis.Result, error) {
	a.mu.Lock()
	a.calls[text]++
	a.mu.Unlock()
	select {
	case a.started <- text:
	case <-ctx.Done():
		return jdanalysis.Result{}, ctx.Err()
	}
	select {
	case <-a.release:
	case <-ctx.Done():
		return jdanalysis.Result{}, ctx.Err()
	}
	return jdanalysis.Result{
		DocumentType: jdanalysis.DocumentJobDescription, ValidationStatus: jdanalysis.ValidationValid,
		Title: "后端开发实习生", EmploymentType: "internship", Responsibilities: []string{"负责服务端业务开发"},
		Classifications: []jdanalysis.JobClassification{{CategoryCode: "backend", SpecialtyCode: "backend-business",
			Relation: "primary", Evidence: "负责服务端业务开发", Reason: "核心职责是服务端业务开发。"}},
	}, nil
}

func TestAnalysisWorkerPoolProcessesDistinctJobsConcurrently(t *testing.T) {
	db := agentTransactionDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	userID, targetID := uuid.New(), uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO job_targets(id,user_id,title,employment_type,directions,catalog_status)
		VALUES($1,$2,'后端开发','internship','[]','valid')`, targetID, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO target_directions(target_id,category_id,specialty_id,sort_order)
		VALUES($1,'10000000-0000-0000-0000-000000000001',NULL,1)`, targetID); err != nil {
		t.Fatal(err)
	}
	for index := range 3 {
		jdID := uuid.New()
		text := fmt.Sprintf("测试%d 后端开发实习生，负责服务端业务开发，参与系统设计与代码测试。", index)
		if _, err := db.ExecContext(ctx, `INSERT INTO job_descriptions(id,user_id,target_id,raw_text,raw_text_hash,status)
			VALUES($1,$2,$3,$4,encode(digest($4,'sha256'),'hex'),'processing')`, jdID, userID, targetID, text); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO analysis_jobs(id,user_id,target_id,job_description_id,job_type,status)
			VALUES($1,$2,$3,$4,'jd_analysis','queued')`, uuid.New(), userID, targetID, jdID); err != nil {
			t.Fatal(err)
		}
	}
	release := make(chan struct{})
	analyzer := &concurrentAnalyzer{started: make(chan string, 3), release: release, calls: map[string]int{}}
	worker := jdanalysis.NewWorker(NewAnalysisRepository(db), transactionTestCredentials{}, analyzer, 5*time.Millisecond)
	finished := make(chan struct{})
	go func() { defer close(finished); worker.Run(ctx, 2) }()
	t.Cleanup(func() { cancel(); <-finished })
	for range 2 {
		select {
		case <-analyzer.started:
		case <-ctx.Done():
			t.Fatal("two workers did not enter model calls concurrently")
		}
	}
	var running, queued int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FILTER(WHERE status='running'),COUNT(*) FILTER(WHERE status='queued')
		FROM analysis_jobs WHERE user_id=$1`, userID).Scan(&running, &queued); err != nil {
		t.Fatal(err)
	}
	if running != 2 || queued != 1 {
		t.Fatalf("running=%d queued=%d want 2 / 1", running, queued)
	}
	close(release)
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		var succeeded int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM analysis_jobs WHERE user_id=$1 AND status='succeeded'`, userID).Scan(&succeeded); err != nil {
			t.Fatal(err)
		}
		if succeeded == 3 {
			break
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal("worker pool did not finish all tasks")
		}
	}
	cancel()
	<-finished
	analyzer.mu.Lock()
	defer analyzer.mu.Unlock()
	if len(analyzer.calls) != 3 {
		t.Fatalf("calls=%v", analyzer.calls)
	}
	for text, count := range analyzer.calls {
		if count != 1 {
			t.Fatalf("duplicate model call: %q count=%d", text, count)
		}
	}
}

func TestConcurrentReviewWorkersDoNotExceedQuota(t *testing.T) {
	for _, limits := range []AbilityReviewQuotaLimits{{UserDaily: 1, GlobalDaily: 30}, {UserDaily: 3, GlobalDaily: 1}} {
		t.Run(fmt.Sprintf("user_%d_global_%d", limits.UserDaily, limits.GlobalDaily), func(t *testing.T) {
			db := agentTransactionDatabase(t)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			userID := uuid.New()
			inputs := make([]abilityreview.Input, 2)
			for index := range inputs {
				inputs[index] = abilityreview.Input{ID: uuid.New(), UserID: userID, LeaseToken: uuid.New()}
				if limits.GlobalDaily == 1 {
					inputs[index].UserID = uuid.New()
				}
				input := inputs[index]
				if _, err := db.ExecContext(ctx, `INSERT INTO ability_review_requests(id,candidate_key,normalized_name,proposed_name,initiated_by_user_id,
					status,attempts,lease_token,heartbeat_at,lease_expires_at)
					VALUES($1,$2,$2,$2,$3,'running',1,$4,NOW(),NOW()+INTERVAL '5 minutes')`, input.ID, input.ID.String(), input.UserID, input.LeaseToken); err != nil {
					t.Fatal(err)
				}
			}
			repo := NewAbilityReviewRepository(db, limits)
			start := make(chan struct{})
			results := make(chan error, 2)
			for _, input := range inputs {
				go func() { <-start; _, _, err := repo.ReserveUsage(ctx, input, "test"); results <- err }()
			}
			close(start)
			reserved, deferred := 0, 0
			for range 2 {
				select {
				case err := <-results:
					if err == nil {
						reserved++
					} else if errors.Is(err, abilityreview.ErrQuota) {
						deferred++
					} else {
						t.Fatal(err)
					}
				case <-ctx.Done():
					t.Fatal("concurrent quota reservation timed out")
				}
			}
			var calls int
			if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM platform_model_usage`).Scan(&calls); err != nil {
				t.Fatal(err)
			}
			if reserved != 1 || deferred != 1 || calls != 1 {
				t.Fatalf("reserved=%d deferred=%d platform calls=%d", reserved, deferred, calls)
			}
		})
	}
}
