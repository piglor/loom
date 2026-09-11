package secrets

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	configuredWithoutCredentials := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer configuredWithoutCredentials.Close()
	configuredStore, err := NewOpenBao(OpenBaoConfig{Address: configuredWithoutCredentials.URL})
	if err != nil {
		t.Fatalf("expected missing AppRole credentials to remain actionable: %v", err)
	}
	if status := configuredStore.Status(context.Background()); status != StatusNeedsCredentials {
		t.Fatalf("expected missing AppRole credentials to remain actionable: status=%s", status)
	}
	if _, err = configuredStore.Put(context.Background(), "organizations/test/plugins/github/credentials/id", map[string]string{"key": "value"}); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("expected Put without credentials to be rejected as unconfigured, got %v", err)
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

func TestOpenBaoReportsInitializationAndAuthenticationSeparately(t *testing.T) {
	initializing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotImplemented)
	}))
	defer initializing.Close()
	store, err := NewOpenBao(OpenBaoConfig{Address: initializing.URL})
	if err != nil || store.Status(context.Background()) != StatusInitializing {
		t.Fatalf("expected initializing status: status=%s err=%v", store.Status(context.Background()), err)
	}
	authentication := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/sys/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	defer authentication.Close()
	store, err = NewOpenBao(OpenBaoConfig{Address: authentication.URL, RoleID: "role", SecretID: "secret"})
	if err != nil || store.Status(context.Background()) != StatusAuthentication {
		t.Fatalf("expected authentication status: status=%s err=%v", store.Status(context.Background()), err)
	}
}

func TestOpenBaoReloadsCredentialFiles(t *testing.T) {
	roleFile := filepath.Join(t.TempDir(), "role_id")
	secretFile := filepath.Join(filepath.Dir(roleFile), "secret_id")
	if err := os.WriteFile(roleFile, []byte("role-1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/sys/health":
			w.WriteHeader(http.StatusOK)
		case "/v1/auth/approle/login":
			var input map[string]string
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Fatal(err)
			}
			if input["role_id"] != "role-1" || input["secret_id"] != "secret-1" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"auth": map[string]any{"client_token": "file-token", "lease_duration": 300}})
		default:
			if r.Header.Get("X-Vault-Token") != "file-token" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"version": 1}})
		}
	}))
	defer server.Close()
	store, err := NewOpenBao(OpenBaoConfig{Address: server.URL, RoleIDFile: roleFile, SecretIDFile: secretFile})
	if err != nil {
		t.Fatal(err)
	}
	if store.Status(context.Background()) != StatusNeedsCredentials {
		t.Fatal("expected missing file credential to remain actionable")
	}
	if err := os.WriteFile(secretFile, []byte("secret-1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if store.Status(context.Background()) != StatusReady {
		t.Fatal("expected credential files to be reloaded")
	}
	if _, err := store.Put(context.Background(), "organizations/test/plugins/github/credentials/id", map[string]string{"key": "value"}); err != nil {
		t.Fatalf("expected file credentials to authenticate: %v", err)
	}
}
