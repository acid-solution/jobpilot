package config

import (
	"encoding/base64"
	"errors"
	"os"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
)

const defaultDevUserID = "00000000-0000-0000-0000-000000000001"

type Config struct {
	Environment                    string
	HTTPAddr                       string
	WorkerCount                    int
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
	values, err := loadValues(".env")
	if err != nil {
		return Config{}, err
	}
	return parse(values)
}

type configValues map[string]string

// Reading a file never changes process environment; deployment variables win,
// including explicitly empty values used to disable a file-provided setting.
func loadValues(path string) (configValues, error) {
	values, err := godotenv.Read(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		// Parser errors may include the offending line, which can contain a key.
		return nil, errors.New("cannot read .env configuration; check file permissions and syntax")
	}
	if values == nil {
		values = make(map[string]string)
	}
	for _, entry := range os.Environ() {
		key, value, found := strings.Cut(entry, "=")
		if found {
			values[key] = value
		}
	}
	return values, nil
}

func parse(values configValues) (Config, error) {
	environment := values.valueOrDefault("APP_ENV", "development")
	databaseURL := values["DATABASE_URL"]
	if databaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}

	devUserID, err := uuid.Parse(values.valueOrDefault("DEV_USER_ID", defaultDevUserID))
	if err != nil {
		return Config{}, errors.New("DEV_USER_ID must be a UUID")
	}
	encryptionKeyValue := values["CREDENTIAL_ENCRYPTION_KEY"]
	if encryptionKeyValue == "" {
		return Config{}, errors.New("CREDENTIAL_ENCRYPTION_KEY is required")
	}
	encryptionKey, err := base64.StdEncoding.DecodeString(encryptionKeyValue)
	if err != nil || len(encryptionKey) != 32 {
		return Config{}, errors.New("CREDENTIAL_ENCRYPTION_KEY must be a Base64-encoded 32-byte key")
	}
	authMode := values.valueOrDefault("AUTH_MODE", "shared")
	if authMode != "shared" && authMode != "dev" {
		return Config{}, errors.New("AUTH_MODE must be shared or dev")
	}
	if authMode == "dev" && environment != "development" {
		return Config{}, errors.New("AUTH_MODE=dev is only allowed in development")
	}

	reviewEnabled, err := strconv.ParseBool(values.valueOrDefault("ABILITY_REVIEW_ENABLED", "false"))
	if err != nil {
		return Config{}, errors.New("ABILITY_REVIEW_ENABLED must be true or false")
	}
	gradingEnabled, err := strconv.ParseBool(values.valueOrDefault("JD_ABILITY_GRADING_ENABLED", "false"))
	if err != nil {
		return Config{}, errors.New("JD_ABILITY_GRADING_ENABLED must be true or false")
	}
	classificationReviewEnabled, err := strconv.ParseBool(values.valueOrDefault("JOB_CLASSIFICATION_REVIEW_ENABLED", "false"))
	if err != nil {
		return Config{}, errors.New("JOB_CLASSIFICATION_REVIEW_ENABLED must be true or false")
	}
	embeddingEnabled, err := strconv.ParseBool(values.valueOrDefault("EMBEDDING_ENABLED", "false"))
	if err != nil {
		return Config{}, errors.New("EMBEDDING_ENABLED must be true or false")
	}
	agentEnabled, err := strconv.ParseBool(values.valueOrDefault("AGENT_ENABLED", "true"))
	if err != nil {
		return Config{}, errors.New("AGENT_ENABLED must be true or false")
	}
	userDailyLimit, err := values.nonNegativeInt("ABILITY_REVIEW_USER_DAILY_LIMIT", 3)
	if err != nil {
		return Config{}, err
	}
	globalDailyLimit, err := values.nonNegativeInt("ABILITY_REVIEW_GLOBAL_DAILY_LIMIT", 30)
	if err != nil {
		return Config{}, err
	}
	workerCount, err := strconv.Atoi(values.valueOrDefault("WORKER_COUNT", "2"))
	if err != nil || workerCount < 1 {
		return Config{}, errors.New("WORKER_COUNT must be a positive integer")
	}
	return Config{
		Environment:                    environment,
		HTTPAddr:                       values.valueOrDefault("HTTP_ADDR", "127.0.0.1:18081"),
		WorkerCount:                    workerCount,
		DatabaseURL:                    databaseURL,
		AuthMode:                       authMode,
		AuthJWKSURL:                    values.valueOrDefault("AUTH_JWKS_URL", "http://127.0.0.1:18082/.well-known/jwks.json"),
		AuthIssuer:                     values.valueOrDefault("AUTH_ISSUER", "shared-auth"),
		AuthAudience:                   values.valueOrDefault("AUTH_AUDIENCE", "jobpilot"),
		DevUserID:                      devUserID,
		CredentialEncryptionKey:        encryptionKey,
		DeepSeekBaseURL:                values.valueOrDefault("DEEPSEEK_BASE_URL", "https://api.deepseek.com"),
		EmbeddingBaseURL:               values.valueOrDefault("EMBEDDING_BASE_URL", ""),
		PlatformEmbeddingAPIKey:        values["PLATFORM_EMBEDDING_API_KEY"],
		EmbeddingEnabled:               embeddingEnabled,
		AgentEnabled:                   agentEnabled,
		GitHubToken:                    values["GITHUB_TOKEN"],
		AbilityReviewEnabled:           reviewEnabled,
		JDAbilityGradingEnabled:        gradingEnabled,
		JobClassificationReviewEnabled: classificationReviewEnabled,
		PlatformDeepSeekAPIKey:         values["PLATFORM_DEEPSEEK_API_KEY"],
		PlatformReviewModel:            values.valueOrDefault("PLATFORM_REVIEW_MODEL", "deepseek-chat"),
		AbilityReviewUserDailyLimit:    userDailyLimit,
		AbilityReviewGlobalDailyLimit:  globalDailyLimit,
	}, nil
}

func (values configValues) nonNegativeInt(key string, fallback int) (int, error) {
	value := values.valueOrDefault(key, strconv.Itoa(fallback))
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 {
		return 0, errors.New(key + " must be a non-negative integer")
	}
	return parsed, nil
}

func (values configValues) valueOrDefault(key, fallback string) string {
	if value := values[key]; value != "" {
		return value
	}
	return fallback
}
