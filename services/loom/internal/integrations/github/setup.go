package github

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/piglor/loom/services/loom/internal/control"
	"github.com/piglor/loom/services/loom/internal/integrations/catalog"
	"github.com/piglor/loom/services/loom/internal/secrets"
)

const setupCookie = "loom_plugin_setup"

var accountPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?$`)
var appSlugPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,99}$`)

type SetupStore interface {
	CreateIntegrationSetup(context.Context, string, string, string, time.Time) (string, error)
	IntegrationSetupByState(context.Context, string) (control.IntegrationSetupRecord, error)
	SetIntegrationSetupStage(context.Context, string, string, *string, *string) error
	CreateIntegrationCredential(context.Context, control.IntegrationCredentialRecord) error
	IntegrationCredential(context.Context, string) (control.IntegrationCredentialRecord, error)
	SetIntegrationCredentialState(context.Context, string, string, *int) error
	UpsertIntegrationInstance(context.Context, control.IntegrationInstanceRecord) (string, error)
	ListIntegrationInstances(context.Context, string) ([]control.IntegrationInstanceRecord, error)
	DisableIntegrationInstance(context.Context, string) error
}

func (h *SetupHandler) Plugin(ctx context.Context) catalog.Plugin {
	status := h.secrets.Status(ctx)
	plugin := catalog.Plugin{
		ID: "github", Name: "GitHub", Category: "Source control",
		Description: "Connect repositories and wake Goals from trusted GitHub events.",
		State:       catalog.NeedsConfiguration, SetupTitle: "Connect GitHub",
		SetupSummary:  "Create a least-privilege GitHub App, choose repositories, and let Loom store its credentials securely.",
		EstimatedTime: "About 3 minutes", SecretBackend: string(status),
		Steps: []catalog.Step{
			{Title: "Choose your GitHub account", Description: "Connect an organization or personal account."},
			{Title: "Approve read-only access", Description: "GitHub creates the app and lets you select repositories."},
			{Title: "Return connected", Description: "Loom verifies the installation and starts accepting signed events."},
		},
		Endpoints: []catalog.Endpoint{{Label: "Webhook URL", Path: "/v1/github/webhook"}},
		Notice:    "GitHub can wake an authorized wait; connecting it does not grant merge, deployment, or unrelated execution authority.",
	}
	backendReady := status == secrets.StatusReady
	plugin.Checks = []catalog.Check{{ID: "secret_storage", Label: "OpenBao secret storage", Status: checkStatus(backendReady), Detail: readyDetail(backendReady, secretStatusDetail(status)), Required: true}}
	if !backendReady {
		return plugin
	}
	instances, err := h.store.ListIntegrationInstances(ctx, "github")
	if err != nil {
		plugin.State = catalog.NeedsAttention
		plugin.Checks = append(plugin.Checks, catalog.Check{ID: "connection_state", Label: "Connection records", Status: catalog.CheckMissing, Detail: "Temporarily unavailable", Required: true})
		return plugin
	}
	for _, instance := range instances {
		if instance.State == "active" {
			plugin.ConnectionCount++
		}
	}
	if plugin.ConnectionCount > 0 {
		plugin.State = catalog.Connected
		plugin.Checks = append(plugin.Checks, catalog.Check{ID: "github_installation", Label: "GitHub installation", Status: catalog.CheckReady, Detail: fmt.Sprintf("%d connected", plugin.ConnectionCount), Required: true})
	} else {
		plugin.State = catalog.ReadyToConnect
		plugin.Checks = append(plugin.Checks, catalog.Check{ID: "github_installation", Label: "GitHub installation", Status: catalog.CheckMissing, Detail: "Not connected yet", Required: true})
	}
	return plugin
}

func secretStatusDetail(status secrets.Status) string {
	switch status {
	case secrets.StatusSealed:
		return "OpenBao is sealed"
	case secrets.StatusUnavailable:
		return "OpenBao is unreachable"
	default:
		return "Configure LOOM_OPENBAO_ADDR and AppRole credentials"
	}
}

