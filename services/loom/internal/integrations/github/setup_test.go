package github

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/piglor/loom/services/loom/internal/control"
	"github.com/piglor/loom/services/loom/internal/secrets"
)

type fakeSetupStore struct {
	setup      control.IntegrationSetupRecord
	credential control.IntegrationCredentialRecord
	instances  []control.IntegrationInstanceRecord
}

func (f *fakeSetupStore) CreateIntegrationSetup(_ context.Context, plugin, mode, _ string, expires time.Time) (string, error) {
	f.setup = control.IntegrationSetupRecord{ID: "a2222222-2222-4222-8222-222222222222", PluginID: plugin, Mode: mode, Stage: "created", ExpiresAt: expires}
	return f.setup.ID, nil
}
func (f *fakeSetupStore) IntegrationSetupByState(context.Context, string) (control.IntegrationSetupRecord, error) {
	return f.setup, nil
}
func (f *fakeSetupStore) SetIntegrationSetupStage(_ context.Context, _ string, stage string, credentialID, installationID *string) error {
	f.setup.Stage, f.setup.CredentialID, f.setup.PendingInstallationID = stage, credentialID, installationID
	return nil
}
func (f *fakeSetupStore) CreateIntegrationCredential(_ context.Context, record control.IntegrationCredentialRecord) error {
	f.credential = record
	return nil
}
func (f *fakeSetupStore) IntegrationCredential(context.Context, string) (control.IntegrationCredentialRecord, error) {
	if f.credential.ID == "" {
		return control.IntegrationCredentialRecord{}, errors.New("not found")
	}
	return f.credential, nil
}
func (f *fakeSetupStore) SetIntegrationCredentialState(_ context.Context, _ string, state string, _ *int) error {
	f.credential.State = state
	return nil
}
func (f *fakeSetupStore) UpsertIntegrationInstance(_ context.Context, record control.IntegrationInstanceRecord) (string, error) {
	record.ID = "b2222222-2222-4222-8222-222222222222"
	f.instances = append(f.instances, record)
	return record.ID, nil
}
func (f *fakeSetupStore) ListIntegrationInstances(context.Context, string) ([]control.IntegrationInstanceRecord, error) {
	return f.instances, nil
}
func (f *fakeSetupStore) DisableIntegrationInstance(context.Context, string) error { return nil }

type fakeSecretStore struct {
	status secrets.Status
	values map[string]string
	ref    string
}

func (f *fakeSecretStore) Status(context.Context) secrets.Status { return f.status }
func (f *fakeSecretStore) Put(_ context.Context, ref string, values map[string]string) (int, error) {
	f.ref, f.values = ref, values
	return 1, nil
}
func (f *fakeSecretStore) Get(context.Context, string) (map[string]string, int, error) {
	return f.values, 1, nil
}
func (f *fakeSecretStore) Delete(context.Context, string) error { return nil }

