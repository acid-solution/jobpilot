package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/LeoninCS/jobpilot-next/backend/internal/config"
	"github.com/LeoninCS/jobpilot-next/backend/internal/embedding"
	"github.com/LeoninCS/jobpilot-next/backend/internal/model/deepseek"
	"github.com/LeoninCS/jobpilot-next/backend/internal/modelconfig"
	"github.com/LeoninCS/jobpilot-next/backend/internal/modelusage"
	"github.com/LeoninCS/jobpilot-next/backend/internal/secure"
	"github.com/google/uuid"
)

// This observer keeps only provider accounting metadata, never prompt,
// response content, credentials, or embedding vectors.
type providerUsageObserver struct {
	metadata []struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Usage struct {
			Input  *int64 `json:"prompt_tokens"`
			Output *int64 `json:"completion_tokens"`
			Total  *int64 `json:"total_tokens"`
			Hit    *int64 `json:"prompt_cache_hit_tokens"`
			Miss   *int64 `json:"prompt_cache_miss_tokens"`
		} `json:"usage"`
	}
}

func (o *providerUsageObserver) RoundTrip(req *http.Request) (*http.Response, error) {
	response, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		return response, err
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 24<<20))
	response.Body.Close()
	if err != nil {
		return nil, err
	}
	o.metadata = append(o.metadata, struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Usage struct {
			Input  *int64 `json:"prompt_tokens"`
			Output *int64 `json:"completion_tokens"`
			Total  *int64 `json:"total_tokens"`
			Hit    *int64 `json:"prompt_cache_hit_tokens"`
			Miss   *int64 `json:"prompt_cache_miss_tokens"`
		} `json:"usage"`
	}{})
	_ = json.Unmarshal(raw, &o.metadata[len(o.metadata)-1])
	response.Body = io.NopCloser(bytes.NewReader(raw))
	return response, nil
}

func TestLiveModelUsageProviderMetadata(t *testing.T) {
	if os.Getenv("RUN_MODEL_USAGE_LIVE_TEST") != "1" {
		t.Skip("requires explicit opt-in and dedicated test account")
	}
	user, err := uuid.Parse(os.Getenv("MODEL_USAGE_TEST_USER"))
	if err != nil || user == uuid.Nil {
		t.Fatal("dedicated test account required")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal("invalid local configuration")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	db, err := Open(ctx, cfg.DatabaseURL)
	if err != nil {
		t.Fatal("test database unavailable")
	}
	defer db.Close()
	cipher, err := secure.NewCipher(cfg.CredentialEncryptionKey)
	if err != nil {
		t.Fatal("cipher unavailable")
	}
	credentials, err := modelconfig.NewService(NewModelConfigRepository(db), cipher, nil).Credentials(ctx, user)
	if err != nil {
		t.Fatal("test model configuration unavailable")
	}
	repo := NewModelUsageRepository(db)
	for _, provider := range []string{"deepseek", "bailian"} {
		t.Run(provider, func(t *testing.T) {
			operation := uuid.New()
			payer := "user"
			purpose := "connection_test"
			if provider == "bailian" {
				payer = "platform"
				purpose = "embedding"
			}
			scoped := modelusage.For(ctx, user, operation, purpose, payer)
			observer := &providerUsageObserver{}
			client := &http.Client{Timeout: 45 * time.Second, Transport: &modelusage.Transport{Base: observer, Repo: repo, Provider: provider, Payer: payer}}
			if provider == "deepseek" {
				err = deepseek.NewClient(cfg.DeepSeekBaseURL, client).TestConnection(scoped, credentials.APIKey, credentials.Model)
			} else {
				_, err = embedding.NewClient(cfg.EmbeddingBaseURL, cfg.PlatformEmbeddingAPIKey, client).Embed(scoped, []string{"模型用量测试：Go 后端开发"})
			}
			if err != nil {
				t.Fatal("supplier call did not complete")
			}
			page, e := repo.Calls(ctx, user, modelusage.Filter{From: time.Now().Add(-time.Hour), To: time.Now().Add(time.Hour), OperationID: modelusage.OperationKey(modelusage.From(scoped)), Page: 1, PageSize: 10})
			if e != nil || page.Total != 1 || len(observer.metadata) != 1 {
				t.Fatal("request was not recorded exactly once")
			}
			actual := observer.metadata[0]
			recorded := page.Items[0]
			eq := func(a, b *int64) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }
			if recorded.ProviderRequestID != actual.ID || recorded.ReturnedModel != actual.Model || !eq(recorded.Total, actual.Usage.Total) {
				t.Fatal("supplier metadata differs from ledger")
			}
			if provider == "deepseek" {
				if !eq(recorded.Input, actual.Usage.Input) || !eq(recorded.Output, actual.Usage.Output) || !eq(recorded.CacheHit, actual.Usage.Hit) || !eq(recorded.CacheMiss, actual.Usage.Miss) {
					t.Fatal("DeepSeek token mismatch")
				}
			} else if !eq(recorded.Input, actual.Usage.Total) || recorded.Output != nil {
				t.Fatal("embedding token mismatch")
			}
			t.Logf("%s provider usage exactly matches ledger; status=%s", provider, recorded.UsageStatus)
		})
	}
}
