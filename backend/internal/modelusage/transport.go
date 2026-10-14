package modelusage

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Transport observes supplier metadata only. Request text and streamed content
// are never persisted, emitted to logs, or forwarded to the accounting API.
type Transport struct {
	Base     http.RoundTripper
	Repo     Repository
	Provider string
	Payer    string
	Pricing  Pricing
}

func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	s := From(req.Context())
	if s.Purpose == "" || s.OperationID == uuid.Nil {
		return nil, ErrUnavailable
	}
	payer := s.Payer
	if t.Payer != "" {
		payer = t.Payer
	}
	if payer == "" {
		payer = "user"
	}
	c := Call{ID: uuid.New(), Scope: s, Provider: t.Provider, Payer: payer, Purpose: s.Purpose, OperationID: OperationKey(s), StartedAt: time.Now(), Result: "pending", UsageStatus: "unknown"}
	if req.GetBody != nil {
		if b, e := req.GetBody(); e == nil {
			var v struct {
				Model string `json:"model"`
			}
			_ = json.NewDecoder(io.LimitReader(b, 2<<20)).Decode(&v)
			b.Close()
			if len(v.Model) <= 100 {
				c.RequestedModel = v.Model
			}
		}
	}
	if err := t.Repo.Begin(req.Context(), c); err != nil {
		return nil, ErrUnavailable
	}
	finish := func() {
		c.DurationMS = time.Since(c.StartedAt).Milliseconds()
		t.Pricing.Apply(&c)
		ctx, cancel := context.WithTimeout(context.WithoutCancel(req.Context()), 5*time.Second)
		defer cancel()
		if e := t.Repo.Finish(ctx, c); e != nil {
			slog.Error("persist model usage failed", "call_id", c.ID, "error_type", "database_error")
		}
	}
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	resp, err := base.RoundTrip(req)
	if err != nil {
		c.Result = "transport_error"
		finish()
		return resp, err
	}
	c.Confirmed = true // A supplier response establishes dispatch; a crash or dial error does not.
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		c.Result = "provider_success"
	} else {
		c.Result = "http_error"
	}
	if strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		resp.Body = &streamBody{ReadCloser: resp.Body, call: &c, finish: finish}
		return resp, nil
	}
	// JSON calls are already non-streaming. Finish usage before the business
	// decoder can reject empty content, output limits, or invalid generated JSON.
	raw, e := io.ReadAll(io.LimitReader(resp.Body, (24<<20)+1))
	_ = resp.Body.Close()
	if e != nil || len(raw) > 24<<20 {
		c.Result = "response_incomplete"
		finish()
		if e == nil {
			e = io.ErrUnexpectedEOF
		}
		return nil, e
	}
	extract(raw, &c)
	finish()
	resp.Body = io.NopCloser(bytes.NewReader(raw))
	return resp, nil
}
func extract(raw []byte, c *Call) {
	var envelope struct {
		ID        string `json:"id"`
		RequestID string `json:"request_id"`
		Model     string `json:"model"`
		Usage     *struct {
			Prompt     *int64 `json:"prompt_tokens"`
			Completion *int64 `json:"completion_tokens"`
			Total      *int64 `json:"total_tokens"`
			Hit        *int64 `json:"prompt_cache_hit_tokens"`
			Miss       *int64 `json:"prompt_cache_miss_tokens"`
			Details    struct {
				Cached *int64 `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
		} `json:"usage"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		return
	}
	if envelope.ID != "" && len(envelope.ID) <= 200 {
		c.ProviderRequestID = envelope.ID
	} else if len(envelope.RequestID) <= 200 {
		c.ProviderRequestID = envelope.RequestID
	}
	if envelope.Model != "" && len(envelope.Model) <= 100 {
		c.ReturnedModel = envelope.Model
	}
	if envelope.Usage == nil {
		return
	}
	u := envelope.Usage
	for _, n := range []*int64{u.Prompt, u.Completion, u.Total, u.Hit, u.Miss, u.Details.Cached} {
		if n != nil && (*n < 0 || *n > 1000000000) {
			return
		}
	}
	if u.Hit == nil {
		u.Hit = u.Details.Cached
	}
	if u.Prompt != nil && ((u.Hit != nil && *u.Hit > *u.Prompt) || (u.Miss != nil && *u.Miss > *u.Prompt)) {
		return
	}
	if u.Prompt != nil && u.Hit != nil && u.Miss != nil && *u.Hit+*u.Miss != *u.Prompt {
		return
	}
	if u.Total != nil && u.Prompt != nil && u.Completion != nil && *u.Total != *u.Prompt+*u.Completion {
		return
	}
	c.Tokens = Tokens{Input: u.Prompt, Output: u.Completion, Total: u.Total, CacheHit: u.Hit, CacheMiss: u.Miss}
	if c.Provider == "bailian" {
		c.Input = u.Total
		c.Output = nil
		c.CacheHit = nil
		c.CacheMiss = nil
		if u.Total != nil {
			c.UsageStatus = "known"
		}
		return
	}
	if u.Prompt != nil && u.Completion != nil {
		c.UsageStatus = "known"
	} else {
		c.UsageStatus = "partial"
	}
}

type streamBody struct {
	io.ReadCloser
	call      *Call
	finish    func()
	pending   []byte
	mu        sync.Mutex
	finalized bool
	overflow  bool
}

func (b *streamBody) Read(p []byte) (int, error) {
	n, e := b.ReadCloser.Read(p)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.finalized {
		return n, e
	}
	for _, ch := range p[:n] {
		if ch == '\n' {
			if !b.overflow {
				line := bytes.TrimSpace(b.pending)
				if bytes.HasPrefix(line, []byte("data:")) {
					payload := bytes.TrimSpace(line[5:])
					if !bytes.Equal(payload, []byte("[DONE]")) {
						extract(payload, b.call)
					}
				}
			}
			b.pending = b.pending[:0]
			b.overflow = false
		} else if !b.overflow {
			if len(b.pending) >= 4<<20 {
				b.pending = b.pending[:0]
				b.overflow = true
			} else {
				b.pending = append(b.pending, ch)
			}
		}
	}
	if e != nil {
		if e != io.EOF && b.call.UsageStatus == "unknown" {
			b.call.Result = "response_incomplete"
		}
		b.finalized = true
		b.finish()
	}
	return n, e
}
func (b *streamBody) Close() error {
	e := b.ReadCloser.Close()
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.finalized {
		b.finalized = true
		b.finish()
	}
	return e
}
