package postgres

import (
	"context"
	"database/sql"

	"github.com/google/uuid"
)

// A shared review can resolve options belonging to several accounts. Lock all
// owners in UUID order before locking the review row, matching page writers'
// account -> review order. If another account linked an option in the meantime,
// retry this short transaction, without repeating the model call.
func (r *AbilityReviewRepository) beginReviewMutation(ctx context.Context, request uuid.UUID) (*sql.Tx, []uuid.UUID, error) {
	for {
		tx, err := r.database.BeginTx(ctx, nil)
		if err != nil {
			return nil, nil, err
		}
		before, err := reviewAffectedSources(ctx, tx, request)
		if err != nil {
			tx.Rollback()
			return nil, nil, err
		}
		owners := map[uuid.UUID]bool{}
		for _, source := range before {
			if owners[source.user] {
				continue
			}
			if err = lockUserMutationTx(ctx, tx, source.user); err != nil {
				tx.Rollback()
				return nil, nil, err
			}
			owners[source.user] = true
		}
		var id uuid.UUID
		if err = tx.QueryRowContext(ctx, `SELECT id FROM ability_review_requests WHERE id=$1 FOR UPDATE`, request).Scan(&id); err != nil {
			tx.Rollback()
			return nil, nil, err
		}
		after, err := reviewAffectedSources(ctx, tx, request)
		if err != nil {
			tx.Rollback()
			return nil, nil, err
		}
		ids := make([]uuid.UUID, 0, len(after))
		stable := true
		for _, source := range after {
			if !owners[source.user] {
				stable = false
				break
			}
			ids = append(ids, source.jd)
		}
		if stable {
			return tx, ids, nil
		}
		tx.Rollback()
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
	}
}

type reviewSource struct{ user, jd uuid.UUID }

func reviewAffectedSources(ctx context.Context, tx *sql.Tx, request uuid.UUID) ([]reviewSource, error) {
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT jd.user_id,jd.id
		FROM job_description_ability_requirement_options option
		JOIN job_description_ability_requirements requirement ON requirement.id=option.requirement_id
		JOIN job_descriptions jd ON jd.id=requirement.job_description_id
		WHERE option.review_request_id=$1 ORDER BY jd.user_id,jd.id`, request)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sources []reviewSource
	for rows.Next() {
		var source reviewSource
		if err := rows.Scan(&source.user, &source.jd); err != nil {
			return nil, err
		}
		sources = append(sources, source)
	}
	return sources, rows.Err()
}