type SetupHandler struct {
	store         SetupStore
	secrets       secrets.Store
	operatorToken string
	organization  string
	publicURL     string
	githubAPI     string
	httpClient    *http.Client
}

type setupStartRequest struct {
	AccountType string `json:"account_type"`
	Account     string `json:"account"`
}

type setupStartResponse struct {
	SetupID   string    `json:"setup_id"`
	State     string    `json:"state"`
	ActionURL string    `json:"action_url"`
	Manifest  string    `json:"manifest"`
	ExpiresAt time.Time `json:"expires_at"`
}

type manualRequest struct {
	Label          string `json:"label"`
	AppID          string `json:"app_id"`
	ClientID       string `json:"client_id"`
	ClientSecret   string `json:"client_secret"`
	PrivateKey     string `json:"private_key"`
	WebhookSecret  string `json:"webhook_secret"`
	AppSlug        string `json:"app_slug"`
	InstallationID string `json:"installation_id"`
}

type githubCredentials struct {
	AppID         string `json:"app_id"`
	ClientID      string `json:"client_id"`
	ClientSecret  string `json:"client_secret"`
	PrivateKey    string `json:"private_key"`
	WebhookSecret string `json:"webhook_secret"`
	AppSlug       string `json:"app_slug"`
}

type manifestConversion struct {
	ID            int64  `json:"id"`
	ClientID      string `json:"client_id"`
	ClientSecret  string `json:"client_secret"`
	PEM           string `json:"pem"`
	WebhookSecret string `json:"webhook_secret"`
	Slug          string `json:"slug"`
}

type installation struct {
	ID      int64 `json:"id"`
	Account struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
	} `json:"account"`
	RepositorySelection string            `json:"repository_selection"`
	Permissions         map[string]string `json:"permissions"`
	SuspendedAt         *time.Time        `json:"suspended_at"`
}

func NewSetupHandler(store SetupStore, secretStore secrets.Store, operatorToken, organization, publicURL string) (*SetupHandler, error) {
	parsed, err := url.Parse(publicURL)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("LOOM_PUBLIC_URL must be an absolute HTTP or HTTPS URL")
	}
	parsed.Path = strings.TrimSuffix(parsed.Path, "/")
	return &SetupHandler{
		store: store, secrets: secretStore, operatorToken: operatorToken,
		organization: organization, publicURL: parsed.String(), githubAPI: "https://api.github.com",
		httpClient: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}, nil
}

func (h *SetupHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v1/integration-instances":
		h.auth(h.listInstances)(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/plugins/github/setup-sessions":
		h.auth(h.startSetup)(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/plugins/github/manual":
		h.auth(h.manualSetup)(w, r)
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1/integration-instances/") && strings.HasSuffix(r.URL.Path, "/disable"):
		h.auth(h.disableInstance)(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/plugins/github/manifest/callback":
		h.manifestCallback(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/plugins/github/install/callback":
		h.installCallback(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/plugins/github/oauth/callback":
		h.oauthCallback(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (h *SetupHandler) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+h.operatorToken)) != 1 {
			writeSetupJSON(w, http.StatusUnauthorized, map[string]string{"detail": "Invalid authentication"})
			return
		}
		next(w, r)
	}
}

func writeSetupJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func decodeSetupJSON(w http.ResponseWriter, r *http.Request, destination any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 128<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(destination) != nil {
		writeSetupJSON(w, http.StatusUnprocessableEntity, map[string]string{"detail": "Invalid setup request"})
		return false
	}
	return true
}

func randomSetupState() (string, string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", err
	}
	state := base64.RawURLEncoding.EncodeToString(raw)
	digest := sha256.Sum256([]byte(state))
	return state, hex.EncodeToString(digest[:]), nil
}

func stateDigest(state string) string {
	digest := sha256.Sum256([]byte(state))
	return hex.EncodeToString(digest[:])
}

func (h *SetupHandler) setSetupCookie(w http.ResponseWriter, state string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{Name: setupCookie, Value: state, Path: "/v1/plugins/github/", Expires: expires, MaxAge: int(time.Until(expires).Seconds()), HttpOnly: true, Secure: strings.HasPrefix(h.publicURL, "https://"), SameSite: http.SameSiteLaxMode})
}

func (h *SetupHandler) clearSetupCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: setupCookie, Value: "", Path: "/v1/plugins/github/", MaxAge: -1, HttpOnly: true, Secure: strings.HasPrefix(h.publicURL, "https://"), SameSite: http.SameSiteLaxMode})
}

