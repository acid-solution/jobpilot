package identity

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

type accessClaims struct {
	SessionID string `json:"sid"`
	jwt.RegisteredClaims
}

type jwk struct {
	KeyType string `json:"kty"`
	Use     string `json:"use"`
	Alg     string `json:"alg"`
	KeyID   string `json:"kid"`
	N       string `json:"n"`
	E       string `json:"e"`
}

type jwksDocument struct {
	Keys []jwk `json:"keys"`
}

// JWKSResolver validates shared-auth access tokens locally. Only public keys are
// fetched from shared-auth; JobPilot never reads the authentication database.
type JWKSResolver struct {
	url      string
	issuer   string
	audience string
	client   *http.Client
	cacheTTL time.Duration
	now      func() time.Time

	mu        sync.RWMutex
	keys      map[string]*rsa.PublicKey
	expiresAt time.Time
}

func NewJWKSResolver(url, issuer, audience string, client *http.Client, cacheTTL time.Duration) *JWKSResolver {
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	if cacheTTL <= 0 {
		cacheTTL = 5 * time.Minute
	}
	return &JWKSResolver{
		url: url, issuer: issuer, audience: audience,
		client: client, cacheTTL: cacheTTL, now: time.Now,
		keys: make(map[string]*rsa.PublicKey),
	}
}

func (r *JWKSResolver) Resolve(request *http.Request) (uuid.UUID, error) {
	raw, err := bearerToken(request.Header.Get("Authorization"))
	if err != nil {
		return uuid.Nil, err
	}

	claims := &accessClaims{}
	parsed, err := jwt.ParseWithClaims(raw, claims, func(tokenValue *jwt.Token) (any, error) {
		if tokenValue.Method != jwt.SigningMethodRS256 {
			return nil, ErrInvalid
		}
		keyID, ok := tokenValue.Header["kid"].(string)
		if !ok || strings.TrimSpace(keyID) == "" {
			return nil, ErrInvalid
		}
		return r.key(request.Context(), keyID)
	},
		jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg()}),
		jwt.WithIssuer(r.issuer),
		jwt.WithAudience(r.audience),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
	)
	if err != nil {
		if errors.Is(err, ErrUnavailable) {
			return uuid.Nil, ErrUnavailable
		}
		return uuid.Nil, ErrInvalid
	}
	if !parsed.Valid || len(claims.Audience) != 1 || claims.Audience[0] != r.audience {
		return uuid.Nil, ErrInvalid
	}
	userID, err := uuid.Parse(claims.Subject)
	if err != nil || userID == uuid.Nil {
		return uuid.Nil, ErrInvalid
	}
	if sessionID, err := uuid.Parse(claims.SessionID); err != nil || sessionID == uuid.Nil {
		return uuid.Nil, ErrInvalid
	}
	return userID, nil
}

func bearerToken(value string) (string, error) {
	const prefix = "Bearer "
	if !strings.HasPrefix(value, prefix) {
		return "", ErrInvalid
	}
	raw := strings.TrimSpace(strings.TrimPrefix(value, prefix))
	if raw == "" {
		return "", ErrInvalid
	}
	return raw, nil
}

func (r *JWKSResolver) key(ctx context.Context, keyID string) (*rsa.PublicKey, error) {
	now := r.now()
	r.mu.RLock()
	key, found := r.keys[keyID]
	fresh := now.Before(r.expiresAt)
	r.mu.RUnlock()
	if found && fresh {
		return key, nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	now = r.now()
	if key, found = r.keys[keyID]; found && now.Before(r.expiresAt) {
		return key, nil
	}
	keys, err := r.fetch(ctx)
	if err != nil {
		return nil, err
	}
	r.keys = keys
	r.expiresAt = now.Add(r.cacheTTL)
	key, found = keys[keyID]
	if !found {
		return nil, ErrInvalid
	}
	return key, nil
}

func (r *JWKSResolver) fetch(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, r.url, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: create JWKS request", ErrUnavailable)
	}
	response, err := r.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("%w: fetch JWKS: %v", ErrUnavailable, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: JWKS returned %d", ErrUnavailable, response.StatusCode)
	}
	var document jwksDocument
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&document); err != nil {
		return nil, fmt.Errorf("%w: decode JWKS", ErrUnavailable)
	}
	keys := make(map[string]*rsa.PublicKey, len(document.Keys))
	for _, candidate := range document.Keys {
		if candidate.KeyType != "RSA" || candidate.Use != "sig" || candidate.Alg != "RS256" || candidate.KeyID == "" {
			continue
		}
		publicKey, err := rsaPublicKey(candidate.N, candidate.E)
		if err != nil {
			continue
		}
		keys[candidate.KeyID] = publicKey
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("%w: JWKS has no usable signing keys", ErrUnavailable)
	}
	return keys, nil
}

func rsaPublicKey(modulus, exponent string) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(modulus)
	if err != nil || len(nBytes) == 0 {
		return nil, errors.New("invalid RSA modulus")
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(exponent)
	if err != nil || len(eBytes) == 0 {
		return nil, errors.New("invalid RSA exponent")
	}
	eValue := new(big.Int).SetBytes(eBytes)
	if !eValue.IsInt64() || eValue.Int64() < 3 || eValue.Int64() > int64(^uint(0)>>1) {
		return nil, errors.New("invalid RSA exponent")
	}
	key := &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: int(eValue.Int64())}
	if key.N.BitLen() < 2048 {
		return nil, errors.New("RSA key is too small")
	}
	return key, nil
}
