package modelconfig

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/LeoninCS/jobpilot-next/backend/internal/secure"
	"github.com/google/uuid"
)

const (
	ProviderDeepSeek     = "deepseek"
	DefaultDeepSeekModel = "deepseek-flash"
)

var (
	ErrNotConfigured = errors.New("model is not configured")
	ErrValidation    = errors.New("invalid model configuration")
)

type StoredConfig struct {
	UserID       uuid.UUID
	Provider     string
	Model        string
	EncryptedKey []byte
	KeyHint      string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type Metadata struct {
	Provider   string     `json:"provider"`
	Model      string     `json:"model"`
	Configured bool       `json:"configured"`
	KeyHint    string     `json:"key_hint,omitempty"`
	UpdatedAt  *time.Time `json:"updated_at,omitempty"`
}

type Credentials struct {
	Provider string
	Model    string
	APIKey   string
}

type Repository interface {
	Find(context.Context, uuid.UUID, string) (StoredConfig, error)
	Upsert(context.Context, StoredConfig) (StoredConfig, error)
	Delete(context.Context, uuid.UUID, string) error
}

type ConnectionTester interface {
	TestConnection(context.Context, string, string) error
}

type Service struct {
	repository Repository
	cipher     *secure.Cipher
	tester     ConnectionTester
}

func NewService(repository Repository, cipher *secure.Cipher, tester ConnectionTester) *Service {
	return &Service{repository: repository, cipher: cipher, tester: tester}
}

func (s *Service) Get(ctx context.Context, userID uuid.UUID) (Metadata, error) {
	stored, err := s.repository.Find(ctx, userID, ProviderDeepSeek)
	if errors.Is(err, ErrNotConfigured) {
		return Metadata{Provider: ProviderDeepSeek, Model: DefaultDeepSeekModel, Configured: false}, nil
	}
	if err != nil {
		return Metadata{}, err
	}
	return metadata(stored), nil
}

func (s *Service) Save(ctx context.Context, userID uuid.UUID, model, apiKey string) (Metadata, error) {
	model = strings.TrimSpace(model)
	apiKey = strings.TrimSpace(apiKey)
	if model == "" {
		model = DefaultDeepSeekModel
	}
	if model != DefaultDeepSeekModel || len(apiKey) < 8 || len(apiKey) > 500 {
		return Metadata{}, ErrValidation
	}
	encryptedKey, err := s.cipher.Encrypt(apiKey)
	if err != nil {
		return Metadata{}, err
	}
	stored, err := s.repository.Upsert(ctx, StoredConfig{
		UserID: userID, Provider: ProviderDeepSeek, Model: model,
		EncryptedKey: encryptedKey, KeyHint: keyHint(apiKey),
	})
	if err != nil {
		return Metadata{}, err
	}
	return metadata(stored), nil
}

func (s *Service) Test(ctx context.Context, userID uuid.UUID, model, apiKey string) error {
	model = strings.TrimSpace(model)
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		credentials, err := s.Credentials(ctx, userID)
		if err != nil {
			return err
		}
		model, apiKey = credentials.Model, credentials.APIKey
	}
	if model == "" {
		model = DefaultDeepSeekModel
	}
	if model != DefaultDeepSeekModel || len(apiKey) < 8 {
		return ErrValidation
	}
	return s.tester.TestConnection(ctx, apiKey, model)
}

func (s *Service) Delete(ctx context.Context, userID uuid.UUID) error {
	return s.repository.Delete(ctx, userID, ProviderDeepSeek)
}

func (s *Service) Credentials(ctx context.Context, userID uuid.UUID) (Credentials, error) {
	stored, err := s.repository.Find(ctx, userID, ProviderDeepSeek)
	if err != nil {
		return Credentials{}, err
	}
	apiKey, err := s.cipher.Decrypt(stored.EncryptedKey)
	if err != nil {
		return Credentials{}, err
	}
	return Credentials{Provider: stored.Provider, Model: stored.Model, APIKey: apiKey}, nil
}

func metadata(stored StoredConfig) Metadata {
	return Metadata{
		Provider: stored.Provider, Model: stored.Model, Configured: true,
		KeyHint: stored.KeyHint, UpdatedAt: &stored.UpdatedAt,
	}
}

func keyHint(apiKey string) string {
	runes := []rune(apiKey)
	if len(runes) <= 4 {
		return "••••"
	}
	return "••••" + string(runes[len(runes)-4:])
}
