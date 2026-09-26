package projectrecs

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

type fakeGenerator struct {
	outputs []any
	calls   int
}

func TestGitHubResearchLive(t *testing.T) {
	if os.Getenv("JOBPILOT_REAL_MODEL_TEST") != "1" {
		t.Skip("live acceptance disabled")
	}
	d := sampleDraft()
	d.SearchQueries = []string{"url shortener", "link shortener"}
	r, err := NewGitHubClient("https://api.github.com", os.Getenv("GITHUB_TOKEN"), nil).Research(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, repo := range r.Repositories {
		names = append(names, repo.FullName)
	}
	t.Logf("GitHub search checked %d queries and found documented code repositories: %v", len(r.Queries), names)
}

func (f *fakeGenerator) GenerateJSON(_ context.Context, _, _, _, _ string, out any) error {
	if f.calls >= len(f.outputs) {
		return errors.New("unexpected model call")
	}
	raw, _ := json.Marshal(f.outputs[f.calls])
	f.calls++
	return json.Unmarshal(raw, out)
}
func sampleDraft() Draft {
	return Draft{ID: "p1", Title: "受控 Agent 任务平台", Summary: "可追踪的任务执行", Audience: "研发团队", Problem: "长任务失败难恢复", Shape: "多用户 Web 服务", Scope: []string{"任务状态机"}, FitReasons: []string{"贴合后端岗位"}, Abilities: []string{"Go"}, Duration: "8 周", KnownFacts: []string{"岗位要求 Go"}, Assumptions: []string{"团队需要人工确认"}, SearchQueries: []string{"agent task workflow"}}
}
func TestGenerateDraftsKeepsAtMostThreeValidDistinctCandidates(t *testing.T) {
	d := sampleDraft()
	d.KnownFacts = []string{"不存在的画像事实"}
	d2 := d
	d2.ID = "p2"
	d2.Title = "工单协作"
	d3 := d
	d3.ID = "p3"
	d3.Title = "任务审计"
	d4 := d
	d4.ID = "p4"
	d4.Title = "项目四"
	f := &fakeGenerator{outputs: []any{draftOutput{Drafts: []Draft{d, d, d2, d3, d4}}}}
	got, _, err := GenerateDrafts(context.Background(), f, "key", "model", Input{Goal: "后端开发", IncludedJDCount: 10}, "")
	if err != nil || len(got) != 3 || got[2].ID != "p3" {
		t.Fatalf("drafts=%+v err=%v", got, err)
	}
	if len(got[0].KnownFacts) != 2 || strings.Contains(strings.Join(got[0].KnownFacts, " "), "不存在") {
		t.Fatalf("model-supplied profile fact was trusted: %+v", got[0].KnownFacts)
	}
}
func TestEvaluateRequiresLiteralRepositoryEvidence(t *testing.T) {
	d := sampleDraft()
	repo := RepoEvidence{FullName: "team/tool", URL: "https://github.com/team/tool", ReadmeURL: "https://github.com/team/tool/blob/main/README.md", Readme: "This project supports task retries and audit logs."}
	r := Research{DraftID: d.ID, Repositories: []RepoEvidence{repo}}
	valid := assessment{DraftID: d.ID, References: []evaluatedReference{{FullName: repo.FullName, Facts: []Fact{{Text: "支持重试", Quote: "supports task retries", SourceURL: repo.ReadmeURL}}}}}
	f := &fakeGenerator{outputs: []any{comparisonOutput{Items: []assessment{valid}}}}
	if _, err := Evaluate(context.Background(), f, "key", "model", []Draft{d}, []Research{r}, false); err != nil {
		t.Fatal(err)
	}
	valid.References[0].Facts[0].Quote = "does not support retries"
	f = &fakeGenerator{outputs: []any{comparisonOutput{Items: []assessment{valid}}}}
	if _, err := Evaluate(context.Background(), f, "key", "model", []Draft{d}, []Research{r}, false); !errors.Is(err, ErrModelFormat) {
		t.Fatalf("expected invalid evidence, got %v", err)
	}
}
func TestEvaluateAcceptsLiteralRepositoryDescriptionEvidence(t *testing.T) {
	d := sampleDraft()
	repo := RepoEvidence{FullName: "team/tool", URL: "https://github.com/team/tool", Description: "A task workflow with retries and audit logs."}
	r := Research{DraftID: d.ID, Repositories: []RepoEvidence{repo}}
	a := assessment{DraftID: d.ID, References: []evaluatedReference{{FullName: repo.FullName, Facts: []Fact{{Text: "支持任务重试", Quote: "workflow with retries", SourceURL: repo.URL}}}}}
	f := &fakeGenerator{outputs: []any{comparisonOutput{Items: []assessment{a}}}}
	if _, err := Evaluate(context.Background(), f, "key", "model", []Draft{d}, []Research{r}, false); err != nil {
		t.Fatal(err)
	}
}
func TestNearDuplicateMayReviseOnceThenGetsDropped(t *testing.T) {
	d := sampleDraft()
	revised := d
	revised.Title = "更聚焦的方向"
	f := &fakeGenerator{outputs: []any{comparisonOutput{Items: []assessment{{DraftID: d.ID, TooSimilar: true, Revised: &revised}}}, comparisonOutput{Items: []assessment{{DraftID: d.ID, TooSimilar: true}}}}}
	research := Research{DraftID: d.ID, Repositories: []RepoEvidence{{FullName: "team/same"}}}
	first, err := Evaluate(context.Background(), f, "key", "model", []Draft{d}, []Research{research}, false)
	if err != nil || first[0].Revised == nil {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	last, err := Evaluate(context.Background(), f, "key", "model", []Draft{revised}, []Research{research}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(BuildProjects([]Draft{revised}, []Research{{DraftID: d.ID}}, last)) != 0 {
		t.Fatal("same project should be dropped")
	}
}
func TestGenericPracticeProjectIsNotRecommended(t *testing.T) {
	d := sampleDraft()
	a := assessment{DraftID: d.ID, Unsuitable: true, UnsuitableReason: "仅有通用任务列表，没有具体使用场景"}
	f := &fakeGenerator{outputs: []any{comparisonOutput{Items: []assessment{a}}}}
	checks, err := Evaluate(context.Background(), f, "key", "model", []Draft{d}, []Research{{DraftID: d.ID}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(BuildProjects([]Draft{d}, []Research{{DraftID: d.ID}}, checks)) != 0 {
		t.Fatal("generic practice project should be dropped")
	}
}
func TestGitHubResearchDistinguishesNoMatchFromFailure(t *testing.T) {
	mux := http.NewServeMux()
	fail := false
	mux.HandleFunc("/search/repositories", func(w http.ResponseWriter, r *http.Request) {
		if fail {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"items": []any{map[string]any{"full_name": "team/tool", "html_url": "https://github.com/team/tool", "description": "task workflow", "language": "Go"}}})
	})
	mux.HandleFunc("/repos/team/tool/readme", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte(strings.Repeat("Task workflow with retries and audit logs. ", 5) + "[Design](docs/architecture.md)")), "html_url": "https://github.com/team/tool/blob/main/README.md"})
	})
	mux.HandleFunc("/repos/team/tool/contents", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode([]any{map[string]any{"name": "go.mod", "type": "file"}, map[string]any{"name": "README.md", "type": "file"}})
	})
	mux.HandleFunc("/repos/team/tool/contents/docs/architecture.md", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte("Tasks have a durable audit trail.")), "html_url": "https://github.com/team/tool/blob/main/docs/architecture.md"})
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	client := NewGitHubClient(server.URL, "", server.Client())
	client.interval = time.Nanosecond
	result, err := client.Research(context.Background(), sampleDraft())
	if err != nil || len(result.Repositories) != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if !strings.Contains(result.Repositories[0].Readme, "audit logs") {
		t.Fatal("README not read")
	}
	if len(result.Repositories[0].AdditionalDocs) != 1 {
		t.Fatal("linked architecture document not read")
	}
	fail = true
	bad := NewGitHubClient(server.URL, "", server.Client())
	bad.interval = time.Nanosecond
	_, err = bad.Research(context.Background(), sampleDraft())
	if !errors.Is(err, ErrGitHubUnavailable) {
		t.Fatalf("expected outage, got %v", err)
	}
}
