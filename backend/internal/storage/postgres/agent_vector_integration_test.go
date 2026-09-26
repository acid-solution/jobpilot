package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/LeoninCS/jobpilot-next/backend/internal/agent"
	"github.com/LeoninCS/jobpilot-next/backend/internal/embedding"
	"github.com/LeoninCS/jobpilot-next/backend/internal/jdanalysis"
	"github.com/LeoninCS/jobpilot-next/backend/internal/storage/postgres/migrations"
	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
)

func TestAgentAndVectorMigrationIsolated(t *testing.T) {
	dsn := os.Getenv("JOBPILOT_ADMIN_DATABASE_URL")
	if dsn == "" {
		t.Skip("JOBPILOT_ADMIN_DATABASE_URL is not set")
	}
	ctx := context.Background()
	admin, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	name := "jobpilot_agent_test_" + uuid.NewString()[:8]
	if _, err = admin.ExecContext(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, e := admin.ExecContext(ctx, "DROP DATABASE "+name+" WITH (FORCE)"); e != nil {
			t.Errorf("drop isolated test database: %v", e)
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
	if err = goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	if err = goose.UpTo(db, "sql", 28); err != nil {
		t.Fatal(err)
	}
	oldUser, oldTarget, oldJD, oldAnalysis := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if _, err = db.ExecContext(ctx, `INSERT INTO job_targets(id,user_id,title,employment_type) VALUES($1,$2,'旧目标','internship')`, oldTarget, oldUser); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO job_descriptions(id,user_id,target_id,raw_text,raw_text_hash,status,validation_status,analysis_prompt_version)
		VALUES($1,$2,$3,'招聘 Go 开发：负责 API 服务',encode(digest('招聘 Go 开发：负责 API 服务','sha256'),'hex'),'included','valid','old-prompt')`, oldJD, oldUser, oldTarget); err != nil {
		t.Fatal(err)
	}
	var oldRequirement uuid.UUID
	if err = db.QueryRowContext(ctx, `INSERT INTO job_description_ability_requirements(job_description_id,operator,required_count,requirement_kind,evidence,sort_order)
		VALUES($1,'single',1,'required','负责 API 服务',1) RETURNING id`, oldJD).Scan(&oldRequirement); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO job_description_ability_requirement_options(requirement_id,raw_label,evidence,sort_order,resolution_status)
		VALUES($1,'Go','负责 API 服务',1,'pending_review')`, oldRequirement); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO analysis_jobs(id,user_id,target_id,job_description_id,job_type,status,preserve_previous_result)
		VALUES($1,$2,$3,$4,'jd_analysis','failed',TRUE)`, oldAnalysis, oldUser, oldTarget, oldJD); err != nil {
		t.Fatal(err)
	}
	if err = goose.Up(db, "sql"); err != nil {
		t.Fatal(err)
	}
	var oldStatus, oldJDStatus, normalizationStatus string
	var preserved bool
	if err = db.QueryRowContext(ctx, `SELECT job.status,job.preserve_previous_result,jd.status FROM analysis_jobs job
		JOIN job_descriptions jd ON jd.id=job.job_description_id WHERE job.id=$1`, oldAnalysis).Scan(&oldStatus, &preserved, &oldJDStatus); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, `SELECT status FROM jd_normalization_jobs WHERE job_description_id=$1`, oldJD).Scan(&normalizationStatus); err != nil {
		t.Fatal(err)
	}
	if oldStatus != "succeeded" || preserved || oldJDStatus != "included" || normalizationStatus != "queued" {
		t.Fatalf("historical JD migration lost visible result: %s %v %s %s", oldStatus, preserved, oldJDStatus, normalizationStatus)
	}
	if _, err = NewJDNormalizationRepository(db, NewAnalysisRepository(db), NewEmbeddingRepository(db)).ClaimNormalization(ctx); !errors.Is(err, jdanalysis.ErrNoNormalizationJob) {
		t.Fatalf("normalization claimed a JD without user model configuration: %v", err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE jd_normalization_jobs SET status='succeeded' WHERE job_description_id=$1`, oldJD); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"agent_conversations", "agent_messages", "agent_actions", "agent_checkpoints", "source_embeddings", "embedding_jobs", "material_reassessment_jobs", "vector_backfill_state", "jd_normalization_jobs"} {
		var found bool
		if err = db.QueryRowContext(ctx, `SELECT to_regclass($1) IS NOT NULL`, table).Scan(&found); err != nil || !found {
			t.Fatalf("table %s missing: %v", table, err)
		}
	}
	user, other := uuid.New(), uuid.New()
	firstLocker := &UserMutationLocker{database: db}
	secondLocker := &UserMutationLocker{database: db}
	releaseFirst, err := firstLocker.Lock(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	otherRelease, err := secondLocker.Lock(ctx, other)
	if err != nil {
		releaseFirst()
		t.Fatalf("different account should not be blocked: %v", err)
	}
	otherRelease()
	waitCtx, cancelWait := context.WithTimeout(ctx, 75*time.Millisecond)
	_, waitErr := secondLocker.Lock(waitCtx, user)
	cancelWait()
	if !errors.Is(waitErr, context.DeadlineExceeded) {
		releaseFirst()
		t.Fatalf("same account mutation bypassed database lock: %v", waitErr)
	}
	releaseFirst()
	releaseSecond, err := secondLocker.Lock(ctx, user)
	if err != nil {
		t.Fatalf("account lock was not released: %v", err)
	}
	releaseSecond()
	if _, err = db.ExecContext(ctx, `INSERT INTO model_configs(user_id,provider,model,api_key_ciphertext,api_key_hint)
		VALUES($1,'deepseek','deepseek-chat',$2,'test')`, user, []byte{1}); err != nil {
		t.Fatal(err)
	}
	repo := NewAgentRepository(db)
	conv, err := repo.Create(ctx, user, "goal-a", "新对话")
	if err != nil {
		t.Fatal(err)
	}
	items, err := repo.List(ctx, other, "goal-a")
	if err != nil || len(items) != 0 {
		t.Fatal("cross-account conversation visible")
	}
	if _, err = repo.Get(ctx, user, "goal-b", conv.ID); !errors.Is(err, agent.ErrNotFound) {
		t.Fatalf("cross-goal conversation visible: %v", err)
	}
	lease, err := repo.BeginRun(ctx, user, "goal-a", conv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.BeginRun(ctx, user, "goal-a", conv.ID); !errors.Is(err, agent.ErrBusy) {
		t.Fatalf("second run should be blocked: %v", err)
	}
	if err = repo.HeartbeatRun(ctx, conv.ID, lease); err != nil {
		t.Fatal(err)
	}
	args := json.RawMessage(`{"raw_text":"JD 原文"}`)
	action, err := repo.CreateAction(ctx, conv.ID, user, "goal-a", "jd_add", args, "添加 JD", agent.Snapshot{Hash: "hash", Versions: map[string]int64{"target_selection": 1}})
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.SetInterrupt(ctx, conv.ID, action.ID, "interrupt"); err != nil {
		t.Fatal(err)
	}
	if err = repo.EndRun(ctx, conv.ID, lease, true); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.BeginRun(ctx, user, "goal-a", conv.ID); !errors.Is(err, agent.ErrBusy) {
		t.Fatalf("pending action must block new send: %v", err)
	}
	resume, err := repo.BeginResume(ctx, user, "goal-a", conv.ID)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := repo.ClaimAction(ctx, action.ID, conv.ID)
	if err != nil || !claimed {
		t.Fatalf("first claim failed: %v", err)
	}
	claimed, err = repo.ClaimAction(ctx, action.ID, conv.ID)
	if err != nil || claimed {
		t.Fatal("duplicate claim succeeded")
	}
	if err = repo.FinishAction(ctx, action.ID, "succeeded", json.RawMessage(`{"ok":true}`), ""); err != nil {
		t.Fatal(err)
	}
	if err = repo.EndRun(ctx, conv.ID, resume, false); err != nil {
		t.Fatal(err)
	}
	store := NewAgentCheckpointStore(db)
	if err = store.Set(ctx, "agent-test", []byte("checkpoint")); err != nil {
		t.Fatal(err)
	}
	if b, ok, e := store.Get(ctx, "agent-test"); e != nil || !ok || string(b) != "checkpoint" {
		t.Fatal("checkpoint not durable")
	}
	if err = store.Delete(ctx, "agent-test"); err != nil {
		t.Fatal(err)
	}
	orphan, err := repo.Create(ctx, user, "goal-a", "未完成的工具调用")
	if err != nil {
		t.Fatal(err)
	}
	orphanLease, err := repo.BeginRun(ctx, user, "goal-a", orphan.ID)
	if err != nil {
		t.Fatal(err)
	}
	orphanAction, err := repo.CreateAction(ctx, orphan.ID, user, "goal-a", "jd_add", args, "添加 JD", agent.Snapshot{Hash: "hash", Versions: map[string]int64{"target_selection": 1}})
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.EndRun(ctx, orphan.ID, orphanLease, false); err != nil {
		t.Fatal(err)
	}
	orphanState, err := repo.GetAction(ctx, user, "goal-a", orphanAction.ID)
	if err != nil || orphanState.Status != "failed" {
		t.Fatalf("interrupted proposal remained pending: %s %v", orphanState.Status, err)
	}
	if _, err = repo.BeginRun(ctx, user, "goal-a", orphan.ID); err != nil {
		t.Fatalf("failed proposal blocked new conversation run: %v", err)
	}
	interrupted, err := repo.Create(ctx, user, "goal-a", "确认中断")
	if err != nil {
		t.Fatal(err)
	}
	interruptedLease, err := repo.BeginRun(ctx, user, "goal-a", interrupted.ID)
	if err != nil {
		t.Fatal(err)
	}
	interruptedAction, err := repo.CreateAction(ctx, interrupted.ID, user, "goal-a", "jd_add", args, "添加 JD", agent.Snapshot{Hash: "hash", Versions: map[string]int64{"target_selection": 1}})
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.SetInterrupt(ctx, interrupted.ID, interruptedAction.ID, "interrupt"); err != nil {
		t.Fatal(err)
	}
	if err = repo.EndRun(ctx, interrupted.ID, interruptedLease, true); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.BeginResume(ctx, user, "goal-a", interrupted.ID); err != nil {
		t.Fatal(err)
	}
	if claimed, err = repo.ClaimAction(ctx, interruptedAction.ID, interrupted.ID); err != nil || !claimed {
		t.Fatalf("claim interrupted action: %v %v", claimed, err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE agent_conversations SET run_expires_at=NOW()-INTERVAL '1 second' WHERE id=$1`, interrupted.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.BeginRun(ctx, user, "goal-a", interrupted.ID); !errors.Is(err, agent.ErrBusy) {
		t.Fatalf("new run hid an executing action before recovery: %v", err)
	}
	if err = repo.RecoverRuns(ctx); err != nil {
		t.Fatal(err)
	}
	if err = repo.RecoverRuns(ctx); err != nil {
		t.Fatal(err)
	}
	recovered, err := repo.Get(ctx, user, "goal-a", interrupted.ID)
	if err != nil {
		t.Fatal(err)
	}
	uncertainMessages := 0
	for _, message := range recovered.Messages {
		if strings.Contains(message.Content, "结果尚不能确认") {
			uncertainMessages++
		}
	}
	if recovered.Status != "idle" || uncertainMessages != 1 {
		t.Fatalf("interrupted action outcome was hidden or duplicated: %s %d", recovered.Status, uncertainMessages)
	}
	if err = store.Set(ctx, "agent-"+conv.ID.String(), []byte("orphan checkpoint")); err != nil {
		t.Fatal(err)
	}
	if err = repo.Delete(ctx, user, "goal-a", conv.ID); err != nil {
		t.Fatal(err)
	}
	if _, present, err := store.Get(ctx, "agent-"+conv.ID.String()); err != nil || present {
		t.Fatalf("deleted conversation retained checkpoint: %v %v", present, err)
	}
	vectorRepo := NewEmbeddingRepository(db)
	job, err := vectorRepo.Claim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	source, err := vectorRepo.Source(ctx, job)
	if err != nil {
		t.Fatal(err)
	}
	vector := make([]float32, embedding.Dimensions)
	vector[0] = 1
	if err = vectorRepo.Complete(ctx, job, source, []embedding.VectorChunk{{Chunk: embedding.Chunk{Start: 0, End: len([]rune(source.Text)), Text: source.Text}, Vector: vector}}); err != nil {
		t.Fatal(err)
	}
	if err = vectorRepo.Heartbeat(ctx, job); !errors.Is(err, embedding.ErrLeaseLost) {
		t.Fatalf("stale worker heartbeat accepted: %v", err)
	}
	targetID, jdID := uuid.New(), uuid.New()
	_, err = db.ExecContext(ctx, `INSERT INTO job_targets(id,user_id,title,employment_type) VALUES($1,$2,'后端','internship')`, targetID, user)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO job_descriptions(id,user_id,target_id,raw_text,raw_text_hash,status) VALUES($1,$2,$3,'Go 后端开发：负责 API 开发与测试',encode(digest('Go 后端开发：负责 API 开发与测试','sha256'),'hex'),'processing')`, jdID, user, targetID)
	if err != nil {
		t.Fatal(err)
	}
	var queued int
	if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM embedding_jobs WHERE source_type='jd' AND source_id=$1 AND status='queued'`, jdID).Scan(&queued); err != nil || queued != 1 {
		t.Fatalf("JD embedding not enqueued: %d %v", queued, err)
	}
	otherTarget := uuid.New()
	if _, err = db.ExecContext(ctx, `INSERT INTO job_targets(id,user_id,title,employment_type,is_current) VALUES($1,$2,'Agent','internship',FALSE)`, otherTarget, user); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE embedding_jobs SET status='succeeded' WHERE source_type='jd' AND source_id=$1`, jdID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE job_descriptions SET target_id=$2 WHERE id=$1`, jdID, otherTarget); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM embedding_jobs WHERE source_type='jd' AND source_id=$1 AND status='queued'`, jdID).Scan(&queued); err != nil || queued != 1 {
		t.Fatalf("moving JD to another target did not requeue vector: %d %v", queued, err)
	}
	materialID := uuid.New()
	if _, err = db.ExecContext(ctx, `INSERT INTO user_profile_materials(id,user_id,type,title,source_text,status) VALUES($1,$2,'resume','简历','Go 项目经历','ready')`, materialID, user); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM embedding_jobs WHERE source_type='resume' AND source_id=$1 AND status='queued'`, materialID).Scan(&queued); err != nil || queued != 1 {
		t.Fatalf("ready material did not enqueue vector: %d %v", queued, err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE user_profile_materials SET status='draft' WHERE id=$1`, materialID); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM embedding_jobs WHERE source_type='resume' AND source_id=$1 AND status='failed'`, materialID).Scan(&queued); err != nil || queued != 1 {
		t.Fatalf("unconfirmed material retained active vector job: %d %v", queued, err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE user_profile_materials SET status='ready' WHERE id=$1`, materialID); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM embedding_jobs WHERE source_type='resume' AND source_id=$1 AND status='queued'`, materialID).Scan(&queued); err != nil || queued != 1 {
		t.Fatalf("reconfirmed material did not requeue vector: %d %v", queued, err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO source_embeddings(user_id,goal_signature,source_type,source_id,source_hash,chunk_index,start_offset,end_offset,text,embedding,embedding_model)
		VALUES($1,$2,'jd',$3,encode(digest('Go 后端开发：负责 API 开发与测试','sha256'),'hex'),0,0,19,'Go 后端开发：负责 API 开发与测试',$4::vector,$5)`, user, otherTarget.String(), jdID, vectorLiteral(vector), embedding.Model); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO source_embeddings(user_id,goal_signature,source_type,source_id,source_hash,chunk_index,start_offset,end_offset,text,embedding,embedding_model)
		VALUES($1,'','resume',$2,encode(digest('Go 项目经历','sha256'),'hex'),0,0,7,'Go 项目经历',$3::vector,$4)`, user, materialID, vectorLiteral(vector), embedding.Model); err != nil {
		t.Fatal(err)
	}
	otherMatches, err := vectorRepo.SearchSources(ctx, other, otherTarget, vector, 5)
	if err != nil || len(otherMatches) != 0 {
		t.Fatalf("source leaked across accounts: %v %v", otherMatches, err)
	}
	oldGoalMatches, err := vectorRepo.SearchSources(ctx, user, targetID, vector, 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, match := range oldGoalMatches {
		if match.SourceID == jdID {
			t.Fatal("JD source leaked across targets")
		}
	}
	currentMatches, err := vectorRepo.SearchSources(ctx, user, otherTarget, vector, 5)
	if err != nil {
		t.Fatal(err)
	}
	var foundJD, foundMaterial bool
	for _, match := range currentMatches {
		foundJD = foundJD || match.SourceID == jdID
		foundMaterial = foundMaterial || match.SourceID == materialID
	}
	if !foundJD || !foundMaterial {
		t.Fatalf("authorized sources unavailable: %v", currentMatches)
	}
	var requirementID, optionID uuid.UUID
	if err = db.QueryRowContext(ctx, `INSERT INTO job_description_ability_requirements(job_description_id,operator,required_count,requirement_kind,evidence,sort_order)
		VALUES($1,'single',1,'required','Go 后端开发：负责 API 开发与测试',1) RETURNING id`, jdID).Scan(&requirementID); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, `INSERT INTO job_description_ability_requirement_options(requirement_id,raw_label,evidence,sort_order,resolution_status)
		VALUES($1,'Go','Go 后端开发：负责 API 开发与测试',1,'pending_review') RETURNING id`, requirementID).Scan(&optionID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE job_descriptions SET validation_status='valid',status='included',analysis_prompt_version='older-parser' WHERE id=$1`, jdID); err != nil {
		t.Fatal(err)
	}
	normalizeRepo := NewJDNormalizationRepository(db, NewAnalysisRepository(db), vectorRepo)
	normalizationJob, err := normalizeRepo.ClaimNormalization(ctx)
	if err != nil {
		t.Fatal(err)
	}
	catalog, current, err := normalizeRepo.LoadNormalization(ctx, normalizationJob)
	if err != nil {
		t.Fatal(err)
	}
	if len(current.AbilityRequirements) != 1 || len(current.AbilityRequirements[0].Options) != 1 || len(catalog.Abilities) == 0 {
		t.Fatalf("existing JD options not loaded: %+v", current.AbilityRequirements)
	}
	current.AbilityRequirements[0].Options[0].CatalogCode = catalog.Abilities[0].Code
	current.AbilityRequirements[0].Options[0].AbilityName = catalog.Abilities[0].Name
	current.VectorNormalized = true
	// A review may finish while the vector model is still running. Its result
	// must survive the old normalizer's attempt to write its stale snapshot.
	if _, err = db.ExecContext(ctx, `UPDATE job_description_ability_requirement_options SET
		ability_id=(SELECT id FROM abilities WHERE code=$2),resolution_status='resolved' WHERE id=$1`,
		optionID, catalog.Abilities[0].Code); err != nil {
		t.Fatal(err)
	}
	if err = normalizeRepo.CompleteNormalization(ctx, normalizationJob, current); !errors.Is(err, jdanalysis.ErrNormalizationStateChanged) {
		t.Fatalf("stale normalizer overwrote reviewed ability: %v", err)
	}
	if err = normalizeRepo.FailNormalization(ctx, normalizationJob, "state_changed"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE jd_normalization_jobs SET next_attempt_at=NOW() WHERE id=$1`, normalizationJob.ID); err != nil {
		t.Fatal(err)
	}
	normalizationJob, err = normalizeRepo.ClaimNormalization(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, current, err = normalizeRepo.LoadNormalization(ctx, normalizationJob)
	if err != nil {
		t.Fatal(err)
	}
	current.VectorNormalized = true
	if err = normalizeRepo.CompleteNormalization(ctx, normalizationJob, current); err != nil {
		t.Fatal(err)
	}
	var abilityCode, jdStatus, normalizationVersion string
	if err = db.QueryRowContext(ctx, `SELECT ability.code,jd.status,jd.normalization_prompt_version FROM job_description_ability_requirement_options option
		JOIN abilities ability ON ability.id=option.ability_id JOIN job_description_ability_requirements requirement ON requirement.id=option.requirement_id
		JOIN job_descriptions jd ON jd.id=requirement.job_description_id WHERE option.id=$1`, optionID).
		Scan(&abilityCode, &jdStatus, &normalizationVersion); err != nil {
		t.Fatal(err)
	}
	if abilityCode != catalog.Abilities[0].Code || jdStatus != "included" || normalizationVersion != jdanalysis.PromptVersion {
		t.Fatalf("atomic JD normalization incomplete: %s %s %s", abilityCode, jdStatus, normalizationVersion)
	}
	if err = normalizeRepo.CompleteNormalization(ctx, normalizationJob, current); !errors.Is(err, jdanalysis.ErrNormalizationLeaseLost) {
		t.Fatalf("expired normalizer could write again: %v", err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE abilities SET embedding=$1::vector,embedding_model=$2 WHERE is_active AND source='seed'`,
		vectorLiteral(vector), embedding.Model); err != nil {
		t.Fatal(err)
	}
	var testAbility uuid.UUID
	if err = db.QueryRowContext(ctx, `SELECT id FROM abilities WHERE is_active AND source='seed' LIMIT 1`).Scan(&testAbility); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE abilities SET source='dynamic_review',embedding=NULL,embedding_model=NULL WHERE id=$1`, testAbility); err != nil {
		t.Fatal(err)
	}
	ready, err := vectorRepo.EmbeddingsReady(ctx)
	if err != nil || !ready {
		t.Fatalf("new dynamic ability blocked unrelated normalization: %v %v", ready, err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE abilities SET source='seed' WHERE id=$1`, testAbility); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE embedding_jobs SET status='failed',attempts=max_attempts,last_error='embedding_failed' WHERE source_type='ability' AND source_id=$1`, testAbility); err != nil {
		t.Fatal(err)
	}
	ready, err = vectorRepo.EmbeddingsReady(ctx)
	if err != nil || !ready {
		t.Fatalf("terminal embedding failure blocked unrelated normalization: %v %v", ready, err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE embedding_jobs SET status='queued' WHERE source_type='ability' AND source_id=$1`, testAbility); err != nil {
		t.Fatal(err)
	}
	ready, err = vectorRepo.EmbeddingsReady(ctx)
	if err != nil || ready {
		t.Fatalf("unfinished seed backfill was ignored: %v %v", ready, err)
	}
}
