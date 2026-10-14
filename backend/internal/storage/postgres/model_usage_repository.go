package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/LeoninCS/jobpilot-next/backend/internal/modelusage"
	"github.com/google/uuid"
)

type ModelUsageRepository struct{ db *sql.DB }

func NewModelUsageRepository(db *sql.DB) *ModelUsageRepository { return &ModelUsageRepository{db} }
func ensureUsageOperation(ctx context.Context, db interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, s modelusage.Scope) error {
	_, e := db.ExecContext(ctx, `INSERT INTO model_usage_operations(id,user_id,source_operation_id,purpose,target_id,resource_id)VALUES($1,$2,$3,$4,COALESCE($5::uuid,(SELECT id FROM job_targets WHERE user_id=$2 AND is_current)), $6)ON CONFLICT(user_id,source_operation_id,purpose) DO NOTHING`, modelusage.OperationKey(s), s.UserID, s.OperationID, s.Purpose, nullUsageID(s.TargetID), nullUsageID(s.ResourceID))
	return e
}
func (r *ModelUsageRepository) EnsureOperation(ctx context.Context, s modelusage.Scope) error {
	return ensureUsageOperation(ctx, r.db, s)
}
func (r *ModelUsageRepository) Begin(ctx context.Context, c modelusage.Call) error {
	tx, e := r.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = ensureUsageOperation(ctx, tx, c.Scope); e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO model_usage_calls(id,provider,requested_model,payer,purpose,source_operation_id,started_at,result,usage_status,legacy_usage_id,confirmed)VALUES($1,$2,$3,$4,$5,$6,$7,'pending','unknown',$8,false)`, c.ID, c.Provider, c.RequestedModel, c.Payer, c.Purpose, c.Scope.OperationID, c.StartedAt, nullUsageID(c.Scope.LegacyUsageID))
	if e != nil {
		return e
	}
	// SELECT instead of computed ID also finds operations restored by migration.
	_, e = tx.ExecContext(ctx, `INSERT INTO model_usage_links(call_id,operation_id)SELECT $1,id FROM model_usage_operations WHERE user_id=$2 AND source_operation_id=$3 AND purpose=$4 ON CONFLICT DO NOTHING`, c.ID, c.Scope.UserID, c.Scope.OperationID, c.Purpose)
	if e != nil {
		return e
	}
	if c.Purpose == "ability_review" || c.Purpose == "ability_alias_review" {
		if e = linkSharedReviewUsage(ctx, tx, c.Scope.OperationID, c.Purpose); e != nil {
			return e
		}
	}
	return tx.Commit()
}
func nullUsageID(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id
}
func (r *ModelUsageRepository) Finish(ctx context.Context, c modelusage.Call) error {
	var snapshot any
	if len(c.PriceSnapshot) > 0 {
		snapshot = []byte(c.PriceSnapshot)
	}
	_, e := r.db.ExecContext(ctx, `UPDATE model_usage_calls SET returned_model=$2,provider_request_id=$3,duration_ms=$4,result=$5,usage_status=$6,input_tokens=$7,output_tokens=$8,total_tokens=$9,cache_hit_tokens=$10,cache_miss_tokens=$11,currency=$12,estimated_cost=$13,price_snapshot=$14,confirmed=$15,finished_at=now() WHERE id=$1 AND finished_at IS NULL`, c.ID, c.ReturnedModel, c.ProviderRequestID, c.DurationMS, c.Result, c.UsageStatus, c.Input, c.Output, c.Total, c.CacheHit, c.CacheMiss, c.Currency, c.Cost, snapshot, c.Confirmed)
	return e
}
func linkSharedReviewUsage(ctx context.Context, tx *sql.Tx, request uuid.UUID, purpose string) error {
	rows, e := tx.QueryContext(ctx, `SELECT DISTINCT jd.user_id FROM job_description_ability_requirement_options opt JOIN job_description_ability_requirements req ON req.id=opt.requirement_id JOIN job_descriptions jd ON jd.id=req.job_description_id WHERE opt.review_request_id=$1 UNION SELECT COALESCE(jd.user_id,m.user_id) FROM ability_alias_review_sources src LEFT JOIN job_descriptions jd ON jd.id=src.job_description_id LEFT JOIN user_profile_materials m ON m.id=src.material_id WHERE src.review_request_id=$1`, request)
	if e != nil {
		return e
	}
	var users []uuid.UUID
	for rows.Next() {
		var user uuid.UUID
		if e = rows.Scan(&user); e != nil {
			rows.Close()
			return e
		}
		users = append(users, user)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, user := range users {
		s := modelusage.Scope{UserID: user, OperationID: request, Purpose: purpose}
		if e = ensureUsageOperation(ctx, tx, s); e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO model_usage_links(call_id,operation_id)SELECT c.id,o.id FROM model_usage_calls c JOIN model_usage_operations o ON o.user_id=$1 AND o.source_operation_id=c.source_operation_id AND o.purpose=c.purpose WHERE c.source_operation_id=$2 AND c.purpose=$3 ON CONFLICT DO NOTHING`, user, request, purpose)
		if e != nil {
			return e
		}
	}
	return nil
}