func (h *SetupHandler) setupFromCallback(r *http.Request) (control.IntegrationSetupRecord, string, error) {
	state := r.URL.Query().Get("state")
	cookie, err := r.Cookie(setupCookie)
	if err != nil || len(state) < 32 || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(state)) != 1 {
		return control.IntegrationSetupRecord{}, "", errors.New("setup verification failed")
	}
	setup, err := h.store.IntegrationSetupByState(r.Context(), stateDigest(state))
	return setup, state, err
}

func (h *SetupHandler) startSetup(w http.ResponseWriter, r *http.Request) {
	if h.secrets.Status(r.Context()) != secrets.StatusReady {
		writeSetupJSON(w, http.StatusServiceUnavailable, map[string]string{"detail": "Secret storage is not ready"})
		return
	}
	var input setupStartRequest
	if !decodeSetupJSON(w, r, &input) {
		return
	}
	if input.AccountType != "organization" && input.AccountType != "personal" {
		writeSetupJSON(w, http.StatusUnprocessableEntity, map[string]string{"detail": "Choose a GitHub account type"})
		return
	}
	if input.AccountType == "organization" && !accountPattern.MatchString(input.Account) {
		writeSetupJSON(w, http.StatusUnprocessableEntity, map[string]string{"detail": "Enter a valid GitHub organization"})
		return
	}
	state, digest, err := randomSetupState()
	if err != nil {
		writeSetupJSON(w, http.StatusServiceUnavailable, map[string]string{"detail": "Cannot start setup"})
		return
	}
	expires := time.Now().Add(time.Hour)
	setupID, err := h.store.CreateIntegrationSetup(r.Context(), "github", "manifest", digest, expires)
	if err != nil {
		writeSetupJSON(w, http.StatusServiceUnavailable, map[string]string{"detail": "Cannot start setup"})
		return
	}
	manifestValue := map[string]any{
		"name": "Loom " + setupID[:8], "url": h.publicURL,
		"redirect_url":    h.publicURL + "/v1/plugins/github/manifest/callback",
		"callback_urls":   []string{h.publicURL + "/v1/plugins/github/oauth/callback"},
		"setup_url":       h.publicURL + "/v1/plugins/github/install/callback?state=" + url.QueryEscape(state),
		"setup_on_update": true, "public": false,
		"hook_attributes":     map[string]any{"url": h.publicURL + "/v1/github/webhook", "active": true},
		"default_events":      []string{"workflow_run", "installation", "installation_repositories"},
		"default_permissions": map[string]string{"actions": "read", "checks": "read", "metadata": "read", "pull_requests": "read"},
	}
	manifest, err := json.Marshal(manifestValue)
	if err != nil {
		writeSetupJSON(w, http.StatusServiceUnavailable, map[string]string{"detail": "Cannot start setup"})
		return
	}
	actionURL := "https://github.com/settings/apps/new"
	if input.AccountType == "organization" {
		actionURL = "https://github.com/organizations/" + url.PathEscape(input.Account) + "/settings/apps/new"
	}
	h.setSetupCookie(w, state, expires)
	writeSetupJSON(w, http.StatusCreated, setupStartResponse{SetupID: setupID, State: state, ActionURL: actionURL, Manifest: string(manifest), ExpiresAt: expires})
}

func validateCredentials(credentials githubCredentials) error {
	if _, err := strconv.ParseInt(credentials.AppID, 10, 64); err != nil || credentials.ClientID == "" || len(credentials.ClientSecret) < 20 || len(credentials.WebhookSecret) < 32 || !appSlugPattern.MatchString(credentials.AppSlug) {
		return errors.New("missing GitHub App credentials")
	}
	block, _ := pem.Decode([]byte(credentials.PrivateKey))
	if block == nil {
		return errors.New("invalid GitHub private key")
	}
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		if _, ok := key.(*rsa.PrivateKey); ok {
			return nil
		}
	}
	if _, err := x509.ParsePKCS1PrivateKey(block.Bytes); err != nil {
		return errors.New("invalid GitHub private key")
	}
	return nil
}

