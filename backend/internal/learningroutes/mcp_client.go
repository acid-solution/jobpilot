package learningroutes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var ErrRemoteRejected = errors.New("StudyFlow rejected route import")

type MCPClient struct {
	ExchangeURL, ServiceSecret, Endpoint string
	HTTP                                 *http.Client
}

func NewMCPClient(exchange, secret, endpoint string) (*MCPClient, error) {
	c := &MCPClient{ExchangeURL: exchange, ServiceSecret: secret, Endpoint: endpoint, HTTP: &http.Client{Timeout: 90 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	if exchange == "" && secret == "" && endpoint == "" {
		return c, nil
	}
	if len(secret) < 32 || !safeServiceURL(exchange) || !safeServiceURL(endpoint) {
		return nil, fmt.Errorf("invalid learning route integration configuration")
	}
	return c, nil
}
func safeServiceURL(raw string) bool {
	u, e := url.Parse(raw)
	return e == nil && u.Host != "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && (u.Scheme == "https" || (u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost")))
}
func (c *MCPClient) Enabled() bool {
	return c.Endpoint != "" && c.ServiceSecret != "" && c.ExchangeURL != ""
}

type bearerRoundTripper struct {
	token string
	base  http.RoundTripper
}

func (t bearerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	copy := req.Clone(req.Context())
	copy.Header = req.Header.Clone()
	copy.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(copy)
}

func (c *MCPClient) Send(ctx context.Context, sourceAccess string, request ImportRequest) (*Receipt, error) {
	if !c.Enabled() || sourceAccess == "" {
		return nil, ErrNotReady
	}
	form := url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:token-exchange"}, "subject_token_type": {"urn:ietf:params:oauth:token-type:access_token"}, "subject_token": {sourceAccess}, "resource": {c.Endpoint}, "scope": {"learning_route:import learning_route:read"}, "import_key": {request.Key}, "content_digest": {request.Digest}}
	call, err := http.NewRequestWithContext(ctx, http.MethodPost, c.ExchangeURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	call.SetBasicAuth("jobpilot", c.ServiceSecret)
	call.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := c.HTTP.Do(call)
	if err != nil {
		return nil, errors.New("token exchange unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, errors.New("token exchange rejected")
	}
	var exchanged struct {
		Token   string `json:"access_token"`
		Type    string `json:"token_type"`
		Expires int    `json:"expires_in"`
	}
	if err = json.NewDecoder(io.LimitReader(response.Body, 32768)).Decode(&exchanged); err != nil || exchanged.Token == "" || exchanged.Type != "Bearer" || exchanged.Expires <= 0 || exchanged.Expires > 120 {
		return nil, errors.New("invalid token exchange response")
	}
	base := c.HTTP.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "jobpilot-learning-route", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: c.Endpoint, HTTPClient: &http.Client{Timeout: 60 * time.Second, Transport: bearerRoundTripper{exchanged.Token, base}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, DisableStandaloneSSE: true, MaxRetries: 0}, nil)
	if err != nil {
		return nil, errors.New("StudyFlow MCP unavailable")
	}
	defer session.Close()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "import_learning_route", Arguments: struct {
		Key     string  `json:"idempotency_key"`
		Payload Payload `json:"payload"`
	}{request.Key, request.Payload}})
	if err != nil {
		return nil, errors.New("StudyFlow MCP response unavailable")
	}
	if result.IsError {
		return nil, ErrRemoteRejected
	}
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		return nil, errors.New("invalid StudyFlow receipt")
	}
	var receipt Receipt
	if err = json.Unmarshal(raw, &receipt); err != nil {
		return nil, errors.New("invalid StudyFlow receipt")
	}
	return &receipt, nil
}
