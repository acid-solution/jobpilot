package modelconfig

import (
	"bytes"
	"context"
	"testing"

	"github.com/LeoninCS/jobpilot-next/backend/internal/secure"
	"github.com/google/uuid"
)

type repositoryStub struct {
	stored StoredConfig
}

func (r *repositoryStub) Find(context.Context, uuid.UUID, string) (StoredConfig, error) {
	if len(r.stored.EncryptedKey) == 0 {
		return StoredConfig{}, ErrNotConfigured
	}
	return r.stored, nil
}

func (r *repositoryStub) Upsert(_ context.Context, config StoredConfig) (StoredConfig, error) {
	config.UpdatedAt = config.CreatedAt
	r.stored = config
	return config, nil
}

func (r *repositoryStub) Delete(context.Context, uuid.UUID, string) error {
	r.stored = StoredConfig{}
	return nil
}

type testerStub struct{}

func (testerStub) TestConnection(context.Context, string, string) error { return nil }

func TestSaveDoesNotPersistPlaintextAPIKey(t *testing.T) {
	cipher, err := secure.NewCipher(bytes.Repeat([]byte{3}, 32))
	if err != nil {
		t.Fatal(err)
	}
	repository := &repositoryStub{}
	service := NewService(repository, cipher, testerStub{})
	apiKey := "sk-this-key-must-stay-secret"

	metadata, err := service.Save(context.Background(), uuid.New(), DefaultDeepSeekModel, apiKey)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if bytes.Contains(repository.stored.EncryptedKey, []byte(apiKey)) {
		t.Fatal("repository received plaintext API key")
	}
	if metadata.KeyHint != "••••cret" {
		t.Fatalf("unexpected key hint: %q", metadata.KeyHint)
	}
	credentials, err := service.Credentials(context.Background(), repository.stored.UserID)
	if err != nil {
		t.Fatalf("Credentials: %v", err)
	}
	if credentials.APIKey != apiKey {
		t.Fatal("decrypted key differs from saved key")
	}
}