func parsePrivateKey(value string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(value))
	if block == nil {
		return nil, errors.New("invalid private key")
	}
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		if rsaKey, ok := key.(*rsa.PrivateKey); ok {
			return rsaKey, nil
		}
	}
	return x509.ParsePKCS1PrivateKey(block.Bytes)
}

func (h *SetupHandler) secretReference(credentialID string) string {
	digest := sha256.Sum256([]byte(h.organization))
	return "organizations/" + hex.EncodeToString(digest[:8]) + "/plugins/github/credentials/" + credentialID
}

func credentialsMap(value githubCredentials) map[string]string {
	return map[string]string{
		"app_id": value.AppID, "client_id": value.ClientID, "client_secret": value.ClientSecret,
		"private_key": value.PrivateKey, "webhook_secret": value.WebhookSecret, "app_slug": value.AppSlug,
	}
}

func credentialsFromMap(values map[string]string) githubCredentials {
	return githubCredentials{AppID: values["app_id"], ClientID: values["client_id"], ClientSecret: values["client_secret"], PrivateKey: values["private_key"], WebhookSecret: values["webhook_secret"], AppSlug: values["app_slug"]}
}

func (h *SetupHandler) saveCredential(ctx context.Context, label string, credentials githubCredentials) (control.IntegrationCredentialRecord, error) {
	var record control.IntegrationCredentialRecord
	if err := validateCredentials(credentials); err != nil {
		return record, err
	}
	record = control.IntegrationCredentialRecord{ID: control.ID(), PluginID: "github", Label: strings.TrimSpace(label), State: "setup_incomplete"}
	if record.Label == "" {
		record.Label = "GitHub App"
	}
	record.SecretReference = h.secretReference(record.ID)
	version, err := h.secrets.Put(ctx, record.SecretReference, credentialsMap(credentials))
	if err != nil {
		return control.IntegrationCredentialRecord{}, err
	}
	record.SecretVersion = version
	if err = h.store.CreateIntegrationCredential(ctx, record); err != nil {
		_ = h.secrets.Delete(ctx, record.SecretReference)
		return control.IntegrationCredentialRecord{}, err
	}
	return record, nil
}

func (h *SetupHandler) manualSetup(w http.ResponseWriter, r *http.Request) {
	if h.secrets.Status(r.Context()) != secrets.StatusReady {
		writeSetupJSON(w, http.StatusServiceUnavailable, map[string]string{"detail": "Secret storage is not ready"})
		return
	}
	var input manualRequest
	if !decodeSetupJSON(w, r, &input) {
		return
	}
	installationID, err := strconv.ParseInt(input.InstallationID, 10, 64)
	if err != nil || installationID < 1 {
		writeSetupJSON(w, http.StatusUnprocessableEntity, map[string]string{"detail": "Enter a valid installation ID"})
		return
	}
	credentials := githubCredentials{AppID: input.AppID, ClientID: input.ClientID, ClientSecret: input.ClientSecret, PrivateKey: input.PrivateKey, WebhookSecret: input.WebhookSecret, AppSlug: input.AppSlug}
	record, err := h.saveCredential(r.Context(), input.Label, credentials)
	if err != nil {
		writeSetupJSON(w, http.StatusUnprocessableEntity, map[string]string{"detail": "GitHub credentials could not be verified"})
		return
	}
	installed, err := h.getInstallation(r.Context(), credentials, installationID, "")
	if err != nil || installed.SuspendedAt != nil || !requiredPermissions(installed.Permissions) {
		_ = h.secrets.Delete(r.Context(), record.SecretReference)
		_ = h.store.SetIntegrationCredentialState(r.Context(), record.ID, "disabled", nil)
		writeSetupJSON(w, http.StatusUnprocessableEntity, map[string]string{"detail": "GitHub installation could not be verified"})
		return
	}
	instance, err := h.activateInstallation(r.Context(), record, installed)
	if err != nil {
		writeSetupJSON(w, http.StatusServiceUnavailable, map[string]string{"detail": "Connection could not be saved"})
		return
	}
	writeSetupJSON(w, http.StatusCreated, instance)
}

