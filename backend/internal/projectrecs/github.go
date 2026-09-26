package projectrecs

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

var ErrGitHubUnavailable = errors.New("github search unavailable")

type RepoEvidence struct {
	FullName       string         `json:"full_name"`
	URL            string         `json:"url"`
	Description    string         `json:"description"`
	Language       string         `json:"language"`
	Archived       bool           `json:"archived"`
	Readme         string         `json:"readme"`
	ReadmeURL      string         `json:"readme_url"`
	AdditionalDocs []RepoDocument `json:"additional_docs"`
	CodeEntries    []string       `json:"code_entries"`
	CheckedAt      time.Time      `json:"checked_at"`
}
type RepoDocument struct {
	URL     string `json:"url"`
	Content string `json:"content"`
}

var markdownDocLink = regexp.MustCompile(`\]\((?:\./)?(docs/[A-Za-z0-9_./-]+\.md)(?:#[^)]*)?\)`)

type Research struct {
	DraftID      string         `json:"draft_id"`
	Queries      []string       `json:"queries"`
	Repositories []RepoEvidence `json:"repositories"`
	Incomplete   bool           `json:"incomplete"`
}
type Searcher interface {
	Research(context.Context, Draft) (Research, error)
}

type GitHubClient struct {
	baseURL  string
	token    string
	client   *http.Client
	interval time.Duration
	mu       sync.Mutex
	next     time.Time
}

