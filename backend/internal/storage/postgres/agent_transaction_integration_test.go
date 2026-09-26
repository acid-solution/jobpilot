package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LeoninCS/jobpilot-next/backend/internal/agent"
	"github.com/LeoninCS/jobpilot-next/backend/internal/market"
	"github.com/LeoninCS/jobpilot-next/backend/internal/modelconfig"
	"github.com/LeoninCS/jobpilot-next/backend/internal/profile"
	"github.com/LeoninCS/jobpilot-next/backend/internal/storage/postgres/migrations"
	"github.com/LeoninCS/jobpilot-next/backend/internal/target"
	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
)

func agentTransactionDatabase(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("JOBPILOT_ADMIN_DATABASE_URL")
	if dsn == "" {
		t.Skip("JOBPILOT_ADMIN_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	admin, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	name := "jobpilot_action_tx_" + uuid.NewString()[:8]
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+name); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := admin.ExecContext(ctx, "DROP DATABASE "+name+" WITH (FORCE)"); err != nil {
			t.Error(err)
		}
		admin.Close()
	})
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	db, err := Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	goose.SetBaseFS(migrations.Files)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	if err := goose.Up(db, "sql"); err != nil {
		t.Fatal(err)
	}
	return db
}

type transactionTestCredentials struct{}

func (transactionTestCredentials) Credentials(context.Context, uuid.UUID) (modelconfig.Credentials, error) {
	return modelconfig.Credentials{APIKey: "test-key", Model: "deepseek-chat"}, nil
}

