package github

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/piglor/loom/services/loom/internal/control"
)

var githubTestDSN string

func TestMain(m *testing.M) {
	githubTestDSN = os.Getenv("LOOM_GO_TEST_DATABASE_URL")
	container := ""
	if githubTestDSN == "" {
		output, err := exec.Command("docker", "run", "--detach", "--rm", "-e", "POSTGRES_PASSWORD=loom-test-only", "-e", "POSTGRES_DB=loom_test", "-p", "127.0.0.1::5432", "postgres:15.6").Output()
		if err != nil {
			fmt.Fprintln(os.Stderr, "GitHub integration tests require Docker:", err)
			os.Exit(1)
		}
		container = strings.TrimSpace(string(output))
		port, err := exec.Command("docker", "port", container, "5432/tcp").Output()
		if err != nil {
			_ = exec.Command("docker", "stop", container).Run()
			os.Exit(1)
		}
		githubTestDSN = "postgres://postgres:loom-test-only@" + strings.TrimSpace(string(port)) + "/loom_test?sslmode=disable"
	}
	code := m.Run()
	if container != "" {
		if err := exec.Command("docker", "stop", container).Run(); err != nil {
			code = 1
		}
	}
	os.Exit(code)
}

func githubStoreWithoutMigration(t *testing.T) *control.Store {
	t.Helper()
	ctx := context.Background()
	config, err := pgx.ParseConfig(githubTestDSN)
	if err != nil {
		t.Fatal(err)
	}
	admin := stdlib.OpenDB(*config)
	deadline := time.Now().Add(30 * time.Second)
	for admin.PingContext(ctx) != nil {
		if time.Now().After(deadline) {
			t.Fatal("PostgreSQL unavailable")
		}
		time.Sleep(100 * time.Millisecond)
	}
	schema := "github_test_" + strings.ReplaceAll(control.ID(), "-", "")
	if _, err = admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	config.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*config)
	t.Cleanup(func() {
		db.Close()
		_, _ = admin.ExecContext(ctx, "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
	})
	return &control.Store{DB: db, Organization: "github-test"}
}

func githubStore(t *testing.T) *control.Store {
	t.Helper()
	ctx := context.Background()
	store := githubStoreWithoutMigration(t)
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return store
}

type freshnessStub struct {
	bindable bool
	current  bool
	err      error
}

func (f *freshnessStub) Bindable(context.Context, Workflow) (bool, error) {
	return f.bindable, f.err
}
func (f *freshnessStub) Current(context.Context, Workflow) (bool, error) {
	return f.current, f.err
}

func request(t *testing.T, handler http.Handler, path, token string, body any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(encoded))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	for key, value := range headers {
		r.Header.Set(key, value)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func signedHeaders(body []byte, secret, delivery string) map[string]string {
	h := hmac.New(sha256.New, []byte(secret))
	_, _ = h.Write(body)
	return map[string]string{"X-Hub-Signature-256": "sha256=" + hex.EncodeToString(h.Sum(nil)), "X-GitHub-Delivery": delivery, "X-GitHub-Event": "workflow_run"}
}

func TestHandlerBindsAndRoutesExactlyOnce(t *testing.T) {
	store := githubStore(t)
	ctx := context.Background()
	condition := control.Condition{Source: "github", Type: "workflow.completed", Resource: "11/12/4/81/2", Version: strings.Repeat("a", 40)}
	goalID, err := store.Create(ctx, control.CreateGoal{Title: "GitHub", Objective: "Wake from CI", Condition: condition})
	if err != nil || store.Dispatch(ctx, goalID) != nil {
		t.Fatal(err)
	}
	admin := strings.Repeat("a", 40)
	secret := strings.Repeat("s", 40)
	freshness := &freshnessStub{bindable: true, current: true}
	handler := NewHandler(store, admin, secret, freshness)
	binding := BindingRequest{GoalID: goalID, Generation: 1, Installation: 7, Repository: 11, RepositoryRef: "piglor/loom", PullRequest: 12, HeadSHA: strings.Repeat("a", 40), RunID: 81, RunAttempt: 2, WorkflowID: 4}
	if response := request(t, handler, "/v1/github/bindings", admin, binding, nil); response.Code != http.StatusOK {
		t.Fatalf("bind=%d %s", response.Code, response.Body.String())
	}
	body := raw(t, payload())
	headers := signedHeaders(body, secret, "00000000-0000-4000-8000-000000000001")
	webhook := httptest.NewRequest(http.MethodPost, "/v1/github/webhook", bytes.NewReader(body))
	for key, value := range headers {
		webhook.Header.Set(key, value)
	}
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, webhook)
	if first.Code != http.StatusOK || !strings.Contains(first.Body.String(), `"disposition":"accepted"`) {
		t.Fatalf("first=%d %s", first.Code, first.Body.String())
	}
	duplicate := httptest.NewRecorder()
	webhook = httptest.NewRequest(http.MethodPost, "/v1/github/webhook", bytes.NewReader(body))
	for key, value := range headers {
		webhook.Header.Set(key, value)
	}
	handler.ServeHTTP(duplicate, webhook)
	if duplicate.Code != http.StatusOK || !strings.Contains(duplicate.Body.String(), `"disposition":"duplicate"`) {
		t.Fatalf("duplicate=%d %s", duplicate.Code, duplicate.Body.String())
	}
	var state string
	if err = store.DB.QueryRowContext(ctx, "SELECT state FROM goals WHERE id=$1", goalID).Scan(&state); err != nil || state != "READY" {
		t.Fatalf("state=%s err=%v", state, err)
	}
}