func (h *SetupHandler) manifestCallback(w http.ResponseWriter, r *http.Request) {
	setup, state, err := h.setupFromCallback(r)
	if err != nil || setup.PluginID != "github" || setup.Mode != "manifest" || setup.Stage != "created" {
		h.callbackPage(w, false, "This setup link is invalid or expired.", "")
		return
	}
	code := r.URL.Query().Get("code")
	var conversion manifestConversion
	if code == "" || h.githubJSON(r.Context(), http.MethodPost, h.githubAPI+"/app-manifests/"+url.PathEscape(code)+"/conversions", "", nil, &conversion) != nil {
		_ = h.store.SetIntegrationSetupStage(r.Context(), setup.ID, "failed", nil, nil)
		h.callbackPage(w, false, "GitHub did not complete app creation.", "")
		return
	}
	credentials := githubCredentials{AppID: strconv.FormatInt(conversion.ID, 10), ClientID: conversion.ClientID, ClientSecret: conversion.ClientSecret, PrivateKey: conversion.PEM, WebhookSecret: conversion.WebhookSecret, AppSlug: conversion.Slug}
	record, err := h.saveCredential(r.Context(), "GitHub App", credentials)
	if err != nil {
		_ = h.store.SetIntegrationSetupStage(r.Context(), setup.ID, "failed", nil, nil)
		h.callbackPage(w, false, "Loom could not store the GitHub credentials.", "")
		return
	}
	if err = h.store.SetIntegrationSetupStage(r.Context(), setup.ID, "app_created", &record.ID, nil); err != nil {
		h.callbackPage(w, false, "Loom could not finish app setup.", "")
		return
	}
	installURL := "https://github.com/apps/" + url.PathEscape(credentials.AppSlug) + "/installations/new"
	message := `GitHub App created. Continue in this window and choose the repositories Loom may observe.`
	h.callbackPage(w, true, message, installURL+"?state="+url.QueryEscape(state))
}

func (h *SetupHandler) installCallback(w http.ResponseWriter, r *http.Request) {
	setup, state, err := h.setupFromCallback(r)
	if err != nil || setup.Stage != "app_created" || setup.CredentialID == nil {
		h.callbackPage(w, false, "This installation link is invalid or expired.", "")
		return
	}
	installationID, err := strconv.ParseInt(r.URL.Query().Get("installation_id"), 10, 64)
	if err != nil || installationID < 1 {
		h.callbackPage(w, false, "GitHub did not provide a valid installation.", "")
		return
	}
	record, credentials, err := h.loadCredential(r.Context(), *setup.CredentialID)
	if err != nil {
		h.callbackPage(w, false, "Loom could not read the GitHub credentials.", "")
		return
	}
	installed, err := h.getInstallation(r.Context(), credentials, installationID, "")
	if err != nil || installed.SuspendedAt != nil || !requiredPermissions(installed.Permissions) {
		_ = h.store.SetIntegrationCredentialState(r.Context(), record.ID, "needs_attention", nil)
		h.callbackPage(w, false, "The GitHub installation is missing required access.", "")
		return
	}
	pending := strconv.FormatInt(installationID, 10)
	if err = h.store.SetIntegrationSetupStage(r.Context(), setup.ID, "installation_pending", nil, &pending); err != nil {
		h.callbackPage(w, false, "Loom could not continue installation.", "")
		return
	}
	oauth := "https://github.com/login/oauth/authorize?client_id=" + url.QueryEscape(credentials.ClientID) + "&redirect_uri=" + url.QueryEscape(h.publicURL+"/v1/plugins/github/oauth/callback") + "&state=" + url.QueryEscape(state)
	http.Redirect(w, r, oauth, http.StatusSeeOther)
}