func NewGitHubClient(baseURL, token string, client *http.Client) *GitHubClient {
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	interval := 7 * time.Second
	if token != "" {
		interval = 2100 * time.Millisecond
	}
	return &GitHubClient{baseURL: strings.TrimRight(baseURL, "/"), token: token, client: client, interval: interval}
}
func (g *GitHubClient) wait(ctx context.Context) error {
	g.mu.Lock()
	until := g.next
	if until.Before(time.Now()) {
		until = time.Now()
	}
	g.next = until.Add(g.interval)
	g.mu.Unlock()
	if delay := time.Until(until); delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	return nil
}
func (g *GitHubClient) get(ctx context.Context, path string, out any) (int, error) {
	for attempt := 0; attempt < 3; attempt++ {
		status, err := g.getOnce(ctx, path, out)
		if err == nil || ctx.Err() != nil || status != 0 && status < 500 {
			return status, err
		}
		if attempt == 2 {
			return status, err
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(time.Duration(attempt+1) * time.Second):
		}
	}
	return 0, ErrGitHubUnavailable
}
func (g *GitHubClient) getOnce(ctx context.Context, path string, out any) (int, error) {
	if err := g.wait(ctx); err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.baseURL+path, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "JobPilot-project-recommendations")
	if g.token != "" {
		req.Header.Set("Authorization", "Bearer "+g.token)
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrGitHubUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return resp.StatusCode, nil
	}
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusForbidden {
		until := time.Now().Add(time.Minute)
		if value := resp.Header.Get("Retry-After"); value != "" {
			if seconds, parseErr := time.ParseDuration(value + "s"); parseErr == nil {
				until = time.Now().Add(seconds)
			}
		}
		if value := resp.Header.Get("X-RateLimit-Reset"); value != "" {
			if unix, parseErr := strconv.ParseInt(value, 10, 64); parseErr == nil && time.Unix(unix, 0).After(until) {
				until = time.Unix(unix, 0)
			}
		}
		g.mu.Lock()
		if until.After(g.next) {
			g.next = until
		}
		g.mu.Unlock()
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return resp.StatusCode, fmt.Errorf("%w: HTTP %d", ErrGitHubUnavailable, resp.StatusCode)
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(out); err != nil {
		return resp.StatusCode, fmt.Errorf("%w: %v", ErrGitHubUnavailable, err)
	}
	return resp.StatusCode, nil
}
func (g *GitHubClient) Research(ctx context.Context, d Draft) (Research, error) {
	r := Research{DraftID: d.ID, Queries: []string{}, Repositories: []RepoEvidence{}}
	type item struct {
		FullName    string `json:"full_name"`
		HTMLURL     string `json:"html_url"`
		Description string `json:"description"`
		Language    string `json:"language"`
		Archived    bool   `json:"archived"`
		Fork        bool   `json:"fork"`
	}
	groups := [][]item{}
	for _, q := range d.SearchQueries {
		q = cleanSearchQuery(q)
		if q == "" {
			continue
		}
		if len([]rune(q)) > 120 {
			q = string([]rune(q)[:120])
		}
		r.Queries = append(r.Queries, q)
		var result struct {
			Items      []item `json:"items"`
			Incomplete bool   `json:"incomplete_results"`
		}
		path := "/search/repositories?q=" + url.QueryEscape(q+" in:name,description") + "&per_page=10"
		status, err := g.get(ctx, path, &result)
		if status == http.StatusNotFound {
			return r, ErrGitHubUnavailable
		}
		if err != nil {
			return r, err
		}
		r.Incomplete = r.Incomplete || result.Incomplete
		group := []item{}
		for _, repo := range result.Items {
			if repo.Fork || !validFullName(repo.FullName) {
				continue
			}
			group = append(group, repo)
		}
		groups = append(groups, group)
	}
	seen := map[string]bool{}
	candidates := []item{}
	for rank := 0; rank < 10; rank++ {
		for _, group := range groups {
			if rank < len(group) && !seen[group[rank].FullName] {
				seen[group[rank].FullName] = true
				candidates = append(candidates, group[rank])
			}
		}
	}
	checked := 0
	for _, repo := range candidates {
		if len(r.Repositories) >= 4 || checked >= 8 {
			break
		}
		checked++
		var readme struct {
			Content  string `json:"content"`
			Encoding string `json:"encoding"`
			HTMLURL  string `json:"html_url"`
		}
		status, err := g.get(ctx, "/repos/"+repo.FullName+"/readme", &readme)
		if status == http.StatusNotFound {
			continue
		}
		if err != nil {
			return r, err
		}
		if readme.Encoding != "base64" {
			continue
		}
		body, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(readme.Content, "\n", ""))
		if err != nil {
			continue
		}
		content := []rune(string(body))
		if len(content) < 100 {
			continue
		}
		if len(content) > 3000 {
			content = content[:3000]
		}
		if !strings.HasPrefix(readme.HTMLURL, "https://github.com/") || !strings.HasPrefix(repo.HTMLURL, "https://github.com/") {
			continue
		}
		var root []struct {
			Name string `json:"name"`
			Type string `json:"type"`
		}
		status, err = g.get(ctx, "/repos/"+repo.FullName+"/contents", &root)
		if err != nil {
			return r, err
		}
		if status == http.StatusNotFound {
			continue
		}
		entries := []string{}
		for _, entry := range root {
			if codeEntry(entry.Name, entry.Type) {
				entries = append(entries, entry.Name)
			}
		}
		if len(entries) == 0 {
			continue
		}
		evidence := RepoEvidence{FullName: repo.FullName, URL: repo.HTMLURL, Description: repo.Description, Language: repo.Language, Archived: repo.Archived, Readme: string(content), ReadmeURL: readme.HTMLURL, CheckedAt: time.Now().UTC(), AdditionalDocs: []RepoDocument{}, CodeEntries: entries}
		if match := markdownDocLink.FindStringSubmatch(evidence.Readme); len(match) == 2 && !strings.Contains(match[1], "..") {
			segments := strings.Split(match[1], "/")
			for i := range segments {
				segments[i] = url.PathEscape(segments[i])
			}
			var doc struct {
				Content  string `json:"content"`
				Encoding string `json:"encoding"`
				HTMLURL  string `json:"html_url"`
			}
			status, err := g.get(ctx, "/repos/"+repo.FullName+"/contents/"+strings.Join(segments, "/"), &doc)
			if err != nil {
				return r, err
			}
			if status != http.StatusNotFound && doc.Encoding == "base64" && strings.HasPrefix(doc.HTMLURL, "https://github.com/"+repo.FullName+"/") {
				if decoded, decodeErr := base64.StdEncoding.DecodeString(strings.ReplaceAll(doc.Content, "\n", "")); decodeErr == nil {
					runes := []rune(string(decoded))
					if len(runes) > 2500 {
						runes = runes[:2500]
					}
					evidence.AdditionalDocs = append(evidence.AdditionalDocs, RepoDocument{URL: doc.HTMLURL, Content: string(runes)})
				}
			}
		}
		r.Repositories = append(r.Repositories, evidence)
	}
	return r, nil
}
func cleanSearchQuery(raw string) string {
	stop := map[string]bool{"backend": true, "service": true, "design": true, "implementation": true, "architecture": true, "system": true, "project": true, "github": true, "code": true}
	words := strings.Fields(strings.TrimSpace(raw))
	kept := []string{}
	for _, word := range words {
		if !stop[strings.ToLower(strings.Trim(word, ",.;:"))] {
			kept = append(kept, word)
		}
	}
	if len(kept) == 0 {
		return strings.TrimSpace(raw)
	}
	if len(kept) > 4 {
		kept = kept[:4]
	}
	return strings.Join(kept, " ")
}
func codeEntry(name, kind string) bool {
	value := strings.ToLower(name)
	if kind == "dir" {
		switch value {
		case "src", "app", "cmd", "backend", "frontend", "server", "api", "internal", "packages":
			return true
		}
	}
	if kind == "file" {
		switch value {
		case "go.mod", "package.json", "requirements.txt", "pyproject.toml", "cargo.toml", "pom.xml", "build.gradle", "main.go", "manage.py", "composer.json":
			return true
		}
	}
	return false
}
func validFullName(name string) bool {
	parts := strings.Split(name, "/")
	if len(parts) != 2 {
		return false
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return false
		}
		for _, ch := range part {
			if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '-' || ch == '_' || ch == '.') {
				return false
			}
		}
	}
	return true
}
