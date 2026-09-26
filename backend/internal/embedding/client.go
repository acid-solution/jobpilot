package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
)

const Model = "text-embedding-v4"
const Dimensions = 1024

var ErrNotConfigured = errors.New("platform embedding is not configured")

type Client struct {
	key string
	endpoint string
	http *http.Client
}

func NewClient(endpoint, key string, client *http.Client) *Client {
	if client == nil { client = &http.Client{Timeout: 45 * time.Second} }
	return &Client{key: strings.TrimSpace(key), endpoint: strings.TrimRight(endpoint, "/"), http: client}
}

func (c *Client) Configured() bool { return c.key != "" && c.endpoint != "" }

func (c *Client) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if !c.Configured() { return nil, ErrNotConfigured }
	if len(texts) == 0 || len(texts) > 10 { return nil, errors.New("embedding batch must contain 1-10 texts") }
	for _, value := range texts { if strings.TrimSpace(value) == "" { return nil, errors.New("empty embedding input") } }
	payload, _ := json.Marshal(map[string]any{"model": Model, "input": texts, "dimensions": Dimensions})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+"/embeddings", bytes.NewReader(payload))
	if err != nil { return nil, err }
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil { return nil, err }
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("embedding provider returned HTTP %d", resp.StatusCode)
	}
	var body struct { Data []struct { Index int `json:"index"`; Embedding []float32 `json:"embedding"` } `json:"data"` }
	if err := json.NewDecoder(io.LimitReader(resp.Body, 20<<20)).Decode(&body); err != nil { return nil, err }
	if len(body.Data) != len(texts) { return nil, errors.New("embedding response count mismatch") }
	result := make([][]float32, len(texts))
	for _, item := range body.Data {
		if item.Index < 0 || item.Index >= len(texts) || result[item.Index] != nil || len(item.Embedding) != Dimensions { return nil, errors.New("invalid embedding response") }
		for _, number := range item.Embedding { if math.IsNaN(float64(number)) || math.IsInf(float64(number), 0) { return nil, errors.New("invalid embedding number") } }
		result[item.Index] = item.Embedding
	}
	return result, nil
}

type Chunk struct { Start, End int; Text string }

// Split keeps rune offsets so citations can be checked against the stored source.
func Split(value string) []Chunk {
	runes := []rune(value)
	if len(runes) == 0 { return nil }
	chunks := make([]Chunk, 0, len(runes)/700+1)
	for start := 0; start < len(runes); {
		end := start+800
		if end > len(runes) { end = len(runes) }
		if end < len(runes) {
			for candidate := end; candidate > start+400; candidate-- {
				if runes[candidate-1] == '\n' || runes[candidate-1] == '。' { end = candidate; break }
			}
		}
		chunks = append(chunks, Chunk{Start: start, End: end, Text: string(runes[start:end])})
		if end == len(runes) { break }
		start = end-120
	}
	return chunks
}