func (h *SetupHandler) oauthCallback(w http.ResponseWriter, r *http.Request) {
	setup, _, err := h.setupFromCallback(r)
	if err != nil || setup.Stage != "installation_pending" || setup.CredentialID == nil || setup.PendingInstallationID == nil {
		h.callbackPage(w, false, "This authorization link is invalid or expired.", "")
		return
	}
	record, credentials, err := h.loadCredential(r.Context(), *setup.CredentialID)
	if err != nil {
		h.callbackPage(w, false, "Loom could not read the GitHub credentials.", "")
		return
	}
	accessToken, tokenErr := h.exchangeUserToken(r.Context(), credentials, r.URL.Query().Get("code"))
	if tokenErr != nil {
		h.callbackPage(w, false, "GitHub authorization could not be verified.", "")
		return
	}
	var accessible struct {
		Installations []installation `json:"installations"`
	}
	if h.githubJSON(r.Context(), http.MethodGet, h.githubAPI+"/user/installations", accessToken, nil, &accessible) != nil {
		h.callbackPage(w, false, "GitHub installation access could not be verified.", "")
		return
	}
	pendingID, _ := strconv.ParseInt(*setup.PendingInstallationID, 10, 64)
	var selected *installation
	for i := range accessible.Installations {
		if accessible.Installations[i].ID == pendingID {
			selected = &accessible.Installations[i]
			break
		}
	}
	if selected == nil || selected.SuspendedAt != nil || !requiredPermissions(selected.Permissions) {
		h.callbackPage(w, false, "Your GitHub account cannot authorize that installation.", "")
		return
	}
	if _, err = h.activateInstallation(r.Context(), record, *selected); err != nil {
		h.callbackPage(w, false, "Loom could not save the installation.", "")
		return
	}
	_ = h.store.SetIntegrationSetupStage(r.Context(), setup.ID, "complete", nil, nil)
	h.clearSetupCookie(w)
	h.callbackPage(w, true, "GitHub is connected. You can close this window.", "")
}

func (h *SetupHandler) exchangeUserToken(ctx context.Context, credentials githubCredentials, code string) (string, error) {
	if code == "" {
		return "", errors.New("missing OAuth code")
	}
	values := url.Values{"client_id": {credentials.ClientID}, "client_secret": {credentials.ClientSecret}, "code": {code}, "redirect_uri": {h.publicURL + "/v1/plugins/github/oauth/callback"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://github.com/login/oauth/access_token", strings.NewReader(values.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := h.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", errors.New("GitHub OAuth exchange failed")
	}
	var result struct {
		AccessToken string `json:"access_token"`
	}
	if decodeErr := json.NewDecoder(io.LimitReader(response.Body, maxBody+1)).Decode(&result); decodeErr != nil || result.AccessToken == "" {
		return "", errors.New("invalid GitHub OAuth response")
	}
	return result.AccessToken, nil
}

func (h *SetupHandler) loadCredential(ctx context.Context, id string) (control.IntegrationCredentialRecord, githubCredentials, error) {
	record, err := h.store.IntegrationCredential(ctx, id)
	if err != nil {
		return record, githubCredentials{}, err
	}
	values, _, err := h.secrets.Get(ctx, record.SecretReference)
	if err != nil {
		return record, githubCredentials{}, err
	}
	credentials := credentialsFromMap(values)
	return record, credentials, validateCredentials(credentials)
}

func (h *SetupHandler) activateInstallation(ctx context.Context, record control.IntegrationCredentialRecord, installed installation) (control.IntegrationInstanceRecord, error) {
	instance := control.IntegrationInstanceRecord{
		CredentialID: record.ID, PluginID: "github", ExternalInstanceID: strconv.FormatInt(installed.ID, 10),
		AccountID: strconv.FormatInt(installed.Account.ID, 10), AccountLabel: installed.Account.Login,
		RepositorySelection: installed.RepositorySelection, Metadata: map[string]any{"permissions": installed.Permissions}, State: "active",
	}
	id, err := h.store.UpsertIntegrationInstance(ctx, instance)
	if err != nil {
		return instance, err
	}
	instance.ID = id
	if err = h.store.SetIntegrationCredentialState(ctx, record.ID, "active", nil); err != nil {
		return instance, err
	}
	now := time.Now().UTC()
	instance.LastVerifiedAt = &now
	return instance, nil
}

func requiredPermissions(permissions map[string]string) bool {
	for _, key := range []string{"actions", "checks", "metadata", "pull_requests"} {
		if permissions[key] != "read" && permissions[key] != "write" {
			return false
		}
	}
	return true
}

func (h *SetupHandler) getInstallation(ctx context.Context, credentials githubCredentials, installationID int64, userToken string) (installation, error) {
	var result installation
	token := userToken
	if token == "" {
		var err error
		token, err = appJWT(credentials)
		if err != nil {
			return result, err
		}
	}
	err := h.githubJSON(ctx, http.MethodGet, h.githubAPI+"/app/installations/"+strconv.FormatInt(installationID, 10), token, nil, &result)
	return result, err
}

func appJWT(credentials githubCredentials) (string, error) {
	key, err := parsePrivateKey(credentials.PrivateKey)
	if err != nil {
		return "", err
	}
	now := time.Now()
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT"})
	payload, _ := json.Marshal(map[string]any{"iat": now.Add(-60 * time.Second).Unix(), "exp": now.Add(9 * time.Minute).Unix(), "iss": credentials.AppID})
	encoded := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(encoded))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return encoded + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func (h *SetupHandler) githubJSON(ctx context.Context, method, endpoint, token string, input, destination any) error {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "piglor-loom/0.3")
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := h.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("GitHub returned HTTP %d", response.StatusCode)
	}
	limited := io.LimitReader(response.Body, maxBody+1)
	data, err := io.ReadAll(limited)
	if err != nil || len(data) > maxBody {
		return errors.New("GitHub response exceeds limit")
	}
	if destination == nil || len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, destination)
}

