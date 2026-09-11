package secrets

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenBaoLifecycleWithAppRole(t *testing.T) {
	stored := map[string]string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/sys/health":
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/v1/auth/approle/login":
			_ = json.NewEncoder(w).Encode(map[string]any{"auth": map[string]any{"client_token": "scoped-token", "lease_duration": 300}})
		case r.Header.Get("X-Vault-Token") != "scoped-token":
			w.WriteHeader(http.StatusForbidden)
		case r.URL.Path == "/v1/loom/data/organizations/test/plugins/github/credentials/id" && r.Method == http.MethodPost:
			var input struct {
				Data map[string]string `json:"data"`
			}
			_ = json.NewDecoder(r.Body).Decode(&input)
			stored = input.Data
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"version": 1}})
		case r.URL.Path == "/v1/loom/data/organizations/test/plugins/github/credentials/id" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"data": stored, "metadata": map[string]any{"version": 1}}})
		case r.URL.Path == "/v1/loom/data/organizations/test/plugins/github/credentials/id" && r.Method == http.MethodDelete:
			stored = map[string]string{}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	store, err := NewOpenBao(OpenBaoConfig{Address: server.URL, RoleID: "role", SecretID: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if store.Status(context.Background()) != StatusReady {
		t.Fatal("expected ready")
	}
	reference := "organizations/test/plugins/github/credentials/id"
	version, err := store.Put(context.Background(), reference, map[string]string{"private_key": "private"})
	if err != nil || version != 1 {
		t.Fatalf("put version=%d err=%v", version, err)
	}
	values, version, err := store.Get(context.Background(), reference)
	if err != nil || version != 1 || values["private_key"] != "private" {
		t.Fatalf("get values=%v version=%d err=%v", values, version, err)
	}
	if err = store.Delete(context.Background(), reference); err != nil {
		t.Fatal(err)
	}
}

func TestOpenBaoConfigurationAndFailures(t *testing.T) {
	unconfigured, err := NewOpenBao(OpenBaoConfig{})
	if err != nil || unconfigured.Status(context.Background()) != StatusUnconfigured {
		t.Fatalf("unexpected unconfigured result: %v", err)
	}
	if _, err = NewOpenBao(OpenBaoConfig{Address: "https://user:pass@example.com", Token: "token"}); err == nil {
		t.Fatal("accepted embedded credentials")
	}
	sealed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	defer sealed.Close()
	store, err := NewOpenBao(OpenBaoConfig{Address: sealed.URL, Token: "scoped"})
	if err != nil || store.Status(context.Background()) != StatusSealed {
		t.Fatalf("expected sealed: %v", err)
	}
	for _, reference := range []string{"", "../secret", "a//b", strings.Repeat("a", 250)} {
		if _, err = store.Put(context.Background(), reference, map[string]string{"key": "value"}); err == nil {
			t.Fatalf("accepted reference %q", reference)
		}
	}
}
