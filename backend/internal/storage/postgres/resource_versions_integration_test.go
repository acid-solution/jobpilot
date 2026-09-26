package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"maps"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/LeoninCS/jobpilot-next/backend/internal/abilityreview"
	"github.com/LeoninCS/jobpilot-next/backend/internal/agent"
	"github.com/LeoninCS/jobpilot-next/backend/internal/jdanalysis"
	"github.com/LeoninCS/jobpilot-next/backend/internal/storage/postgres/migrations"
	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
)

// Never seed or erase the developer's live accounts to test revision guards.
func TestResourceVersionsIsolated(t *testing.T) {
	dsn := os.Getenv("JOBPILOT_ADMIN_DATABASE_URL")
	if dsn == "" {
		t.Skip("JOBPILOT_ADMIN_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	admin, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	name := "jobpilot_revision_test_" + uuid.NewString()[:8]
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := admin.ExecContext(context.Background(), "DROP DATABASE "+name+" WITH (FORCE)"); err != nil {
			t.Error(err)
		}
	}()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	db, err := Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	goose.SetBaseFS(migrations.Files)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpTo(db, "sql", 30); err != nil {
		t.Fatal(err)
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	user, targetID, jd, material := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	exec(`INSERT INTO job_targets(id,user_id,title,employment_type) VALUES($1,$2,'后端开发','internship')`, targetID, user)
	exec(`INSERT INTO job_descriptions(id,user_id,target_id,raw_text,raw_text_hash,status,validation_status)
		VALUES($1,$2,$3,'招聘 Go 后端，负责接口设计，要求 Go', $4,'included','valid')`, jd, user, targetID, jd.String())
	exec(`INSERT INTO user_profile_materials(id,user_id,type,title,source_text) VALUES($1,$2,'experience','经历','Go 项目经历')`, material, user)
	legacyConv, legacyAction := uuid.New(), uuid.New()
	exec(`INSERT INTO agent_conversations(id,user_id,goal_signature,status) VALUES($1,$2,'goal','awaiting_confirmation')`, legacyConv, user)
	exec(`INSERT INTO agent_actions(id,conversation_id,user_id,goal_signature,kind,arguments,summary,expected_hash)
		VALUES($1,$2,$3,'goal','jd_delete','{}','旧提议','old-hash')`, legacyAction, legacyConv, user)
	exec(`INSERT INTO agent_checkpoints(key,value) VALUES($1,$2)`, "agent-"+legacyConv.String(), []byte("checkpoint"))
	if err := goose.Up(db, "sql"); err != nil {
		t.Fatal(err)
	}
	repo := NewAgentRepository(db)
	revision := func(owner uuid.UUID, key string) int64 {
		t.Helper()
		values, err := repo.ReadResourceVersions(ctx, owner, []string{key})
		if err != nil {
			t.Fatal(err)
		}
		return values[key]
	}
	assertBumped := func(key string, mutate func()) {
		t.Helper()
		before := revision(user, key)
		mutate()
		if after := revision(user, key); after <= before {
			t.Fatalf("%s did not advance: %d -> %d", key, before, after)
		}
	}
	jdKey, materialKey := "jd:"+jd.String(), "material:"+material.String()
	t.Run("backfill and unsafe old proposals", func(t *testing.T) {
		for _, key := range []string{"target_selection", "target:" + targetID.String(), jdKey, "market", materialKey, "profile"} {
			if revision(user, key) != 1 {
				t.Fatalf("missing baseline %s", key)
			}
		}
		var actionStatus, conversationStatus string
		var checkpoints int
		if err := db.QueryRowContext(ctx, `SELECT a.status,c.status FROM agent_actions a JOIN agent_conversations c ON c.id=a.conversation_id WHERE a.id=$1`, legacyAction).Scan(&actionStatus, &conversationStatus); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM agent_checkpoints WHERE key=$1`, "agent-"+legacyConv.String()).Scan(&checkpoints); err != nil {
			t.Fatal(err)
		}
		if actionStatus != "stale" || conversationStatus != "idle" || checkpoints != 0 {
			t.Fatal("legacy action must require a new proposal")
		}
	})
	t.Run("ABA rollback no-op and user isolation", func(t *testing.T) {
		before := revision(user, jdKey)
		exec(`UPDATE job_descriptions SET company='改动' WHERE id=$1`, jd)
		exec(`UPDATE job_descriptions SET company=NULL WHERE id=$1`, jd)
		if revision(user, jdKey) != before+2 {
			t.Fatal("A -> B -> A must still change revision")
		}
		before = revision(user, jdKey)
		exec(`UPDATE job_descriptions SET updated_at=NOW() WHERE id=$1`, jd)
		if revision(user, jdKey) != before {
			t.Fatal("timestamp refresh is not a source change")
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE job_descriptions SET company='回滚' WHERE id=$1`, jd); err != nil {
			t.Fatal(err)
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		if revision(user, jdKey) != before {
			t.Fatal("rolled back mutation changed revision")
		}
		if revision(uuid.New(), jdKey) != 0 {
			t.Fatal("cross-account revision leak")
		}
	})
	var ability uuid.UUID
	var abilityCode string
	if err := db.QueryRowContext(ctx, `SELECT id,code FROM abilities WHERE is_active ORDER BY sort_order LIMIT 1`).Scan(&ability, &abilityCode); err != nil {
		t.Fatal(err)
	}
	var requirement, option uuid.UUID
	if err := db.QueryRowContext(ctx, `INSERT INTO job_description_ability_requirements(job_description_id,operator,required_count,evidence,sort_order)
		VALUES($1,'single',1,'要求 Go',1) RETURNING id`, jd).Scan(&requirement); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `INSERT INTO job_description_ability_requirement_options(requirement_id,raw_label,evidence,sort_order,resolution_status)
		VALUES($1,'新能力','要求 Go',1,'pending_review') RETURNING id`, requirement).Scan(&option); err != nil {
		t.Fatal(err)
	}
	t.Run("related rows and background grading", func(t *testing.T) {
		assertBumped(jdKey, func() {
			exec(`UPDATE job_description_ability_requirement_options SET qualifier='项目经验' WHERE id=$1`, option)
		})
		assertBumped(jdKey, func() {
			exec(`INSERT INTO jd_ability_level_assessments(job_description_id,ability_id,level,source,requirement_kind,evidence_quote,reason,confidence,prompt_version)
			VALUES($1,$2,3,'explicit','required','要求 Go','明确要求',0.9,'test')`, jd, ability)
		})
		assertBumped(jdKey, func() {
			exec(`INSERT INTO jd_ability_option_levels(option_id,job_description_id,ability_id,level,source,requirement_kind,evidence_quote,reason,confidence,prompt_version)
			VALUES($1,$2,$3,3,'explicit','required','要求 Go','明确要求',0.9,'test')`, option, jd, ability)
		})
		assertBumped(jdKey, func() { exec(`UPDATE jd_ability_option_levels SET level=4 WHERE option_id=$1`, option) })
		assertBumped(materialKey, func() {
			exec(`INSERT INTO user_profile_evidence(material_id,ability_id,level,evidence_quote,reason,confidence)
			VALUES($1,$2,2,'Go 项目经历','材料提取',0.9)`, material, ability)
		})
		assertBumped("capability:"+ability.String(), func() {
			exec(`INSERT INTO user_capability_profiles(user_id,ability_id,current_level) VALUES($1,$2,2)`, user, ability)
		})
		assertBumped("capability:"+ability.String(), func() {
			exec(`UPDATE user_capability_profiles SET current_level=1,manual_level=1,manual_updated_at=NOW(),level_source='manual' WHERE user_id=$1 AND ability_id=$2`, user, ability)
		})
		assertBumped("profile_settings", func() {
			exec(`INSERT INTO user_profile_settings(user_id,weekly_hours,expected_weeks,existing_experience) VALUES($1,12,8,'Go 项目')`, user)
		})
		assertBumped("knowledge_gaps", func() {
			exec(`INSERT INTO knowledge_gap_reports(user_id,target_id,goal_signature,source_hash,report) VALUES($1,$2,'goal','hash','{}')`, user, targetID)
		})
		assertBumped("recommendations", func() {
			exec(`INSERT INTO project_recommendation_reports(user_id,target_id,goal_signature,report_id,source_hash,report) VALUES($1,$2,'goal',$3,'hash','{}')`, user, targetID, uuid.New())
		})
		assertBumped("recommendations", func() {
			exec(`UPDATE project_recommendation_reports SET selected_project_id='project-1' WHERE user_id=$1`, user)
		})
	})
	t.Run("action persistence and independent revision checks", func(t *testing.T) {
		conv, err := repo.Create(ctx, user, "goal", "版本测试")
		if err != nil {
			t.Fatal(err)
		}
		versions, err := repo.ReadResourceVersions(ctx, user, []string{jdKey, "target_selection"})
		if err != nil {
			t.Fatal(err)
		}
		action, err := repo.CreateAction(ctx, conv.ID, user, "goal", "jd_delete", json.RawMessage(`{"id":"`+jd.String()+`"}`), "删除", agent.Snapshot{Hash: "hash", Versions: versions})
		if err != nil {
			t.Fatal(err)
		}
		stored, err := repo.GetAction(ctx, user, "goal", action.ID)
		if err != nil || stored.ExpectedHash != "hash" || !maps.Equal(stored.ExpectedVersions, versions) {
			t.Fatalf("snapshot not persisted: %+v %v", stored, err)
		}
		view, err := repo.Get(ctx, user, "goal", conv.ID)
		if err != nil || view.PendingAction == nil || !maps.Equal(view.PendingAction.ExpectedVersions, versions) {
			t.Fatal("pending action lost revisions after reload")
		}
		encoded, err := json.Marshal(stored)
		if err != nil {
			t.Fatal(err)
		}
		var public map[string]any
		if err := json.Unmarshal(encoded, &public); err != nil {
			t.Fatal(err)
		}
		if _, exposed := public["ExpectedVersions"]; exposed {
			t.Fatal("internal snapshot exposed")
		}
	})

	t.Run("shared review waits for every affected account", func(t *testing.T) {
		otherUser, otherTarget, otherJD := uuid.New(), uuid.New(), uuid.New()
		exec(`INSERT INTO job_targets(id,user_id,title,employment_type) VALUES($1,$2,'另一个后端目标','internship')`, otherTarget, otherUser)
		exec(`INSERT INTO job_descriptions(id,user_id,target_id,raw_text,raw_text_hash,status,validation_status) VALUES($1,$2,$3,'要求 Go',$4,'included','valid')`, otherJD, otherUser, otherTarget, otherJD.String())
		request, token := uuid.New(), uuid.New()
		exec(`INSERT INTO ability_review_requests(id,candidate_key,normalized_name,proposed_name,initiated_by_user_id,status,lease_token,attempts) VALUES($1,'test:new','new','新能力',$2,'running',$3,1)`, request, user, token)
		exec(`UPDATE job_description_ability_requirement_options SET review_request_id=$2 WHERE id=$1`, option, request)
		var otherRequirement uuid.UUID
		if err := db.QueryRowContext(ctx, `INSERT INTO job_description_ability_requirements(job_description_id,operator,required_count,evidence,sort_order) VALUES($1,'single',1,'要求 Go',1) RETURNING id`, otherJD).Scan(&otherRequirement); err != nil {
			t.Fatal(err)
		}
		exec(`INSERT INTO job_description_ability_requirement_options(requirement_id,raw_label,evidence,sort_order,resolution_status,review_request_id) VALUES($1,'新能力','要求 Go',1,'pending_review',$2)`, otherRequirement, request)
		locker, err := NewUserMutationLocker(ctx, u.String())
		if err != nil {
			t.Fatal(err)
		}
		defer locker.Close()
		release, err := locker.Lock(ctx, otherUser)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { release() }()
		before := revision(user, jdKey)
		otherBefore := revision(otherUser, "jd:"+otherJD.String())
		completed := make(chan error, 1)
		reviews := NewAbilityReviewRepository(db)
		go func() {
			completed <- reviews.Complete(ctx, abilityreview.Input{ID: request, UserID: user, LeaseToken: token, Attempts: 1, MaxAttempts: 3, Catalog: []abilityreview.CatalogAbility{{Code: abilityCode}}}, abilityreview.Result{Decision: "reuse_existing", Reason: "复用已有能力", ExistingAbilityCode: abilityCode}, uuid.Nil)
		}()
		waitForBlockedAdvisory(t, ctx, db)
		select {
		case err := <-completed:
			t.Fatalf("review crossed confirmation lock: %v", err)
		default:
		}
		release()
		// Avoid a second release of the gate (channel receive) on deferred cleanup.
		release = func() {}
		select {
		case err := <-completed:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		if revision(user, jdKey) <= before || revision(otherUser, "jd:"+otherJD.String()) <= otherBefore {
			t.Fatal("shared review did not advance both owners' JD versions")
		}
	})
	t.Run("failure and expired worker use confirmation lock", func(t *testing.T) {
		jobID, token := uuid.New(), uuid.New()
		exec(`INSERT INTO analysis_jobs(id,user_id,target_id,job_description_id,job_type,status,attempts,lease_token,lease_expires_at)
			VALUES($1,$2,$3,$4,'jd_analysis','running',3,$5,NOW()-INTERVAL '1 minute')`, jobID, user, targetID, jd, token)
		locker, err := NewUserMutationLocker(ctx, u.String())
		if err != nil {
			t.Fatal(err)
		}
		defer locker.Close()
		release, err := locker.Lock(ctx, user)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { release() }()
		completed := make(chan error, 1)
		go func() { _, err := NewAnalysisRepository(db).RecoverExpired(ctx); completed <- err }()
		waitForBlockedAdvisory(t, ctx, db)
		select {
		case err := <-completed:
			t.Fatalf("recovery crossed lock: %v", err)
		default:
		}
		before := revision(user, jdKey)
		release()
		release = func() {}
		if err := <-completed; err != nil {
			t.Fatal(err)
		}
		if revision(user, jdKey) <= before {
			t.Fatal("exhausted worker's failure did not advance revision")
		}
		if err := NewAnalysisRepository(db).Fail(ctx, jdanalysis.Job{ID: jobID, UserID: user, JobDescriptionID: jd, LeaseToken: token, MaxAttempts: 3, Attempts: 3}, "test", false); !errors.Is(err, jdanalysis.ErrLeaseLost) {
			t.Fatalf("old worker accepted: %v", err)
		}
	})
	t.Run("delete and recreate retains tombstone", func(t *testing.T) {
		before := revision(user, materialKey)
		exec(`DELETE FROM user_profile_materials WHERE id=$1`, material)
		exec(`INSERT INTO user_profile_materials(id,user_id,type,title,source_text) VALUES($1,$2,'experience','经历','Go 项目经历')`, material, user)
		if revision(user, materialKey) <= before+1 {
			t.Fatal("deleted resource reset its revision")
		}
	})
}

func waitForBlockedAdvisory(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var blocked bool
		if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND NOT granted AND database=(SELECT oid FROM pg_database WHERE datname=current_database()))`).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("background writer never waited on the account lock")
}