func TestMigratedLegacyBindingRoutesNewSignedWebhook(t *testing.T) {
	store := githubStoreWithoutMigration(t)
	ctx := context.Background()
	for _, path := range []string{
		"../../control/migrations/001-schema.sql",
		"../../control/migrations/002-workers.sql",
		"../../control/legacy/003-github.sql",
		"../../control/legacy/004-github-inbox.sql",
		"../../control/legacy/005-reconcile.sql",
		"../../control/migrations/006-provider-binding.sql",
	} {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = store.DB.ExecContext(ctx, string(body)); err != nil {
			t.Fatal(err)
		}
	}
	goalID := control.ID()
	oldCondition := `{"source":"github","type":"workflow.completed","resource":"11/12/81/2","version":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`
	if _, err := store.DB.ExecContext(ctx, `INSERT INTO goals(id,organization,title,objective,state,completion_criteria) VALUES($1,'github-test','legacy','resume after upgrade','WAITING',jsonb_build_object('event_matches',$2::jsonb))`, goalID, oldCondition); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.ExecContext(ctx, `INSERT INTO waits(id,goal_id,generation,condition,armed_at) VALUES($1,$2,1,$3::jsonb,clock_timestamp())`, control.ID(), goalID, oldCondition); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.ExecContext(ctx, `INSERT INTO github_bindings VALUES($1,'github-test',7,11,12,$2,81,2,4,NULL)`, goalID, strings.Repeat("a", 40)); err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	secret := strings.Repeat("s", 40)
	handler := NewHandler(store, strings.Repeat("a", 40), secret, &freshnessStub{bindable: true, current: true})
	body := raw(t, payload())
	webhook := httptest.NewRequest(http.MethodPost, "/v1/github/webhook", bytes.NewReader(body))
	for key, value := range signedHeaders(body, secret, "00000000-0000-4000-8000-000000000099") {
		webhook.Header.Set(key, value)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, webhook)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"disposition":"accepted"`) {
		t.Fatalf("webhook=%d %s", response.Code, response.Body.String())
	}
	var state string
	if err := store.DB.QueryRowContext(ctx, "SELECT state FROM goals WHERE id=$1", goalID).Scan(&state); err != nil || state != "READY" {
		t.Fatalf("migrated goal state=%s err=%v", state, err)
	}
}

func TestHandlerRetainsTransientEvidenceAndRejectsStaleBinding(t *testing.T) {
	store := githubStore(t)
	ctx := context.Background()
	condition := control.Condition{Source: "github", Type: "workflow.completed", Resource: "11/12/4/81/2", Version: strings.Repeat("a", 40)}
	goalID, err := store.Create(ctx, control.CreateGoal{Title: "GitHub", Objective: "Do not wake stale", Condition: condition})
	if err != nil || store.Dispatch(ctx, goalID) != nil {
		t.Fatal(err)
	}
	admin, secret := strings.Repeat("a", 40), strings.Repeat("s", 40)
	freshness := &freshnessStub{bindable: false, current: true, err: errors.New("temporary")}
	handler := NewHandler(store, admin, secret, freshness)
	body := raw(t, payload())
	headers := signedHeaders(body, secret, "00000000-0000-4000-8000-000000000002")
	webhook := httptest.NewRequest(http.MethodPost, "/v1/github/webhook", bytes.NewReader(body))
	for key, value := range headers {
		webhook.Header.Set(key, value)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, webhook)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("transient=%d %s", response.Code, response.Body.String())
	}
	var receipts int
	if err = store.DB.QueryRowContext(ctx, "SELECT count(*) FROM integration_deliveries").Scan(&receipts); err != nil || receipts != 1 {
		t.Fatalf("receipts=%d err=%v", receipts, err)
	}
	freshness.err = nil
	webhook = httptest.NewRequest(http.MethodPost, "/v1/github/webhook", bytes.NewReader(body))
	for key, value := range headers {
		webhook.Header.Set(key, value)
	}
	retried := httptest.NewRecorder()
	handler.ServeHTTP(retried, webhook)
	if retried.Code != http.StatusOK || !strings.Contains(retried.Body.String(), `"disposition":"ignored"`) {
		t.Fatalf("retried=%d %s", retried.Code, retried.Body.String())
	}
	binding := BindingRequest{GoalID: goalID, Generation: 1, Installation: 7, Repository: 11, RepositoryRef: "piglor/loom", PullRequest: 12, HeadSHA: strings.Repeat("a", 40), RunID: 81, RunAttempt: 2, WorkflowID: 4}
	if stale := request(t, handler, "/v1/github/bindings", admin, binding, nil); stale.Code != http.StatusConflict {
		t.Fatalf("stale bind=%d %s", stale.Code, stale.Body.String())
	}
	var state string
	if err = store.DB.QueryRowContext(ctx, "SELECT state FROM goals WHERE id=$1", goalID).Scan(&state); err != nil || state != "WAITING" {
		t.Fatalf("state=%s err=%v", state, err)
	}
}