func (h *SetupHandler) listInstances(w http.ResponseWriter, r *http.Request) {
	instances, err := h.store.ListIntegrationInstances(r.Context(), r.URL.Query().Get("plugin"))
	if err != nil {
		writeSetupJSON(w, http.StatusServiceUnavailable, map[string]string{"detail": "Connections are temporarily unavailable"})
		return
	}
	writeSetupJSON(w, http.StatusOK, instances)
}

func (h *SetupHandler) disableInstance(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/integration-instances/"), "/disable")
	if err := h.store.DisableIntegrationInstance(r.Context(), id); err != nil {
		status := http.StatusServiceUnavailable
		if control.IsNotFound(err) {
			status = http.StatusNotFound
		}
		writeSetupJSON(w, status, map[string]string{"detail": "Connection could not be disabled"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

var callbackTemplate = template.Must(template.New("callback").Parse(`<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>GitHub setup · Loom</title><style>body{font-family:system-ui;background:#f6f7fb;color:#17213a;display:grid;place-items:center;min-height:100vh;margin:0}.card{background:white;border:1px solid #dfe3ec;border-radius:18px;max-width:34rem;padding:2rem;box-shadow:0 20px 60px #17213a18}a{display:inline-block;margin-top:1rem;background:#5d52d9;color:white;text-decoration:none;padding:.8rem 1rem;border-radius:9px}</style></head><body><main class="card"><h1>{{if .Success}}Setup updated{{else}}Setup needs attention{{end}}</h1><p>{{.Message}}</p>{{if .Action}}<a href="{{.Action}}">Continue with GitHub</a>{{else if .Success}}<button onclick="window.close()">Close window</button>{{end}}</main><script>if(window.opener){window.opener.postMessage({type:"loom-plugin-setup",success:{{.Success}}},window.location.origin)}</script></body></html>`))

func (h *SetupHandler) callbackPage(w http.ResponseWriter, success bool, message, action string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; base-uri 'none'; frame-ancestors 'none'")
	w.WriteHeader(http.StatusOK)
	_ = callbackTemplate.Execute(w, struct {
		Success bool
		Message string
		Action  string
	}{success, message, action})
}
