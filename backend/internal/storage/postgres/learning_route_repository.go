package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/LeoninCS/jobpilot-next/backend/internal/learningroutes"
	"github.com/LeoninCS/jobpilot-next/backend/internal/mutationlock"
	"github.com/google/uuid"
	"time"
)

type LearningRouteRepository struct {
	db           *repositoryDatabase
	transactions *AgentRepository
}

func NewLearningRouteRepository(db *sql.DB) *LearningRouteRepository {
	return &LearningRouteRepository{newRepositoryDatabase(db), NewAgentRepository(db)}
}
func (r *LearningRouteRepository) WithinUserTransaction(ctx context.Context, user uuid.UUID, run func(context.Context) error) error {
	if binding, ok := ctx.Value(transactionKey{}).(transactionBinding); ok && binding.pool == r.db.pool {
		return run(ctx)
	}
	if mutationlock.HeldBy(ctx, user) {
		tx, err := r.db.pool.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		txCtx := context.WithValue(ctx, transactionKey{}, transactionBinding{pool: r.db.pool, tx: tx})
		if err = run(txCtx); err != nil {
			return err
		}
		return tx.Commit()
	}
	return r.transactions.WithinUserTransaction(ctx, user, run)
}

func (r *LearningRouteRepository) Get(ctx context.Context, user uuid.UUID, goal string, report uuid.UUID, project string) (*learningroutes.Version, *learningroutes.JobView, *learningroutes.ImportRequest, error) {
	var route uuid.UUID
	err := r.db.QueryRowContext(ctx, `SELECT id FROM learning_routes WHERE user_id=$1 AND goal_signature=$2 AND report_id=$3 AND project_id=$4`, user, goal, report, project).Scan(&route)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, nil, nil
	}
	if err != nil {
		return nil, nil, nil, err
	}
	var v learningroutes.Version
	var cfg, plans, preferences []byte
	err = r.db.QueryRowContext(ctx, `SELECT v.id,v.route_id,v.version_no,v.revision,v.source_hash,v.knowledge_hash,v.config,v.plans,v.generated_at,v.preference_basis,v.preference_adjustment FROM learning_route_versions v JOIN learning_routes r ON r.id=v.route_id WHERE r.id=$1 AND v.version_no=r.current_version`, route).Scan(&v.ID, &v.RouteID, &v.Number, &v.Revision, &v.SourceHash, &v.KnowledgeHash, &cfg, &plans, &v.GeneratedAt, &preferences, &v.PreferenceAdjustment)
	var version *learningroutes.Version
	if err == nil {
		if e := json.Unmarshal(cfg, &v.Config); e != nil {
			return nil, nil, nil, e
		}
		if e := json.Unmarshal(plans, &v.Plans); e != nil {
			return nil, nil, nil, e
		}
		if len(preferences) > 0 {
			if e := json.Unmarshal(preferences, &v.Preferences); e != nil {
				return nil, nil, nil, e
			}
		}
		version = &v
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, nil, nil, err
	}
	var j learningroutes.JobView
	err = r.db.QueryRowContext(ctx, `SELECT id,status,attempts,last_error,updated_at,last_error_detail FROM learning_route_jobs WHERE route_id=$1 ORDER BY created_at DESC LIMIT 1`, route).Scan(&j.ID, &j.Status, &j.Attempts, &j.ErrorCode, &j.UpdatedAt, &j.ErrorMessage)
	var job *learningroutes.JobView
	if err == nil {
		job = &j
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, nil, nil, err
	}
	var id uuid.UUID
	err = r.db.QueryRowContext(ctx, `SELECT id FROM learning_route_import_requests WHERE route_id=$1 AND user_id=$2 ORDER BY created_at DESC LIMIT 1`, route, user).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return version, job, nil, nil
	}
	if err != nil {
		return nil, nil, nil, err
	}
	request, err := r.FindImport(ctx, user, id)
	return version, job, &request, err
}
func (r *LearningRouteRepository) Enqueue(ctx context.Context, user uuid.UUID, ss learningroutes.Snapshot) (learningroutes.JobView, error) {
	var route uuid.UUID
	var base int
	err := r.db.QueryRowContext(ctx, `INSERT INTO learning_routes(user_id,goal_signature,report_id,project_id)VALUES($1,$2,$3,$4) ON CONFLICT(user_id,goal_signature,report_id,project_id)DO UPDATE SET project_id=EXCLUDED.project_id RETURNING id,current_version`, user, ss.GoalSignature, ss.ReportID, ss.ProjectID).Scan(&route, &base)
	if err != nil {
		return learningroutes.JobView{}, err
	}
	raw, err := json.Marshal(ss.Input)
	if err != nil {
		return learningroutes.JobView{}, err
	}
	var j learningroutes.JobView
	err = r.db.QueryRowContext(ctx, `INSERT INTO learning_route_jobs(route_id,user_id,target_id,goal_signature,source_hash,base_version,input,model_plans)VALUES($1,$2,$3,$4,$5,$6,$7,(SELECT model_plans FROM learning_route_jobs WHERE route_id=$1 AND source_hash=$5 AND base_version=$6 AND input=$7::jsonb AND status='failed' ORDER BY created_at DESC LIMIT 1))ON CONFLICT DO NOTHING RETURNING id,status,attempts,last_error,updated_at`, route, user, ss.TargetID, ss.GoalSignature, ss.SourceHash, base, raw).Scan(&j.ID, &j.Status, &j.Attempts, &j.ErrorCode, &j.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return j, learningroutes.ErrConflict
	}
	return j, err
}
func (r *LearningRouteRepository) Edit(ctx context.Context, user uuid.UUID, input learningroutes.EditInput) error {
	var route uuid.UUID
	var number int
	var projectHash, knowledgeHash string
	err := r.db.QueryRowContext(ctx, `SELECT v.route_id,v.version_no,v.source_hash,v.knowledge_hash FROM learning_route_versions v JOIN learning_routes r ON r.id=v.route_id WHERE r.user_id=$1 AND v.id=$2 AND v.revision=$3 AND v.version_no=r.current_version FOR UPDATE`, user, input.VersionID, input.Revision).Scan(&route, &number, &projectHash, &knowledgeHash)
	if errors.Is(err, sql.ErrNoRows) {
		return learningroutes.ErrConflict
	}
	if err != nil {
		return err
	}
	var frozen, imported bool
	err = r.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM learning_route_import_requests WHERE version_id=$1 AND revision=$2 AND status IN('accepted','sending','unknown')),EXISTS(SELECT 1 FROM learning_route_import_requests WHERE version_id=$1 AND status='succeeded')`, input.VersionID, input.Revision).Scan(&frozen, &imported)
	if err != nil {
		return err
	}
	if frozen {
		return learningroutes.ErrConflict
	}
	cfg, _ := json.Marshal(input.Config)
	plans, _ := json.Marshal(input.Plans)
	// Once imported, editing creates a new route snapshot rather than mutating
	// the provenance of an execution version already visible in StudyFlow.
	if imported {
		_, err = r.db.ExecContext(ctx, `INSERT INTO learning_route_versions(route_id,version_no,source_hash,knowledge_hash,config,plans,preference_basis,preference_adjustment) SELECT $1,$2,$3,$4,$5,$6,preference_basis,preference_adjustment FROM learning_route_versions WHERE id=$7`, route, number+1, projectHash, knowledgeHash, cfg, plans, input.VersionID)
		if err != nil {
			return err
		}
		_, err = r.db.ExecContext(ctx, `UPDATE learning_routes SET current_version=$2 WHERE id=$1`, route, number+1)
		return err
	}
	result, err := r.db.ExecContext(ctx, `UPDATE learning_route_versions SET config=$4,plans=$5,revision=revision+1 WHERE id=$2 AND revision=$3 AND route_id IN(SELECT id FROM learning_routes WHERE user_id=$1)`, user, input.VersionID, input.Revision, cfg, plans)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count != 1 {
		return learningroutes.ErrConflict
	}
	return nil
}
func (r *LearningRouteRepository) Claim(ctx context.Context, lease time.Duration) (learningroutes.Job, error) {
	var j learningroutes.Job
	var raw []byte
	var modelPlans []byte
	err := r.db.QueryRowContext(ctx, `WITH candidate AS(SELECT id FROM learning_route_jobs WHERE status='queued' AND next_attempt_at<=now() AND attempts<max_attempts ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1)
 UPDATE learning_route_jobs j SET status='running',attempts=attempts+1,lease_token=$1,heartbeat_at=now(),lease_expires_at=now()+$2::interval,updated_at=now() FROM candidate c WHERE j.id=c.id RETURNING j.id,j.route_id,j.user_id,j.target_id,j.goal_signature,j.source_hash,j.base_version,j.input,j.attempts,j.lease_token,COALESCE(j.model_plans,'[]'::jsonb),j.last_error_detail`, uuid.New(), lease.String()).Scan(&j.ID, &j.RouteID, &j.UserID, &j.TargetID, &j.GoalSignature, &j.SourceHash, &j.BaseVersion, &raw, &j.Attempts, &j.LeaseToken, &modelPlans, &j.ErrorDetail)
	if errors.Is(err, sql.ErrNoRows) {
		return j, learningroutes.ErrNoJob
	}
	if err != nil {
		return j, err
	}
	err = json.Unmarshal(raw, &j.Input)
	if err == nil {
		err = json.Unmarshal(modelPlans, &j.ModelPlans)
	}
	return j, err
}

func (r *LearningRouteRepository) SaveModelPlans(ctx context.Context, j learningroutes.Job, plans []learningroutes.Plan) error {
	raw, err := json.Marshal(plans)
	if err != nil {
		return err
	}
	result, err := r.db.ExecContext(ctx, `UPDATE learning_route_jobs SET model_plans=$3,updated_at=now() WHERE id=$1 AND lease_token=$2 AND status='running'`, j.ID, j.LeaseToken, raw)
	return routeLeaseResult(result, err)
}
func (r *LearningRouteRepository) Heartbeat(ctx context.Context, j learningroutes.Job, lease time.Duration) error {
	result, err := r.db.ExecContext(ctx, `UPDATE learning_route_jobs SET heartbeat_at=now(),lease_expires_at=now()+$3::interval,updated_at=now() WHERE id=$1 AND lease_token=$2 AND status='running'`, j.ID, j.LeaseToken, lease.String())
	return routeLeaseResult(result, err)
}
func routeLeaseResult(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return learningroutes.ErrLeaseLost
	}
	return nil
}
func (r *LearningRouteRepository) RecoverExpired(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, `UPDATE learning_route_jobs SET status=CASE WHEN attempts<max_attempts THEN 'queued' ELSE 'failed' END,next_attempt_at=now(),lease_token=NULL,heartbeat_at=NULL,lease_expires_at=NULL,last_error='worker_interrupted',updated_at=now() WHERE status='running' AND lease_expires_at<now()`)
	return err
}
func (r *LearningRouteRepository) Complete(ctx context.Context, j learningroutes.Job, plans []learningroutes.Plan, validate func(context.Context) error) error {
	return r.WithinUserTransaction(ctx, j.UserID, func(txCtx context.Context) error {
		var active bool
		err := r.db.QueryRowContext(txCtx, `SELECT true FROM learning_route_jobs WHERE id=$1 AND lease_token=$2 AND status='running' FOR UPDATE`, j.ID, j.LeaseToken).Scan(&active)
		if errors.Is(err, sql.ErrNoRows) {
			return learningroutes.ErrLeaseLost
		}
		if err != nil {
			return err
		}
		var version int
		if err = r.db.QueryRowContext(txCtx, `SELECT current_version FROM learning_routes WHERE id=$1 AND user_id=$2 FOR UPDATE`, j.RouteID, j.UserID).Scan(&version); err != nil {
			return err
		}
		if version != j.BaseVersion {
			return learningroutes.ErrInputsChanged
		}
		if err = validate(txCtx); err != nil {
			return err
		}
		cfg, _ := json.Marshal(j.Input.Config)
		raw, _ := json.Marshal(plans)
		_, err = r.db.ExecContext(txCtx, `INSERT INTO learning_route_versions(route_id,version_no,source_hash,knowledge_hash,config,plans,preference_basis,preference_adjustment)VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, j.RouteID, version+1, j.Input.ProjectHash, j.Input.KnowledgeHash, cfg, raw, nullableJSON(marshalPreference(j.Input.Preferences)), j.Input.Adjustment)
		if err != nil {
			return err
		}
		if _, err = r.db.ExecContext(txCtx, `UPDATE learning_routes SET current_version=$2 WHERE id=$1`, j.RouteID, version+1); err != nil {
			return err
		}
		_, err = r.db.ExecContext(txCtx, `UPDATE learning_route_jobs SET status='succeeded',lease_token=NULL,heartbeat_at=NULL,lease_expires_at=NULL,last_error='',updated_at=now() WHERE id=$1`, j.ID)
		return err
	})
}
func (r *LearningRouteRepository) Fail(ctx context.Context, j learningroutes.Job, code, detail string, retry bool) error {
	status := "failed"
	if retry && j.Attempts < 3 {
		status = "queued"
	}
	delay := 10 * (1 << (j.Attempts - 1))
	result, err := r.db.ExecContext(ctx, `UPDATE learning_route_jobs SET status=$3,last_error=$4,next_attempt_at=now()+$5*interval '1 second',last_error_detail=$6,model_plans=CASE WHEN $3='queued' AND $4='route_budget_or_structure_invalid' THEN NULL ELSE model_plans END,lease_token=NULL,heartbeat_at=NULL,lease_expires_at=NULL,updated_at=now()WHERE id=$1 AND lease_token=$2 AND status='running'`, j.ID, j.LeaseToken, status, code, delay, detail)
	return routeLeaseResult(result, err)
}
func (r *LearningRouteRepository) Freeze(ctx context.Context, user uuid.UUID, v learningroutes.Version, payload learningroutes.Payload, digest string) (learningroutes.ImportRequest, error) {
	raw, _ := json.Marshal(payload)
	id, key := uuid.New(), uuid.NewString()
	var stored uuid.UUID
	err := r.db.QueryRowContext(ctx, `INSERT INTO learning_route_import_requests(id,user_id,route_id,version_id,revision,idempotency_key,content_digest,payload)VALUES($1,$2,$3,$4,$5,$6,$7,$8)ON CONFLICT(version_id,revision) DO UPDATE SET revision=EXCLUDED.revision RETURNING id`, id, user, v.RouteID, v.ID, v.Revision, key, digest, raw).Scan(&stored)
	if err != nil {
		return learningroutes.ImportRequest{}, err
	}
	return r.FindImport(ctx, user, stored)
}
func (r *LearningRouteRepository) FindImport(ctx context.Context, user, id uuid.UUID) (learningroutes.ImportRequest, error) {
	var request learningroutes.ImportRequest
	var payload, receipt []byte
	err := r.db.QueryRowContext(ctx, `SELECT id,user_id,route_id,version_id,revision,idempotency_key,content_digest,payload,status,receipt,error_code FROM learning_route_import_requests WHERE id=$1 AND user_id=$2`, id, user).Scan(&request.ID, &request.UserID, &request.RouteID, &request.VersionID, &request.Revision, &request.Key, &request.Digest, &payload, &request.Status, &receipt, &request.ErrorCode)
	if errors.Is(err, sql.ErrNoRows) {
		return request, learningroutes.ErrNotFound
	}
	if err != nil {
		return request, err
	}
	if err = json.Unmarshal(payload, &request.Payload); err != nil {
		return request, err
	}
	if receipt != nil {
		request.Receipt = &learningroutes.Receipt{}
		err = json.Unmarshal(receipt, request.Receipt)
	}
	return request, err
}
func (r *LearningRouteRepository) ClaimImport(ctx context.Context, user, id uuid.UUID) (learningroutes.ImportRequest, error) {
	request, err := r.FindImport(ctx, user, id)
	if err != nil || request.Status == "succeeded" {
		return request, err
	}
	token := uuid.New()
	result, err := r.db.ExecContext(ctx, `UPDATE learning_route_import_requests SET status='sending',dispatch_token=$3,dispatch_expires_at=now()+interval '3 minutes',updated_at=now() WHERE id=$1 AND user_id=$2 AND status!='succeeded' AND(status!='sending' OR dispatch_expires_at<now())`, id, user, token)
	if err != nil {
		return request, err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return request, learningroutes.ErrConflict
	}
	request.Status = "sending"
	request.DispatchToken = token
	return request, nil
}
func (r *LearningRouteRepository) FinishImport(ctx context.Context, request learningroutes.ImportRequest, receipt *learningroutes.Receipt, status, code string) error {
	var raw any
	if receipt != nil {
		encoded, err := json.Marshal(receipt)
		if err != nil {
			return err
		}
		raw = encoded
	}
	result, err := r.db.ExecContext(ctx, `UPDATE learning_route_import_requests SET status=$3,receipt=$4,error_code=$5,dispatch_token=NULL,dispatch_expires_at=NULL,updated_at=now() WHERE id=$1 AND dispatch_token=$2 AND status='sending'`, request.ID, request.DispatchToken, status, raw, code)
	return routeLeaseResult(result, err)
}

func marshalPreference(v any) json.RawMessage {
	if v == nil {
		return nil
	}
	b, _ := json.Marshal(v)
	return b
}
