package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/LeoninCS/jobpilot-next/backend/internal/modelconfig"
	"github.com/google/uuid"
)

type ModelConfigRepository struct {
	database *repositoryDatabase
}

func NewModelConfigRepository(database *sql.DB) *ModelConfigRepository {
	return &ModelConfigRepository{database: newRepositoryDatabase(database)}
}

func (r *ModelConfigRepository) Find(ctx context.Context, userID uuid.UUID, provider string) (modelconfig.StoredConfig, error) {
	const query = `
		SELECT user_id, provider, model, api_key_ciphertext, api_key_hint, created_at, updated_at
		FROM model_configs
		WHERE user_id = $1 AND provider = $2`
	return scanModelConfig(r.database.QueryRowContext(ctx, query, userID, provider))
}

func (r *ModelConfigRepository) Upsert(ctx context.Context, config modelconfig.StoredConfig) (modelconfig.StoredConfig, error) {
	const query = `
		INSERT INTO model_configs (user_id, provider, model, api_key_ciphertext, api_key_hint)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (user_id, provider) DO UPDATE
		SET model = EXCLUDED.model,
		    api_key_ciphertext = EXCLUDED.api_key_ciphertext,
		    api_key_hint = EXCLUDED.api_key_hint,
		    updated_at = NOW()
		RETURNING user_id, provider, model, api_key_ciphertext, api_key_hint, created_at, updated_at`
	return scanModelConfig(r.database.QueryRowContext(
		ctx, query, config.UserID, config.Provider, config.Model, config.EncryptedKey, config.KeyHint,
	))
}

func (r *ModelConfigRepository) Delete(ctx context.Context, userID uuid.UUID, provider string) error {
	_, err := r.database.ExecContext(ctx, `DELETE FROM model_configs WHERE user_id = $1 AND provider = $2`, userID, provider)
	if err != nil {
		return fmt.Errorf("delete model config: %w", err)
	}
	return nil
}

func scanModelConfig(row rowScanner) (modelconfig.StoredConfig, error) {
	var result modelconfig.StoredConfig
	if err := row.Scan(
		&result.UserID,
		&result.Provider,
		&result.Model,
		&result.EncryptedKey,
		&result.KeyHint,
		&result.CreatedAt,
		&result.UpdatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return modelconfig.StoredConfig{}, modelconfig.ErrNotConfigured
		}
		return modelconfig.StoredConfig{}, fmt.Errorf("scan model config: %w", err)
	}
	return result, nil
}
