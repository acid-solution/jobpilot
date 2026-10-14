package learningroutes

import (
	"github.com/google/uuid"
	"testing"
)

func TestReceiptRequiresExactSourceAndDistinctExecutionVersions(t *testing.T) {
	request := ImportRequest{RouteID: uuid.New(), Digest: "fixed-digest", Payload: Payload{SourceVersion: 2}}
	valid := Receipt{ImportID: uuid.NewString(), GoalID: uuid.NewString(), SourceRouteID: request.RouteID.String(), SourceVersion: 2, ContentDigest: request.Digest, Plans: []PlanRef{{Role: "project", PlanID: uuid.NewString(), PlanVersionID: uuid.NewString(), VersionNo: 1}, {Role: "knowledge", PlanID: uuid.NewString(), PlanVersionID: uuid.NewString(), VersionNo: 2}}}
	if !validReceipt(request, &valid) {
		t.Fatal("knowledge-only receipt rejected")
	}
	for _, change := range []func(*Receipt){
		func(r *Receipt) { r.ContentDigest = "other" },
		func(r *Receipt) { r.SourceVersion = 1 },
		func(r *Receipt) { r.GoalID = uuid.Nil.String() },
		func(r *Receipt) { r.Plans[1].Role = "project" },
		func(r *Receipt) { r.Plans[1].PlanID = r.Plans[0].PlanID },
		func(r *Receipt) { r.Plans[1].VersionNo = 0 },
	} {
		candidate := valid
		candidate.Plans = append([]PlanRef(nil), valid.Plans...)
		change(&candidate)
		if validReceipt(request, &candidate) {
			t.Fatal("unverifiable remote success accepted")
		}
	}
}

func TestIntegrationEndpointsNeverRedirectCredentialsToArbitraryHTTP(t *testing.T) {
	for _, endpoint := range []string{"http://example.com/mcp", "https://user:secret@example.com/mcp", "https://example.com/mcp?token=x", "file:///tmp/server"} {
		if safeServiceURL(endpoint) {
			t.Fatal("unsafe endpoint accepted", endpoint)
		}
	}
	for _, endpoint := range []string{"http://127.0.0.1:8081/mcp", "https://studyflow.example.com/mcp"} {
		if !safeServiceURL(endpoint) {
			t.Fatal("fixed endpoint rejected")
		}
	}
}

func TestHTTPIntegrationRequiresExplicitOptIn(t *testing.T) {
	endpoint := "http://192.0.2.1:18084/mcp"
	if safeServiceURL(endpoint) || safeServiceURL(endpoint, false) {
		t.Fatal("HTTP accepted without opt-in")
	}
	if !safeServiceURL(endpoint, true) || !safeServiceURL("http://auth:18082/auth/v1/token/exchange", true) {
		t.Fatal("explicit HTTP test endpoint rejected")
	}
	for _, raw := range []string{"http://user:secret@example.com/mcp", "http://example.com/mcp?key=x", "http://example.com/mcp#fragment", "ftp://example.com/mcp", "http://:80/mcp"} {
		if safeServiceURL(raw, true) {
			t.Fatal("opt-in bypassed URL structure checks", raw)
		}
	}
	client, err := NewMCPClient("http://auth:18082/auth/v1/token/exchange", "12345678901234567890123456789012", endpoint, true)
	if err != nil || !client.Enabled() {
		t.Fatal("HTTP test configuration rejected", err)
	}
}
