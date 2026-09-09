package github

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func payload() map[string]any {
	return map[string]any{
		"action": "completed", "installation": map[string]any{"id": 7}, "repository": map[string]any{"id": 11, "full_name": "piglor/loom"},
		"workflow_run": map[string]any{
			"id": 81, "run_attempt": 2, "workflow_id": 4, "head_sha": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "status": "completed", "conclusion": "failure", "event": "pull_request", "head_repository": map[string]any{"id": 11},
			"pull_requests": []any{map[string]any{"number": 12, "head": map[string]any{"sha": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "repo": map[string]any{"id": 11}}, "base": map[string]any{"repo": map[string]any{"id": 11}}}},
		},
	}
}

func raw(t *testing.T, value any) []byte {
	t.Helper()
	result, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestOfficialSignatureVector(t *testing.T) {
	if !Verify([]byte("Hello, World!"), "sha256=757107ea0eb2509fc211221cce984b8a37570b6d7586c22c46f4379c8b043e17", "It's a Secret to Everybody") {
		t.Fatal("official GitHub signature vector failed")
	}
	secret := "test-only-secret-with-at-least-thirty-two-characters"
	body := []byte("{}")
	h := hmac.New(sha256.New, []byte(secret))
	_, _ = h.Write(body)
	signature := "sha256=" + hex.EncodeToString(h.Sum(nil))
	if Verify(append(body, ' '), signature, secret) {
		t.Fatal("tampered body accepted")
	}
}

func TestNormalizeAcceptsOnlyExactInternalPullRequestIdentity(t *testing.T) {
	w, trusted, err := Normalize("workflow_run", raw(t, payload()))
	if err != nil || !trusted || w.Repository != 11 || w.PullRequest != 12 || w.RunID != 81 || w.RunAttempt != 2 {
		t.Fatalf("workflow=%+v trusted=%v err=%v", w, trusted, err)
	}
	if w.condition().Resource != "11/12/4/81/2" {
		t.Fatalf("workflow producer missing from correlation: %s", w.condition().Resource)
	}
	tests := []struct {
		name   string
		change func(map[string]any)
	}{
		{"wrong repository", func(p map[string]any) { p["repository"].(map[string]any)["id"] = 99 }},
		{"wrong PR", func(p map[string]any) {
			p["workflow_run"].(map[string]any)["pull_requests"].([]any)[0].(map[string]any)["number"] = 0
		}},
		{"wrong SHA", func(p map[string]any) { p["workflow_run"].(map[string]any)["head_sha"] = "b" }},
		{"superseded run", func(p map[string]any) { p["workflow_run"].(map[string]any)["run_attempt"] = 0 }},
		{"fork head", func(p map[string]any) {
			p["workflow_run"].(map[string]any)["pull_requests"].([]any)[0].(map[string]any)["head"].(map[string]any)["repo"].(map[string]any)["id"] = 99
		}},
		{"fork run", func(p map[string]any) {
			p["workflow_run"].(map[string]any)["head_repository"].(map[string]any)["id"] = 99
		}},
		{"untrusted trigger", func(p map[string]any) { p["workflow_run"].(map[string]any)["event"] = "pull_request_target" }},
		{"ambiguous PR", func(p map[string]any) { p["workflow_run"].(map[string]any)["pull_requests"] = []any{} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := payload()
			test.change(candidate)
			_, trusted, err := Normalize("workflow_run", raw(t, candidate))
			if err != nil || trusted {
				t.Fatalf("trusted=%v err=%v", trusted, err)
			}
		})
	}
}

func TestInstanceSeparatesGitHubAppAndRepositoryHookTrust(t *testing.T) {
	if githubInstance(7, 11) != "installation:7" || githubInstance(0, 11) != "repository:11" {
		t.Fatal("GitHub instance identity is ambiguous")
	}
}

func TestCurrentRevalidatesPullRequestAndWorkflow(t *testing.T) {
	expected, trusted, err := Normalize("workflow_run", raw(t, payload()))
	if err != nil || !trusted {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/repos/piglor/loom/pulls/12":
			_, _ = w.Write([]byte(`{"number":12,"head":{"sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","repo":{"id":11}},"base":{"repo":{"id":11}}}`))
		case "/repos/piglor/loom/actions/runs/81":
			_, _ = w.Write([]byte(`{"id":81,"run_attempt":2,"workflow_id":4,"head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","status":"completed","event":"pull_request","repository":{"id":11}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	api := NewAPI("")
	api.base = server.URL
	ok, err := api.Current(context.Background(), expected)
	if err != nil || !ok {
		t.Fatalf("current=%v err=%v", ok, err)
	}
	expected.HeadSHA = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	ok, err = api.Current(context.Background(), expected)
	if err != nil || ok {
		t.Fatalf("stale=%v err=%v", ok, err)
	}
}
