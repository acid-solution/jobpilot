package identity

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

func TestJWKSResolverAcceptsSharedAuthToken(t *testing.T) {
	privateKey, server := testJWKS(t)
	defer server.Close()

	userID := uuid.New()
	raw := signTestToken(t, privateKey, userID, "jobpilot")
	resolver := NewJWKSResolver(server.URL, "shared-auth", "jobpilot", server.Client(), time.Minute)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/targets/current", nil)
	request.Header.Set("Authorization", "Bearer "+raw)

	resolved, err := resolver.Resolve(request)
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if resolved != userID {
		t.Fatalf("resolved user %s, want %s", resolved, userID)
	}
}

func TestJWKSResolverRejectsWrongAudience(t *testing.T) {
	privateKey, server := testJWKS(t)
	defer server.Close()

	raw := signTestToken(t, privateKey, uuid.New(), "studyflow")
	resolver := NewJWKSResolver(server.URL, "shared-auth", "jobpilot", server.Client(), time.Minute)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/targets/current", nil)
	request.Header.Set("Authorization", "Bearer "+raw)

	if _, err := resolver.Resolve(request); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Resolve error = %v, want ErrInvalid", err)
	}
}

func TestJWKSResolverReportsUnavailableJWKS(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	resolver := NewJWKSResolver(server.URL, "shared-auth", "jobpilot", server.Client(), time.Minute)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/targets/current", nil)
	request.Header.Set("Authorization", "Bearer eyJhbGciOiJSUzI1NiIsImtpZCI6InRlc3Qta2V5In0.eyJpc3MiOiJzaGFyZWQtYXV0aCJ9.c2ln")

	if _, err := resolver.Resolve(request); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Resolve error = %v, want ErrUnavailable", err)
	}
}

func testJWKS(t *testing.T) (*rsa.PrivateKey, *httptest.Server) {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	exponent := big.NewInt(int64(privateKey.PublicKey.E)).Bytes()
	document := jwksDocument{Keys: []jwk{{
		KeyType: "RSA", Use: "sig", Alg: "RS256", KeyID: "test-key",
		N: base64.RawURLEncoding.EncodeToString(privateKey.PublicKey.N.Bytes()),
		E: base64.RawURLEncoding.EncodeToString(exponent),
	}}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(document)
	}))
	return privateKey, server
}

func signTestToken(t *testing.T, privateKey *rsa.PrivateKey, userID uuid.UUID, audience string) string {
	t.Helper()
	now := time.Now()
	claims := accessClaims{
		SessionID: uuid.NewString(),
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer: "shared-auth", Subject: userID.String(),
			Audience: jwt.ClaimStrings{audience},
			IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(time.Minute)),
		},
	}
	tokenValue := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tokenValue.Header["kid"] = "test-key"
	raw, err := tokenValue.SignedString(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