const usageWhere = `o.user_id=$1 AND c.started_at >= $2 AND c.started_at < $3 AND ($4='' OR o.purpose=$4) AND ($5='' OR c.payer=$5) AND ($6::uuid IS NULL OR o.id=$6)`

func usageArgs(user uuid.UUID, f modelusage.Filter) []any {
	return []any{user, f.From, f.To, f.Purpose, f.Payer, nullUsageID(f.OperationID)}
}
func (r *ModelUsageRepository) Calls(ctx context.Context, user uuid.UUID, f modelusage.Filter) (modelusage.Page[modelusage.Call], error) {
	page := modelusage.Page[modelusage.Call]{Items: []modelusage.Call{}, Page: f.Page, PageSize: f.PageSize}
	args := usageArgs(user, f)
	e := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM model_usage_calls c JOIN model_usage_links l ON l.call_id=c.id JOIN model_usage_operations o ON o.id=l.operation_id WHERE `+usageWhere, args...).Scan(&page.Total)
	if e != nil {
		return page, e
	}
	rows, e := r.db.QueryContext(ctx, `SELECT c.id,c.provider,c.requested_model,c.returned_model,c.provider_request_id,c.payer,o.purpose,o.id,c.started_at,c.duration_ms,c.result,c.usage_status,c.input_tokens,c.output_tokens,c.total_tokens,c.cache_hit_tokens,c.cache_miss_tokens,c.currency,c.estimated_cost::text,c.price_snapshot,c.legacy,c.confirmed FROM model_usage_calls c JOIN model_usage_links l ON l.call_id=c.id JOIN model_usage_operations o ON o.id=l.operation_id WHERE `+usageWhere+` ORDER BY c.started_at DESC,c.id LIMIT $7 OFFSET $8`, append(args, f.PageSize, (f.Page-1)*f.PageSize)...)
	if e != nil {
		return page, e
	}
	defer rows.Close()
	for rows.Next() {
		var c modelusage.Call
		var raw []byte
		e = rows.Scan(&c.ID, &c.Provider, &c.RequestedModel, &c.ReturnedModel, &c.ProviderRequestID, &c.Payer, &c.Purpose, &c.OperationID, &c.StartedAt, &c.DurationMS, &c.Result, &c.UsageStatus, &c.Input, &c.Output, &c.Total, &c.CacheHit, &c.CacheMiss, &c.Currency, &c.Cost, &raw, &c.Legacy, &c.Confirmed)
		if e != nil {
			return page, e
		}
		c.PriceSnapshot = json.RawMessage(raw)
		page.Items = append(page.Items, c)
	}
	return page, rows.Err()
}
func (r *ModelUsageRepository) Summary(ctx context.Context, user uuid.UUID, f modelusage.Filter) ([]modelusage.Group, error) {
	args := usageArgs(user, f)
	groups := []modelusage.Group{}
	// DISTINCT call IDs prevent shared associations inflating a user's summary.
	query := `WITH visible AS(SELECT DISTINCT c.* FROM model_usage_calls c JOIN model_usage_links l ON l.call_id=c.id JOIN model_usage_operations o ON o.id=l.operation_id WHERE ` + usageWhere + `) SELECT payer,COUNT(*) FILTER(WHERE confirmed),COUNT(*) FILTER(WHERE usage_status!='known'),COUNT(*) FILTER(WHERE estimated_cost IS NULL),COALESCE(SUM(input_tokens),0),COALESCE(SUM(output_tokens),0),COALESCE(SUM(cache_hit_tokens),0),COALESCE(SUM(cache_miss_tokens),0),COALESCE(SUM(input_tokens) FILTER(WHERE cache_hit_tokens IS NOT NULL AND input_tokens>0),0),COALESCE(SUM(cache_hit_tokens) FILTER(WHERE input_tokens>0),0) FROM visible GROUP BY payer ORDER BY payer`
	rows, e := r.db.QueryContext(ctx, query, args...)
	if e != nil {
		return nil, e
	}
	for rows.Next() {
		g := modelusage.Group{Costs: map[string]string{}}
		var sampleHits int64
		if e = rows.Scan(&g.Payer, &g.CallCount, &g.UnknownUsageCount, &g.UnknownCostCount, &g.InputTokens, &g.OutputTokens, &g.CacheHitTokens, &g.CacheMissTokens, &g.CacheSampleInput, &sampleHits); e != nil {
			rows.Close()
			return nil, e
		}
		if g.CacheSampleInput > 0 {
			rate := float64(sampleHits) / float64(g.CacheSampleInput)
			g.CacheHitRate = &rate
		}
		groups = append(groups, g)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	rows, e = r.db.QueryContext(ctx, `WITH visible AS(SELECT DISTINCT c.* FROM model_usage_calls c JOIN model_usage_links l ON l.call_id=c.id JOIN model_usage_operations o ON o.id=l.operation_id WHERE `+usageWhere+`)SELECT payer,currency,SUM(estimated_cost)::text FROM visible WHERE estimated_cost IS NOT NULL GROUP BY payer,currency`, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var payer, currency, cost string
		if e = rows.Scan(&payer, &currency, &cost); e != nil {
			return nil, e
		}
		for i := range groups {
			if groups[i].Payer == payer {
				groups[i].Costs[currency] = cost
			}
		}
	}
	return groups, rows.Err()
}
func (r *ModelUsageRepository) Operations(ctx context.Context, user uuid.UUID, f modelusage.Filter) (modelusage.Page[modelusage.Operation], error) {
	page := modelusage.Page[modelusage.Operation]{Items: []modelusage.Operation{}, Page: f.Page, PageSize: f.PageSize}
	args := usageArgs(user, f)
	condition := `o.user_id=$1 AND ((o.started_at >= $2 AND o.started_at < $3 AND NOT EXISTS(SELECT 1 FROM model_usage_links z WHERE z.operation_id=o.id)) OR EXISTS(SELECT 1 FROM model_usage_links z JOIN model_usage_calls v ON v.id=z.call_id WHERE z.operation_id=o.id AND v.started_at >= $2 AND v.started_at < $3)) AND ($4='' OR o.purpose=$4) AND ($6::uuid IS NULL OR o.id=$6) AND ($5='' OR EXISTS(SELECT 1 FROM model_usage_links l JOIN model_usage_calls c ON c.id=l.call_id WHERE l.operation_id=o.id AND c.payer=$5 AND c.started_at >= $2 AND c.started_at < $3))`
	e := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM model_usage_operations o WHERE `+condition, args...).Scan(&page.Total)
	if e != nil {
		return page, e
	}
	rows, e := r.db.QueryContext(ctx, `SELECT o.id,o.purpose,o.started_at,(SELECT COUNT(*) FROM model_usage_links l JOIN model_usage_calls c ON c.id=l.call_id WHERE l.operation_id=o.id AND c.started_at >= $2 AND c.started_at < $3 AND c.confirmed AND ($5='' OR c.payer=$5)) FROM model_usage_operations o WHERE `+condition+` ORDER BY o.started_at DESC,o.id LIMIT $7 OFFSET $8`, append(args, f.PageSize, (f.Page-1)*f.PageSize)...)
	if e != nil {
		return page, e
	}
	defer rows.Close()
	for rows.Next() {
		var op modelusage.Operation
		if e = rows.Scan(&op.ID, &op.Purpose, &op.StartedAt, &op.CallCount); e != nil {
			return page, e
		}
		page.Items = append(page.Items, op)
	}
	return page, rows.Err()
}
