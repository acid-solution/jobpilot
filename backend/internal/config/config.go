package config

import (
	"encoding/base64"
	"errors"
	"os"
	"strconv"

	"github.com/google/uuid"
)

const defaultDevUserID = "00000000-0000-0000-0000-000000000001"

type Config struct {
	Environment                    string
	HTTPAddr                       string
	DatabaseURL                    string
	AuthMode                       string
	AuthJWKSURL                    string
	AuthIssuer                     string
	AuthAudience                   string
	DevUserID                      uuid.UUID
	CredentialEncryptionKey        []byte
	DeepSeekBaseURL                string
	EmbeddingBaseURL               string
	PlatformEmbeddingAPIKey        string
	EmbeddingEnabled               bool
	AgentEnabled                   bool
	GitHubToken                    string
	AbilityReviewEnabled           bool
	JDAbilityGradingEnabled        bool
	JobClassificationReviewEnabled bool
	PlatformDeepSeekAPIKey         string
	PlatformReviewModel            string
	AbilityReviewUserDailyLimit    int
	AbilityReviewGlobalDailyLimit  int
}

func Load() (Config, error) {
	environment := valueOrDefault("APP_ENV", "development")
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}

	devUserID, err := uuid.Parse(valueOrDefault("DEV_USER_ID", defaultDevUserID))
	if err != nil {
		return Config{}, errors.New("DEV_USER_ID must be a UUID")
	}
	encryptionKeyValue := os.Getenv("CREDENTIAL_ENCRYPTION_KEY")
	if encryptionKeyValue == "" {
		return Config{}, errors.New("CREDENTIAL_ENCRYPTION_KEY is required")
	}
	encryptionKey, err := base64.StdEncoding.DecodeString(encryptionKeyValue)
	if err != nil || len(encryptionKey) != 32 {
		return Config{}, errors.New("CREDENTIAL_ENCRYPTION_KEY must be a Base64-encoded 32-byte key")
	}
	authMode := valueOrDefault("AUTH_MODE", "shared")
	if authMode != "shared" && authMode != "dev" {
		return Config{}, errors.New("AUTH_MODE must be shared or dev")
	}
	if authMode == "dev" && environment != "development" {
		return Config{}, errors.New("AUTH_MODE=dev is only allowed in development")
	}

	reviewEnabled, err := strconv.ParseBool(valueOrDefault("ABILITY_REVIEW_ENABLED", "false"))
	if err != nil {
		return Config{}, errors.New("ABILITY_REVIEW_ENABLED must be true or false")
	}
	gradingEnabled, err := strconv.ParseBool(valueOrDefault("JD_ABILITY_GRADING_ENABLED", "false"))
	if err != nil {
		return Config{}, errors.New("JD_ABILITY_GRADING_ENABLED must be true or false")
	}
	classificationReviewEnabled, err := strconv.ParseBool(valueOrDefault("JOB_CLASSIFICATION_REVIEW_ENABLED", "false"))
	if err != nil {
		return Config{}, errors.New("JOB_CLASSIFICATION_REVIEW_ENABLED must be true or false")
	}
	embeddingEnabled, err := strconv.ParseBool(valueOrDefault("EMBEDDING_ENABLED", "false"))
	if err != nil {
		return Config{}, errors.New("EMBEDDING_ENABLED must be true or false")
	}
	agentEnabled, err := strconv.ParseBool(valueOrDefault("AGENT_ENABLED", "true"))
	if err != nil {
		return Config{}, errors.New("AGENT_ENABLED must be true or false")
	}
	userDailyLimit, err := nonNegativeInt("ABILITY_REVIEW_USER_DAILY_LIMIT", 3)
	if err != nil {
		return Config{}, err
	}
	globalDailyLimit, err := nonNegativeInt("ABILITY_REVIEW_GLOBAL_DAILY_LIMIT", 30)
	if err != nil {
		return Config{}, err
	}
	return Config{
		Environment:                    environment,
		HTTPAddr:                       valueOrDefault("HTTP_ADDR", "127.0.0.1:18081"),
		DatabaseURL:                    databaseURL,
		AuthMode:                       authMode,
		AuthJWKSURL:                    valueOrDefault("AUTH_JWKS_URL", "http://127.0.0.1:18082/.well-known/jwks.json"),
		AuthIssuer:                     valueOrDefault("AUTH_ISSUER", "shared-auth"),
		AuthAudience:                   valueOrDefault("AUTH_AUDIENCE", "jobpilot"),
		DevUserID:                      devUserID,
		CredentialEncryptionKey:        encryptionKey,
		DeepSeekBaseURL:                valueOrDefault("DEEPSEEK_BASE_URL", "https://api.deepseek.com"),
		EmbeddingBaseURL:               valueOrDefault("EMBEDDING_BASE_URL", ""),
		PlatformEmbeddingAPIKey:        os.Getenv("PLATFORM_EMBEDDING_API_KEY"),
		EmbeddingEnabled:               embeddingEnabled,
		AgentEnabled:                   agentEnabled,
		GitHubToken:                    os.Getenv("GITHUB_TOKEN"),
		AbilityReviewEnabled:           reviewEnabled,
		JDAbilityGradingEnabled:        gradingEnabled,
		JobClassificationReviewEnabled: classificationReviewEnabled,
		PlatformDeepSeekAPIKey:         os.Getenv("PLATFORM_DEEPSEEK_API_KEY"),
		PlatformReviewModel:            valueOrDefault("PLATFORM_REVIEW_MODEL", "deepseek-chat"),
		AbilityReviewUserDailyLimit:    userDailyLimit,
		AbilityReviewGlobalDailyLimit:  globalDailyLimit,
	}, nil
}

func nonNegativeInt(key string, fallback int) (int, error) {
	value := valueOrDefault(key, strconv.Itoa(fallback))
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 {
		return 0, errors.New(key + " must be a non-negative integer")
	}
	return parsed, nil
}

func valueOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