func setupHandlerForTest(t *testing.T, records *fakeSetupStore, vault *fakeSecretStore) *SetupHandler {
	t.Helper()
	handler, err := NewSetupHandler(records, vault, "operator-test-token-with-at-least-32-characters", "test", "https://loom.example")
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func testPrivateKey(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
}

func TestGuidedSetupStartsWithWriteOnlyManifest(t *testing.T) {
	records := &fakeSetupStore{}
	vault := &fakeSecretStore{status: secrets.StatusReady}
	handler := setupHandlerForTest(t, records, vault)
	request := httptest.NewRequest(http.MethodPost, "/v1/plugins/github/setup-sessions", strings.NewReader(`{"account_type":"organization","account":"piglor"}`))
	request.Header.Set("Authorization", "Bearer operator-test-token-with-at-least-32-characters")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || len(response.Result().Cookies()) != 1 {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var result setupStartResponse
	if json.Unmarshal(response.Body.Bytes(), &result) != nil || !strings.Contains(result.ActionURL, "/organizations/piglor/") || !strings.Contains(result.Manifest, "/v1/github/webhook") {
		t.Fatalf("unexpected setup response: %#v", result)
	}
	if strings.Contains(response.Body.String(), "private_key") || records.setup.Stage != "created" {
		t.Fatal("setup leaked credentials or advanced state")
	}
}

func TestManifestCallbackStoresCredentialAndRejectsReplay(t *testing.T) {
	github := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/app-manifests/one-use-code/conversions" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(manifestConversion{ID: 123, ClientID: "Iv1.client", ClientSecret: strings.Repeat("c", 24), PEM: testPrivateKey(t), WebhookSecret: strings.Repeat("w", 32), Slug: "loom-test"})
	}))
	defer github.Close()
	records := &fakeSetupStore{}
	vault := &fakeSecretStore{status: secrets.StatusReady}
	handler := setupHandlerForTest(t, records, vault)
	handler.githubAPI = github.URL

	start := httptest.NewRequest(http.MethodPost, "/v1/plugins/github/setup-sessions", strings.NewReader(`{"account_type":"personal","account":""}`))
	start.Header.Set("Authorization", "Bearer operator-test-token-with-at-least-32-characters")
	started := httptest.NewRecorder()
	handler.ServeHTTP(started, start)
	var setup setupStartResponse
	if err := json.Unmarshal(started.Body.Bytes(), &setup); err != nil {
		t.Fatal(err)
	}
	callback := httptest.NewRequest(http.MethodGet, "/v1/plugins/github/manifest/callback?code=one-use-code&state="+url.QueryEscape(setup.State), nil)
	callback.AddCookie(started.Result().Cookies()[0])
	completed := httptest.NewRecorder()
	handler.ServeHTTP(completed, callback)
	if records.setup.Stage != "app_created" || records.credential.SecretReference != vault.ref || vault.values["private_key"] == "" {
		t.Fatalf("manifest was not persisted through the secret boundary: stage=%q credential=%#v", records.setup.Stage, records.credential)
	}
	if !strings.Contains(completed.Body.String(), "Continue with GitHub") || strings.Contains(completed.Body.String(), vault.values["client_secret"]) {
		t.Fatal("callback did not offer the safe next step or leaked a secret")
	}
	replayed := httptest.NewRecorder()
	handler.ServeHTTP(replayed, callback)
	if !strings.Contains(replayed.Body.String(), "invalid or expired") {
		t.Fatal("manifest callback replay was accepted")
	}
}

func TestPluginReportsOpenBaoAndConnections(t *testing.T) {
	records := &fakeSetupStore{instances: []control.IntegrationInstanceRecord{{State: "active"}}}
	vault := &fakeSecretStore{status: secrets.StatusReady}
	plugin := setupHandlerForTest(t, records, vault).Plugin(context.Background())
	if plugin.State != "connected" || plugin.ConnectionCount != 1 || plugin.SecretBackend != "ready" {
		t.Fatalf("unexpected plugin: %#v", plugin)
	}
	vault.status = secrets.StatusSealed
	plugin = setupHandlerForTest(t, records, vault).Plugin(context.Background())
	if plugin.State != "needs_configuration" || plugin.Checks[0].Detail != "OpenBao is sealed" {
		t.Fatalf("unexpected sealed plugin: %#v", plugin)
	}
}

func TestManualSetupStoresSecretsOutsideMetadata(t *testing.T) {
	github := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/app/installations/42" || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ey") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": 42, "account": map[string]any{"id": 7, "login": "piglor"}, "repository_selection": "selected",
			"permissions": map[string]string{"actions": "read", "checks": "read", "metadata": "read", "pull_requests": "read"},
		})
	}))
	defer github.Close()
	records := &fakeSetupStore{}
	vault := &fakeSecretStore{status: secrets.StatusReady}
	handler := setupHandlerForTest(t, records, vault)
	handler.githubAPI = github.URL
	input := manualRequest{Label: "Team GitHub", AppID: "123", ClientID: "Iv1.client", ClientSecret: strings.Repeat("c", 24), PrivateKey: testPrivateKey(t), WebhookSecret: strings.Repeat("w", 32), AppSlug: "loom-test", InstallationID: "42"}
	body, _ := json.Marshal(input)
	request := httptest.NewRequest(http.MethodPost, "/v1/plugins/github/manual", strings.NewReader(string(body)))
	request.Header.Set("Authorization", "Bearer operator-test-token-with-at-least-32-characters")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if vault.values["private_key"] == "" || records.credential.SecretReference != vault.ref || strings.Contains(records.credential.SecretReference, "private") {
		t.Fatal("credential boundary was not preserved")
	}
	if strings.Contains(response.Body.String(), input.ClientSecret) || strings.Contains(response.Body.String(), "private_key") {
		t.Fatal("secret leaked in response")
	}
}