func TestConfirmedAgentActionTransactionIsolated(t *testing.T) {
	db := agentTransactionDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	// Immediate and deferred failures cover both receipt-writing and final
	// COMMIT failure, after the business repository has "committed" its scope.
	exec(`CREATE FUNCTION fail_action_receipt_test() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
		IF NEW.summary='receipt-fail' AND NEW.status='succeeded' THEN RAISE EXCEPTION 'receipt failure'; END IF;
		RETURN NEW; END $$`)
	exec(`CREATE TRIGGER fail_action_receipt_test BEFORE UPDATE ON agent_actions FOR EACH ROW EXECUTE FUNCTION fail_action_receipt_test()`)
	exec(`CREATE FUNCTION fail_action_commit_test() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
		IF NEW.summary='commit-fail' AND NEW.status='succeeded' THEN RAISE EXCEPTION 'commit failure'; END IF;
		RETURN NEW; END $$`)
	exec(`CREATE CONSTRAINT TRIGGER fail_action_commit_test AFTER UPDATE ON agent_actions
		DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION fail_action_commit_test()`)
	exec(`CREATE FUNCTION fail_level_event_test() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
		IF NEW.new_level=5 THEN RAISE EXCEPTION 'business partial failure'; END IF;
		RETURN NEW; END $$`)
	exec(`CREATE TRIGGER fail_level_event_test BEFORE INSERT ON user_capability_level_events
		FOR EACH ROW EXECUTE FUNCTION fail_level_event_test()`)
	var ability uuid.UUID
	if err := db.QueryRowContext(ctx, `SELECT id FROM abilities WHERE name='Go' AND is_active`).Scan(&ability); err != nil {
		t.Fatal(err)
	}
	targets := target.NewService(NewTargetRepository(db))
	catalog, err := targets.Catalog(ctx)
	if err != nil || len(catalog) == 0 {
		t.Fatalf("catalog: %v", err)
	}
	profiles := profile.NewService(NewProfileRepository(db), nil)
	markets := market.NewService(NewMarketRepository(db), targets)
	for _, tc := range []struct {
		name    string
		marker  string
		level   int
		stale   bool
		aba     bool
		cancel  bool
		status  string
		wantErr bool
	}{
		{name: "success and duplicate confirmation", level: 2, status: "succeeded"},
		{name: "receipt failure rolls back profile and revisions", marker: "receipt-fail", level: 2, status: "pending", wantErr: true},
		{name: "commit failure never reports success", marker: "commit-fail", level: 2, status: "pending", wantErr: true},
		{name: "business partial failure keeps only failed action", level: 5, status: "failed"},
		{name: "stale proposal", level: 3, stale: true, status: "stale", wantErr: true},
		{name: "edited back to identical content", level: 3, aba: true, status: "stale", wantErr: true},
		{name: "cancel never writes", level: 2, cancel: true, status: "cancelled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			user := uuid.New()
			goal, err := targets.UpsertCurrent(ctx, user, target.UpsertInput{EmploymentType: "internship", Directions: []target.DirectionInput{{CategoryID: catalog[0].ID}}})
			if err != nil {
				t.Fatal(err)
			}
			jd := uuid.New()
			exec(`INSERT INTO job_descriptions(id,user_id,target_id,raw_text,raw_text_hash,status,validation_status)
				VALUES($1,$2,$3,'负责 Go 服务开发，要求熟悉 Go',$4,'included','valid')`, jd, user, goal.ID, jd.String())
			var requirement uuid.UUID
			if err := db.QueryRowContext(ctx, `INSERT INTO job_description_ability_requirements(job_description_id,operator,required_count,requirement_kind,evidence,sort_order)
				VALUES($1,'single',1,'required','要求熟悉 Go',1) RETURNING id`, jd).Scan(&requirement); err != nil {
				t.Fatal(err)
			}
			exec(`INSERT INTO job_description_ability_requirement_options(requirement_id,ability_id,raw_label,evidence,sort_order,resolution_status)
				VALUES($1,$2,'Go','要求熟悉 Go',1,'resolved')`, requirement, ability)
			if _, err := profiles.SetCapabilityLevel(ctx, user, ability, 1); err != nil {
				t.Fatal(err)
			}
			args, _ := json.Marshal(map[string]any{"kind": "capability_level", "arguments": map[string]any{"id": ability, "level": tc.level}})
			var modelCalls atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var delta any = map[string]any{"role": "assistant", "content": "操作结果已收到"}
				finish := "stop"
				if modelCalls.Add(1) == 1 {
					delta = map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{
						"index": 0, "id": "write1", "type": "function", "function": map[string]any{"name": "propose_jobpilot_change", "arguments": string(args)},
					}}}
					finish = "tool_calls"
				}
				w.Header().Set("Content-Type", "text/event-stream")
				body, _ := json.Marshal(map[string]any{"id": "test", "object": "chat.completion.chunk", "created": 1, "model": "deepseek-chat", "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
				_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", body)
			}))
			defer provider.Close()
			repo := NewAgentRepository(db)
			s := &agent.Service{Repo: repo, Transactions: repo, Checkpoints: NewAgentCheckpointStore(db), Targets: targets, Market: markets, Profile: profiles, Credentials: transactionTestCredentials{}, DeepSeekBaseURL: provider.URL}
			conversation, err := s.Create(ctx, user, "profile")
			if err != nil {
				t.Fatal(err)
			}
			if err := s.Send(ctx, user, conversation.ID, "profile", "设置 Go 等级", func(agent.Event) {}); err != nil {
				t.Fatal(err)
			}
			view, err := s.Get(ctx, user, conversation.ID)
			if err != nil || view.PendingAction == nil {
				t.Fatalf("missing pending action: %v", err)
			}
			action := view.PendingAction.ID
			if tc.marker != "" {
				exec(`UPDATE agent_actions SET summary=$2 WHERE id=$1`, action, tc.marker)
			}
			expectedLevel := 1
			if tc.stale || tc.aba {
				if _, err := profiles.SetCapabilityLevel(ctx, user, ability, 2); err != nil {
					t.Fatal(err)
				}
				expectedLevel = 2
				if tc.aba {
					if _, err := profiles.SetCapabilityLevel(ctx, user, ability, 1); err != nil {
						t.Fatal(err)
					}
					expectedLevel = 1
				}
			}
			keys := []string{"profile", "capability:" + ability.String()}
			before, err := repo.ReadResourceVersions(ctx, user, keys)
			if err != nil {
				t.Fatal(err)
			}
			var events []agent.Event
			err = s.Resolve(ctx, user, conversation.ID, action, !tc.cancel, func(event agent.Event) {
				events = append(events, event)
				if event.Type == "tool" && event.Text == "已执行" {
					var persisted string
					// A different connection must already see the committed receipt.
					if err := db.QueryRowContext(ctx, `SELECT status FROM agent_actions WHERE id=$1`, action).Scan(&persisted); err != nil || persisted != "succeeded" {
						t.Errorf("success emitted before commit: %s %v", persisted, err)
					}
				}
			})
			if (err != nil) != tc.wantErr {
				t.Fatalf("resolve: %v", err)
			}
			var status string
			if err := db.QueryRowContext(ctx, `SELECT status FROM agent_actions WHERE id=$1`, action).Scan(&status); err != nil || status != tc.status {
				t.Fatalf("action status=%s: %v", status, err)
			}
			if tc.status == "succeeded" {
				expectedLevel = tc.level
				if err := s.Resolve(ctx, user, conversation.ID, action, true, func(agent.Event) {}); err == nil {
					t.Fatal("duplicate confirmation accepted")
				}
			}
			capability, err := profiles.GetOverview(ctx, user)
			if err != nil || len(capability.Capabilities) != 1 || capability.Capabilities[0].CurrentLevel != expectedLevel {
				t.Fatalf("profile did not commit/rollback as expected: %+v %v", capability, err)
			}
			after, err := repo.ReadResourceVersions(ctx, user, keys)
			if err != nil {
				t.Fatal(err)
			}
			for _, key := range keys {
				if tc.status == "succeeded" && after[key] <= before[key] {
					t.Fatal("successful mutation did not advance its revision")
				}
				if tc.status != "succeeded" && after[key] != before[key] {
					t.Fatal("failed/cancelled/stale action changed a resource revision")
				}
			}
			if tc.wantErr {
				for _, event := range events {
					if event.Type == "tool" && event.Text == "已执行" {
						t.Fatal("failed transaction emitted success")
					}
				}
			}
			// Both success and all failure paths must release the account lock.
			locker := &UserMutationLocker{database: db}
			lockCtx, cancelLock := context.WithTimeout(ctx, time.Second)
			release, err := locker.Lock(lockCtx, user)
			cancelLock()
			if err != nil {
				t.Fatalf("action leaked its advisory lock: %v", err)
			}
			release()
		})
	}
}

