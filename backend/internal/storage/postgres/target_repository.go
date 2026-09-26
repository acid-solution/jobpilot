package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/LeoninCS/jobpilot-next/backend/internal/jdanalysis"
	"github.com/LeoninCS/jobpilot-next/backend/internal/target"
	"github.com/google/uuid"
)

type TargetRepository struct {
	database *sql.DB
}

func NewTargetRepository(database *sql.DB) *TargetRepository {
	return &TargetRepository{database: database}
}

func (r *TargetRepository) FindCurrent(ctx context.Context, userID uuid.UUID) (target.Target, error) {
	const query = `
		SELECT id, user_id, title, employment_type, graduation_year, catalog_status, created_at, updated_at
		FROM job_targets
		WHERE user_id = $1 AND is_current = TRUE`
	result, err := scanTargetBase(r.database.QueryRowContext(ctx, query, userID))
	if err != nil {
		return target.Target{}, err
	}
	result.Directions, err = loadTargetDirections(ctx, r.database, result.ID)
	if err != nil {
		return target.Target{}, err
	}
	return result, nil
}

func (r *TargetRepository) ListCatalog(ctx context.Context) ([]target.Category, error) {
	rows, err := r.database.QueryContext(ctx, `
		SELECT category.id, category.code, category.name,
		       specialty.id, specialty.code, specialty.name
		FROM job_categories category
		LEFT JOIN job_specialties specialty
		  ON specialty.category_id = category.id AND specialty.is_active
		WHERE category.is_active
		ORDER BY category.sort_order, specialty.sort_order`)
	if err != nil {
		return nil, fmt.Errorf("list job catalog: %w", err)
	}
	defer rows.Close()

	result := make([]target.Category, 0, 9)
	indexByID := make(map[uuid.UUID]int)
	for rows.Next() {
		var categoryID uuid.UUID
		var categoryCode, categoryName string
		var specialtyID uuid.NullUUID
		var specialtyCode, specialtyName sql.NullString
		if err := rows.Scan(&categoryID, &categoryCode, &categoryName, &specialtyID, &specialtyCode, &specialtyName); err != nil {
			return nil, fmt.Errorf("scan job catalog: %w", err)
		}
		index, exists := indexByID[categoryID]
		if !exists {
			index = len(result)
			indexByID[categoryID] = index
			result = append(result, target.Category{ID: categoryID, Code: categoryCode, Name: categoryName, Specialties: []target.Specialty{}})
		}
		if specialtyID.Valid {
			result[index].Specialties = append(result[index].Specialties, target.Specialty{
				ID: specialtyID.UUID, Code: specialtyCode.String, Name: specialtyName.String,
			})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate job catalog: %w", err)
	}
	return result, nil
}

func (r *TargetRepository) UpsertCurrent(ctx context.Context, userID uuid.UUID, input target.UpsertInput) (target.Target, error) {
	transaction, err := r.database.BeginTx(ctx, nil)
	if err != nil {
		return target.Target{}, fmt.Errorf("begin target transaction: %w", err)
	}
	defer transaction.Rollback()

	directions, title, err := resolveTargetDirections(ctx, transaction, input.Directions)
	if err != nil {
		return target.Target{}, err
	}
	directionsJSON, err := json.Marshal(directions)
	if err != nil {
		return target.Target{}, fmt.Errorf("marshal target directions: %w", err)
	}

	const updateQuery = `
		UPDATE job_targets
		SET title = $2, employment_type = $3, graduation_year = $4, directions = $5,
		    catalog_status = 'valid', updated_at = NOW()
		WHERE user_id = $1 AND is_current = TRUE
		RETURNING id, user_id, title, employment_type, graduation_year, catalog_status, created_at, updated_at`
	current, err := scanTargetBase(transaction.QueryRowContext(
		ctx, updateQuery, userID, title, input.EmploymentType, input.GraduationYear, directionsJSON,
	))
	if errors.Is(err, target.ErrNotFound) {
		const insertQuery = `
			INSERT INTO job_targets (id, user_id, title, employment_type, graduation_year, directions, catalog_status)
			VALUES ($1, $2, $3, $4, $5, $6, 'valid')
			RETURNING id, user_id, title, employment_type, graduation_year, catalog_status, created_at, updated_at`
		current, err = scanTargetBase(transaction.QueryRowContext(
			ctx, insertQuery, uuid.New(), userID, title, input.EmploymentType, input.GraduationYear, directionsJSON,
		))
	}
	if err != nil {
		return target.Target{}, err
	}
	if _, err := transaction.ExecContext(ctx, `DELETE FROM target_directions WHERE target_id = $1`, current.ID); err != nil {
		return target.Target{}, fmt.Errorf("replace target directions: %w", err)
	}
	for index, direction := range directions {
		if _, err := transaction.ExecContext(ctx, `
			INSERT INTO target_directions (target_id, category_id, specialty_id, sort_order)
			VALUES ($1, $2, $3, $4)`, current.ID, direction.CategoryID, direction.SpecialtyID, index+1); err != nil {
			return target.Target{}, fmt.Errorf("insert target direction: %w", err)
		}
	}
	if err := reclassifyTargetJobDescriptions(ctx, transaction, current.ID, userID); err != nil {
		return target.Target{}, err
	}
	if err := transaction.Commit(); err != nil {
		return target.Target{}, fmt.Errorf("commit target: %w", err)
	}
	current.Directions = directions
	return current, nil
}

func resolveTargetDirections(ctx context.Context, transaction *sql.Tx, input []target.DirectionInput) ([]target.Direction, string, error) {
	directions := make([]target.Direction, 0, len(input))
	labels := make([]string, 0, len(input))
	for _, requested := range input {
		var categoryName string
		var specialtyName sql.NullString
		err := transaction.QueryRowContext(ctx, `
			SELECT category.name, specialty.name
			FROM job_categories category
			LEFT JOIN job_specialties specialty
			  ON specialty.id = $2 AND specialty.category_id = category.id AND specialty.is_active
			WHERE category.id = $1 AND category.is_active
			  AND ($2::uuid IS NULL OR specialty.id IS NOT NULL)`, requested.CategoryID, requested.SpecialtyID,
		).Scan(&categoryName, &specialtyName)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, "", target.ErrValidation
		}
		if err != nil {
			return nil, "", fmt.Errorf("resolve target direction: %w", err)
		}
		direction := target.Direction{
			CategoryID: requested.CategoryID, Category: categoryName,
			SpecialtyID: requested.SpecialtyID, Specialty: specialtyName.String,
		}
		directions = append(directions, direction)
		if specialtyName.Valid {
			labels = append(labels, specialtyName.String)
		} else {
			labels = append(labels, categoryName)
		}
	}
	return directions, strings.Join(labels, "＋"), nil
}

func reclassifyTargetJobDescriptions(ctx context.Context, transaction *sql.Tx, targetID, userID uuid.UUID) error {
	rows, err := transaction.QueryContext(ctx, `
		SELECT jd.id, jd.employment_type
		FROM job_descriptions jd
		WHERE jd.target_id = $1 AND jd.user_id = $2
		  AND jd.validation_status = 'valid'
		  AND EXISTS (
			SELECT 1 FROM analysis_jobs job
			WHERE job.job_description_id = jd.id
			  AND job.job_type = 'jd_analysis' AND job.status = 'succeeded'
		  )
		ORDER BY jd.created_at`, targetID, userID)
	if err != nil {
		return fmt.Errorf("list JDs for target reclassification: %w", err)
	}
	type analyzedJD struct {
		id             uuid.UUID
		employmentType string
	}
	values := make([]analyzedJD, 0)
	for rows.Next() {
		var value analyzedJD
		if err := rows.Scan(&value.id, &value.employmentType); err != nil {
			rows.Close()
			return fmt.Errorf("scan JD for target reclassification: %w", err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate JDs for target reclassification: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close target reclassification rows: %w", err)
	}

	for _, value := range values {
		classifications, err := loadSavedClassifications(ctx, transaction, value.id)
		if err != nil {
			return err
		}
		outcome, err := determineTargetRelationship(ctx, transaction, targetID, value.employmentType, classifications)
		if err != nil {
			return err
		}
		if _, err := transaction.ExecContext(ctx, `
			UPDATE job_descriptions
			SET status = $3, relevance_reason = $4, primary_category = $5,
			    secondary_category = $6, updated_at = NOW()
			WHERE id = $1 AND user_id = $2`, value.id, userID, outcome.status,
			outcome.reason, outcome.primaryCategory, outcome.secondaryCategory); err != nil {
			return fmt.Errorf("update JD target relationship: %w", err)
		}
		if outcome.status == "included" {
			if err := enqueuePendingAbilityReviewsForJD(ctx, transaction, value.id, userID); err != nil {
				return err
			}
			if err := enqueueJDAbilityGrading(ctx, transaction, userID, targetID, value.id); err != nil {
				return fmt.Errorf("enqueue JD ability grading after target change: %w", err)
			}
		}
	}
	return nil
}

func loadSavedClassifications(ctx context.Context, transaction *sql.Tx, jdID uuid.UUID) ([]savedClassification, error) {
	rows, err := transaction.QueryContext(ctx, `
		SELECT category.code, category.name, COALESCE(specialty.code, ''),
		       COALESCE(specialty.name, ''), classification.relation
		FROM job_description_classifications classification
		JOIN job_categories category ON category.id = classification.category_id
		LEFT JOIN job_specialties specialty ON specialty.id = classification.specialty_id
		WHERE classification.job_description_id = $1
		ORDER BY CASE classification.relation WHEN 'primary' THEN 0 ELSE 1 END,
		         classification.created_at`, jdID)
	if err != nil {
		return nil, fmt.Errorf("load classifications for target reclassification: %w", err)
	}
	defer rows.Close()
	result := make([]savedClassification, 0)
	for rows.Next() {
		var value savedClassification
		if err := rows.Scan(&value.categoryCode, &value.categoryName, &value.specialtyCode, &value.specialtyName, &value.relation); err != nil {
			return nil, fmt.Errorf("scan classification for target reclassification: %w", err)
		}
		result = append(result, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate classifications for target reclassification: %w", err)
	}
	return result, nil
}

func enqueuePendingAbilityReviewsForJD(ctx context.Context, transaction *sql.Tx, jdID, userID uuid.UUID) error {
	rows, err := transaction.QueryContext(ctx, `
		SELECT option.id, option.raw_label, option.qualifier, option.evidence,
		       option.required_level, option.candidate_metadata
		FROM job_description_ability_requirement_options option
		JOIN job_description_ability_requirements requirement ON requirement.id = option.requirement_id
		WHERE requirement.job_description_id = $1
		  AND option.ability_id IS NULL
		  AND option.resolution_status = 'pending_review'
		  AND option.review_request_id IS NULL
		ORDER BY requirement.sort_order, option.sort_order`, jdID)
	if err != nil {
		return fmt.Errorf("load pending abilities after target change: %w", err)
	}
	pending := make([]pendingAbilityOption, 0)
	for rows.Next() {
		var value pendingAbilityOption
		var requiredLevel sql.NullInt16
		var metadata []byte
		if err := rows.Scan(&value.id, &value.option.RawLabel, &value.option.Qualifier,
			&value.option.Evidence, &requiredLevel, &metadata); err != nil {
			rows.Close()
			return fmt.Errorf("scan pending ability after target change: %w", err)
		}
		if requiredLevel.Valid {
			level := int(requiredLevel.Int16)
			value.option.RequiredLevel = &level
		}
		if len(metadata) > 0 && string(metadata) != "null" && string(metadata) != "{}" {
			var candidate jdanalysis.AbilityCandidate
			if err := json.Unmarshal(metadata, &candidate); err != nil {
				rows.Close()
				return fmt.Errorf("decode pending ability metadata after target change: %w", err)
			}
			value.option.Candidate = &candidate
		}
		pending = append(pending, value)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate pending abilities after target change: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close pending abilities after target change: %w", err)
	}
	for _, value := range pending {
		if err := enqueueAbilityReview(ctx, transaction, userID, value); err != nil {
			return err
		}
	}
	if len(pending) > 0 {
		if err := cleanupRequirementGroups(ctx, transaction, jdID); err != nil {
			return err
		}
	}
	return nil
}

func loadTargetDirections(ctx context.Context, queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, targetID uuid.UUID) ([]target.Direction, error) {
	rows, err := queryer.QueryContext(ctx, `
		SELECT category.id, category.name, specialty.id, specialty.name
		FROM target_directions direction
		JOIN job_categories category ON category.id = direction.category_id
		LEFT JOIN job_specialties specialty ON specialty.id = direction.specialty_id
		WHERE direction.target_id = $1
		ORDER BY direction.sort_order`, targetID)
	if err != nil {
		return nil, fmt.Errorf("load target directions: %w", err)
	}
	defer rows.Close()
	result := make([]target.Direction, 0)
	for rows.Next() {
		var direction target.Direction
		var specialtyID uuid.NullUUID
		var specialtyName sql.NullString
		if err := rows.Scan(&direction.CategoryID, &direction.Category, &specialtyID, &specialtyName); err != nil {
			return nil, fmt.Errorf("scan target direction: %w", err)
		}
		if specialtyID.Valid {
			direction.SpecialtyID = &specialtyID.UUID
			direction.Specialty = specialtyName.String
		}
		result = append(result, direction)
	}
	return result, rows.Err()
}

type rowScanner interface {
	Scan(...any) error
}

func scanTargetBase(row rowScanner) (target.Target, error) {
	var result target.Target
	if err := row.Scan(
		&result.ID,
		&result.UserID,
		&result.Title,
		&result.EmploymentType,
		&result.GraduationYear,
		&result.CatalogStatus,
		&result.CreatedAt,
		&result.UpdatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return target.Target{}, target.ErrNotFound
		}
		return target.Target{}, fmt.Errorf("scan target: %w", err)
	}
	return result, nil
}