func TestAgentTransactionConnectionAndLockLifetimeIsolated(t *testing.T) {
	db := agentTransactionDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	user := uuid.New()
	repo := NewAgentRepository(db)
	profiles := NewProfileRepository(db)
	ready, finish := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- repo.WithinUserTransaction(ctx, user, func(txCtx context.Context) error {
			var outerID, innerID int64
			if err := repo.db.QueryRowContext(txCtx, `SELECT txid_current()`).Scan(&outerID); err != nil {
				return err
			}
			if err := repo.WithinSavepoint(txCtx, func(innerCtx context.Context) error {
				if err := repo.db.QueryRowContext(innerCtx, `SELECT txid_current()`).Scan(&innerID); err != nil {
					return err
				}
				hours, weeks := 12, 10
				_, err := profiles.SaveSettings(innerCtx, user, profile.Settings{WeeklyHours: &hours, ExpectedWeeks: &weeks})
				return err
			}); err != nil {
				return err
			}
			if outerID != innerID {
				return errors.New("business escaped the outer transaction")
			}
			close(ready)
			select {
			case <-finish:
				return nil
			case <-txCtx.Done():
				return txCtx.Err()
			}
		})
	}()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("transaction setup failed: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	defer close(finish)
	settings, err := profiles.GetSettings(ctx, user)
	if err != nil || settings.WeeklyHours != nil {
		t.Fatalf("business data became visible before the outer commit: %+v %v", settings, err)
	}
	locker := &UserMutationLocker{database: db}
	waitCtx, cancelWait := context.WithTimeout(ctx, 100*time.Millisecond)
	_, err = locker.Lock(waitCtx, user)
	cancelWait()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("another writer bypassed the action lock: %v", err)
	}
	otherRelease, err := locker.Lock(ctx, uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	otherRelease()
	finish <- struct{}{}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	settings, err = profiles.GetSettings(ctx, user)
	if err != nil || settings.WeeklyHours == nil || *settings.WeeklyHours != 12 {
		t.Fatalf("outer commit did not publish business data: %+v %v", settings, err)
	}
	release, err := locker.Lock(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	release()
}

func TestAgentTransactionBatchDuplicateAndReadbackIsolated(t *testing.T) {
	db := agentTransactionDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	user := uuid.New()
	repo := NewAgentRepository(db)
	targets := target.NewService(NewTargetRepository(db))
	catalog, err := targets.Catalog(ctx)
	if err != nil || len(catalog) == 0 {
		t.Fatalf("catalog: %v", err)
	}
	if _, err := targets.UpsertCurrent(ctx, user, target.UpsertInput{EmploymentType: "internship", Directions: []target.DirectionInput{{CategoryID: catalog[0].ID}}}); err != nil {
		t.Fatal(err)
	}
	markets := market.NewService(NewMarketRepository(db), targets)
	oldText := "测试公司招聘 Go 后端实习生，负责服务接口开发，要求熟悉 Go 和数据库。"
	newText := "测试公司招聘 Python 后端实习生，负责数据服务开发，要求熟悉 Python 和数据库。"
	if _, err := markets.Submit(ctx, user, oldText); err != nil {
		t.Fatal(err)
	}
	for _, rollback := range []bool{true, false} {
		err := repo.WithinUserTransaction(ctx, user, func(txCtx context.Context) error {
			batch, err := markets.SubmitBatch(txCtx, user, []string{oldText, newText, newText})
			if err != nil {
				return err
			}
			if batch.CreatedCount != 1 || batch.DuplicateCount != 2 {
				return fmt.Errorf("unexpected duplicate handling: %+v", batch)
			}
			// Readback must see the uncommitted insert, and must finish its JD
			// cursor before fetching per-JD details on the same connection.
			items, err := markets.ListCurrent(txCtx, user, market.ListFilter{})
			if err != nil {
				return err
			}
			if len(items) != 2 {
				return fmt.Errorf("transaction readback returned %d JDs", len(items))
			}
			if rollback {
				return errors.New("simulate action receipt failure")
			}
			return nil
		})
		if (err != nil) != rollback {
			t.Fatalf("batch action: %v", err)
		}
		items, err := markets.ListCurrent(ctx, user, market.ListFilter{})
		want := 2
		if rollback {
			want = 1
		}
		if err != nil || len(items) != want {
			t.Fatalf("batch escaped the action transaction: %d JDs, want %d: %v", len(items), want, err)
		}
	}
}

func TestAgentLockWaitDoesNotStarveBusinessPoolIsolated(t *testing.T) {
	db := agentTransactionDatabase(t)
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var databaseName string
	if err := db.QueryRowContext(ctx, `SELECT current_database()`).Scan(&databaseName); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(os.Getenv("JOBPILOT_ADMIN_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + databaseName
	locker, err := NewUserMutationLocker(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer locker.Close()
	user := uuid.New()
	release, err := locker.Lock(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { release() }()

	// A normal page holds its lock on the separate pool, then needs the
	// business pool to write. Concurrent confirmations must leave it usable.
	repo := NewAgentRepository(db)
	started, done := make(chan struct{}), make(chan error, 1)
	go func() {
		close(started)
		done <- repo.WithinUserTransaction(ctx, user, func(context.Context) error { return nil })
	}()
	<-started
	// Let the competing confirmation begin waiting before the page writes.
	time.Sleep(50 * time.Millisecond)
	writeCtx, cancelWrite := context.WithTimeout(ctx, 500*time.Millisecond)
	hours, weeks := 12, 10
	_, err = NewProfileRepository(db).SaveSettings(writeCtx, user, profile.Settings{WeeklyHours: &hours, ExpectedWeeks: &weeks})
	cancelWrite()
	if err != nil {
		t.Fatalf("waiting confirmation exhausted the business connection pool: %v", err)
	}
	select {
	case err := <-done:
		t.Fatalf("confirmation bypassed account lock: %v", err)
	default:
	}
	release()
	release = func() {}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}
