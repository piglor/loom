package control

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func jsonBody(value any) *bytes.Buffer {
	var body bytes.Buffer
	_ = json.NewEncoder(&body).Encode(value)
	return &body
}

func TestPasswordHashRoundTrip(t *testing.T) {
	hash, err := hashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !verifyPassword(hash, "correct horse battery staple") {
		t.Fatal("valid password was rejected")
	}
	if verifyPassword(hash, "wrong password") {
		t.Fatal("invalid password was accepted")
	}
	if _, err := hashPassword("short"); err == nil {
		t.Fatal("short password was accepted")
	}
}

func TestAuthenticatorBootstrapUsesConveniencePassword(t *testing.T) {
	store := testStore(t, true)
	auth, err := NewAuthenticator(store, AuthConfig{
		LegacyToken:  adminTestToken,
		Organization: store.Organization,
		AdminEmail:   "admin@example.com",
		PublicURL:    "http://127.0.0.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.Bootstrap(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	admin, err := store.SignInPassword(context.Background(), "admin@example.com", DefaultAdminPassword)
	if err != nil || admin.Role != "admin" {
		t.Fatalf("default admin sign in: user=%+v err=%v", admin, err)
	}
}

func TestAuthenticatorBootstrapRequiresPasswordOutsideLocalDevelopment(t *testing.T) {
	store := testStore(t, true)
	auth, err := NewAuthenticator(store, AuthConfig{
		Organization: store.Organization,
		AdminEmail:   "admin@example.com",
		PublicURL:    "https://loom.example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.Bootstrap(context.Background(), ""); err == nil {
		t.Fatal("public bootstrap accepted an implicit password")
	}
}

func TestAuthenticatorRejectsWeakLegacyToken(t *testing.T) {
	store := testStore(t, true)
	if _, err := NewAuthenticator(store, AuthConfig{
		LegacyToken:  "too-short",
		Organization: store.Organization,
		AdminEmail:   "admin@example.com",
		PublicURL:    "https://loom.example.com",
	}); err == nil {
		t.Fatal("weak legacy token was accepted")
	}
}

func TestAuthenticatorBootstrapUsesLegacyTokenOutsideLocalDevelopment(t *testing.T) {
	store := testStore(t, true)
	auth, err := NewAuthenticator(store, AuthConfig{
		LegacyToken:  adminTestToken,
		Organization: store.Organization,
		AdminEmail:   "admin@example.com",
		PublicURL:    "https://loom.example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.Bootstrap(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SignInPassword(context.Background(), "admin@example.com", adminTestToken); err != nil {
		t.Fatalf("legacy token fallback sign in: %v", err)
	}
}

func TestAuthenticatorBootstrapPreservesExistingPassword(t *testing.T) {
	store := testStore(t, true)
	ctx := context.Background()
	if err := store.BootstrapAdmin(ctx, "admin@example.com", "existing-admin-password"); err != nil {
		t.Fatal(err)
	}
	auth, err := NewAuthenticator(store, AuthConfig{
		Organization: store.Organization,
		AdminEmail:   "admin@example.com",
		PublicURL:    "http://127.0.0.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.Bootstrap(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SignInPassword(ctx, "admin@example.com", "existing-admin-password"); err != nil {
		t.Fatalf("existing password was replaced: %v", err)
	}
	if _, err := store.SignInPassword(ctx, "admin@example.com", DefaultAdminPassword); err == nil {
		t.Fatal("convenience password replaced the existing password")
	}
}

func TestAuthStoreLifecycle(t *testing.T) {
	store := testStore(t, true)
	ctx := context.Background()
	if _, err := store.SignInExternal(ctx, ExternalIdentity{Provider: "test", Subject: "unverified", Email: "unverified@example.com"}); err == nil {
		t.Fatal("unverified external identity was accepted")
	}
	if err := store.BootstrapAdmin(ctx, "Admin@Example.com", "admin-password-that-is-long"); err != nil {
		t.Fatal(err)
	}
	if err := store.BootstrapAdmin(ctx, "Admin@Example.com", "another-password-that-is-long"); err != nil {
		t.Fatal(err)
	}
	admin, err := store.SignInPassword(ctx, "admin@example.com", "admin-password-that-is-long")
	if err != nil || admin.Role != "admin" {
		t.Fatalf("admin sign in: user=%+v err=%v", admin, err)
	}
	if _, err := store.SignInPassword(ctx, "admin@example.com", "wrong-password-that-is-long"); err == nil {
		t.Fatal("wrong password was accepted")
	}
	member, err := store.RegisterUser(ctx, "member@example.com", "member-password-that-is-long")
	if err != nil || member.Role != "member" {
		t.Fatalf("member registration: user=%+v err=%v", member, err)
	}
	if _, err := store.RegisterUser(ctx, "MEMBER@example.com", "member-password-that-is-long"); err == nil {
		t.Fatal("duplicate email was accepted")
	}
	social, err := store.SignInExternal(ctx, ExternalIdentity{Provider: "test", Subject: "bootstrap-subject", Email: "bootstrap@example.com", EmailVerified: true, DisplayName: "Bootstrap User"})
	if err != nil {
		t.Fatalf("social registration: %v", err)
	}
	if err := store.BootstrapAdmin(ctx, social.Email, "bootstrap-password-that-is-long"); err != nil {
		t.Fatalf("passwordless bootstrap promotion: %v", err)
	}
	promoted, err := store.SignInPassword(ctx, social.Email, "bootstrap-password-that-is-long")
	if err != nil || promoted.Role != "admin" {
		t.Fatalf("promoted admin sign in: user=%+v err=%v", promoted, err)
	}
	token, csrf, err := store.CreateAuthSession(ctx, admin, time.Now())
	if err != nil || token == "" || csrf == "" {
		t.Fatalf("session creation: token=%t csrf=%t err=%v", token != "", csrf != "", err)
	}
	record, err := store.authSession(ctx, token, time.Now())
	if err != nil || record.User.Email != admin.Email || record.CSRFHash == "" {
		t.Fatalf("session lookup: record=%+v err=%v", record, err)
	}
	if err := store.RevokeAuthSession(ctx, token); err != nil {
		t.Fatal(err)
	}
	if _, err := store.authSession(ctx, token, time.Now()); err == nil {
		t.Fatal("revoked session was accepted")
	}
}

func TestAuthHTTPLoginAndCSRF(t *testing.T) {
	store := testStore(t, true)
	ctx := context.Background()
	if err := store.BootstrapAdmin(ctx, "admin@example.com", "admin-password-that-is-long"); err != nil {
		t.Fatal(err)
	}
	auth, err := NewAuthenticator(store, AuthConfig{LegacyToken: adminTestToken, Organization: store.Organization, AdminEmail: "admin@example.com", PublicURL: "http://127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	handler := auth
	configRequest := httptest.NewRequest(http.MethodGet, "/v1/auth/config", nil)
	configResponse := httptest.NewRecorder()
	handler.ServeHTTP(configResponse, configRequest)
	if configResponse.Code != http.StatusOK || configResponse.Result().Cookies()[0].Name != csrfCookie {
		t.Fatalf("config status=%d cookies=%v", configResponse.Code, configResponse.Result().Cookies())
	}
	loginRequest := httptest.NewRequest(http.MethodPost, "/v1/auth/login", jsonBody(authCredentials{Email: "admin@example.com", Password: "admin-password-that-is-long"}))
	loginRequest.RemoteAddr = "127.0.0.1:1234"
	csrfSeed := configResponse.Result().Cookies()[0]
	loginRequest.AddCookie(csrfSeed)
	loginRequest.Header.Set("X-Loom-CSRF", csrfSeed.Value)
	loginResponse := httptest.NewRecorder()
	handler.ServeHTTP(loginResponse, loginRequest)
	if loginResponse.Code != http.StatusOK {
		t.Fatalf("login status=%d body=%s", loginResponse.Code, loginResponse.Body.String())
	}
	var sessionCookie, csrfCookieValue *http.Cookie
	for _, cookie := range loginResponse.Result().Cookies() {
		if cookie.Name == authSessionCookie {
			sessionCookie = cookie
		}
		if cookie.Name == csrfCookie {
			csrfCookieValue = cookie
		}
	}
	if sessionCookie == nil || csrfCookieValue == nil {
		t.Fatal("login did not set session and csrf cookies")
	}
	protected := httptest.NewRequest(http.MethodGet, "/v1/auth/session", nil)
	protected.AddCookie(sessionCookie)
	protected.AddCookie(csrfCookieValue)
	protectedResponse := httptest.NewRecorder()
	handler.ServeHTTP(protectedResponse, protected)
	if protectedResponse.Code != http.StatusOK {
		t.Fatalf("session status=%d body=%s", protectedResponse.Code, protectedResponse.Body.String())
	}
	api := NewAPIHandlerWithAuthorizer(store, auth.Authorized)
	workflowsRequest := httptest.NewRequest(http.MethodGet, "/v1/workflows", nil)
	workflowsRequest.AddCookie(sessionCookie)
	workflowsRequest.AddCookie(csrfCookieValue)
	workflowsResponse := httptest.NewRecorder()
	api.ServeHTTP(workflowsResponse, workflowsRequest)
	if workflowsResponse.Code != http.StatusOK {
		t.Fatalf("session-authorized API status=%d body=%s", workflowsResponse.Code, workflowsResponse.Body.String())
	}
	register := httptest.NewRequest(http.MethodPost, "/v1/auth/register", jsonBody(authCredentials{Email: "new@example.com", Password: "new-password-that-is-long"}))
	register.AddCookie(csrfCookieValue)
	register.Header.Set("X-Loom-CSRF", csrfCookieValue.Value)
	registerResponse := httptest.NewRecorder()
	handler.ServeHTTP(registerResponse, register)
	if registerResponse.Code != http.StatusCreated {
		t.Fatalf("register status=%d body=%s", registerResponse.Code, registerResponse.Body.String())
	}
	var memberSession, memberCSRF *http.Cookie
	for _, cookie := range registerResponse.Result().Cookies() {
		if cookie.Name == authSessionCookie {
			memberSession = cookie
		}
		if cookie.Name == csrfCookie {
			memberCSRF = cookie
		}
	}
	if memberSession == nil || memberCSRF == nil {
		t.Fatal("registration did not set member session cookies")
	}
	adminAPI := NewAPIHandlerWithAuthorizers(store, auth.Authorized, auth.AdminAuthorized)
	memberMutation := httptest.NewRequest(http.MethodPost, "/v1/workflows", jsonBody(map[string]string{"name": "member attempt"}))
	memberMutation.AddCookie(memberSession)
	memberMutation.AddCookie(memberCSRF)
	memberMutation.Header.Set("X-Loom-CSRF", memberCSRF.Value)
	memberMutationResponse := httptest.NewRecorder()
	adminAPI.ServeHTTP(memberMutationResponse, memberMutation)
	if memberMutationResponse.Code != http.StatusUnauthorized {
		t.Fatalf("member mutation status=%d body=%s", memberMutationResponse.Code, memberMutationResponse.Body.String())
	}
}

type testAuthProvider struct{}

func (testAuthProvider) ID() string    { return "test" }
func (testAuthProvider) Name() string  { return "Test provider" }
func (testAuthProvider) Enabled() bool { return true }
func (testAuthProvider) AuthorizationURL(redirectURI, state, challenge string) (string, error) {
	return "https://provider.example/authorize?" + url.Values{"redirect_uri": {redirectURI}, "state": {state}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}}.Encode(), nil
}
func (testAuthProvider) Authenticate(_ context.Context, code, verifier, redirectURI string) (ExternalIdentity, error) {
	if code != "test-code" || verifier == "" || redirectURI == "" {
		return ExternalIdentity{}, fmt.Errorf("invalid test OAuth exchange")
	}
	return ExternalIdentity{Provider: "test", Subject: "subject-42", Email: "social@example.com", EmailVerified: true, DisplayName: "Social User"}, nil
}

func TestSocialOAuthUsesStatePKCEAndProviderBoundary(t *testing.T) {
	store := testStore(t, true)
	auth, err := NewAuthenticator(store, AuthConfig{LegacyToken: adminTestToken, Organization: store.Organization, AdminEmail: "admin@example.com", PublicURL: "https://loom.example.com", Providers: []AuthProvider{testAuthProvider{}}})
	if err != nil {
		t.Fatal(err)
	}
	start := httptest.NewRequest(http.MethodGet, "/v1/auth/test/start", nil)
	startResponse := httptest.NewRecorder()
	auth.ServeHTTP(startResponse, start)
	if startResponse.Code != http.StatusFound {
		t.Fatalf("start status=%d body=%s", startResponse.Code, startResponse.Body.String())
	}
	redirect, err := url.Parse(startResponse.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	state := redirect.Query().Get("state")
	if state == "" || redirect.Query().Get("code_challenge_method") != "S256" || redirect.Query().Get("code_challenge") == "" {
		t.Fatalf("missing state or PKCE parameters: %s", redirect.String())
	}
	var oauthState *http.Cookie
	for _, cookie := range startResponse.Result().Cookies() {
		if cookie.Name == oauthCookie {
			oauthState = cookie
		}
	}
	if oauthState == nil {
		t.Fatal("OAuth state cookie missing")
	}
	wrongState := httptest.NewRequest(http.MethodGet, "/v1/auth/test/callback?code=test-code&state=wrong-state", nil)
	wrongState.AddCookie(oauthState)
	wrongResponse := httptest.NewRecorder()
	auth.ServeHTTP(wrongResponse, wrongState)
	if wrongResponse.Code != http.StatusFound || !strings.Contains(wrongResponse.Header().Get("Location"), "auth_error=social_failed") {
		t.Fatalf("mismatched OAuth state status=%d location=%q", wrongResponse.Code, wrongResponse.Header().Get("Location"))
	}
	callback := httptest.NewRequest(http.MethodGet, "/v1/auth/test/callback?code=test-code&state="+url.QueryEscape(state), nil)
	callback.AddCookie(oauthState)
	callbackResponse := httptest.NewRecorder()
	auth.ServeHTTP(callbackResponse, callback)
	if callbackResponse.Code != http.StatusFound || !strings.HasSuffix(callbackResponse.Header().Get("Location"), "/") {
		t.Fatalf("callback status=%d location=%q body=%s", callbackResponse.Code, callbackResponse.Header().Get("Location"), callbackResponse.Body.String())
	}
	var sessionCookie *http.Cookie
	for _, cookie := range callbackResponse.Result().Cookies() {
		if cookie.Name == authSessionCookie {
			sessionCookie = cookie
		}
	}
	if sessionCookie == nil {
		t.Fatal("OAuth callback did not create a session")
	}
	replay := httptest.NewRequest(http.MethodGet, "/v1/auth/test/callback?code=test-code&state="+url.QueryEscape(state), nil)
	replay.AddCookie(oauthState)
	replayResponse := httptest.NewRecorder()
	auth.ServeHTTP(replayResponse, replay)
	if replayResponse.Code != http.StatusFound || !strings.Contains(replayResponse.Header().Get("Location"), "auth_error=social_failed") {
		t.Fatalf("OAuth replay status=%d location=%q", replayResponse.Code, replayResponse.Header().Get("Location"))
	}
	user, err := store.SignInPassword(context.Background(), "social@example.com", "not-a-password")
	if err == nil || user.ID != "" {
		t.Fatal("OAuth-only account unexpectedly has a password")
	}
	current := httptest.NewRequest(http.MethodGet, "/v1/auth/session", nil)
	current.AddCookie(sessionCookie)
	currentResponse := httptest.NewRecorder()
	auth.ServeHTTP(currentResponse, current)
	if currentResponse.Code != http.StatusOK || !strings.Contains(currentResponse.Body.String(), "social@example.com") {
		t.Fatalf("OAuth session status=%d body=%s", currentResponse.Code, currentResponse.Body.String())
	}
	deniedStart := httptest.NewRecorder()
	auth.ServeHTTP(deniedStart, httptest.NewRequest(http.MethodGet, "/v1/auth/test/start", nil))
	deniedRedirect, err := url.Parse(deniedStart.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	deniedState := deniedRedirect.Query().Get("state")
	var deniedCookie *http.Cookie
	for _, cookie := range deniedStart.Result().Cookies() {
		if cookie.Name == oauthCookie {
			deniedCookie = cookie
		}
	}
	if deniedCookie == nil || deniedState == "" {
		t.Fatal("OAuth denial transaction was not initialized")
	}
	denied := httptest.NewRequest(http.MethodGet, "/v1/auth/test/callback?error=access_denied&state="+url.QueryEscape(deniedState), nil)
	denied.AddCookie(deniedCookie)
	deniedResponse := httptest.NewRecorder()
	auth.ServeHTTP(deniedResponse, denied)
	if deniedResponse.Code != http.StatusFound || !strings.Contains(deniedResponse.Header().Get("Location"), "auth_error=social_failed") {
		t.Fatalf("OAuth denial status=%d location=%q", deniedResponse.Code, deniedResponse.Header().Get("Location"))
	}
	deniedRetry := httptest.NewRequest(http.MethodGet, "/v1/auth/test/callback?code=test-code&state="+url.QueryEscape(deniedState), nil)
	deniedRetry.AddCookie(deniedCookie)
	deniedRetryResponse := httptest.NewRecorder()
	auth.ServeHTTP(deniedRetryResponse, deniedRetry)
	if deniedRetryResponse.Code != http.StatusFound || !strings.Contains(deniedRetryResponse.Header().Get("Location"), "auth_error=social_failed") {
		t.Fatalf("OAuth denial replay status=%d location=%q", deniedRetryResponse.Code, deniedRetryResponse.Header().Get("Location"))
	}
}

func TestAuthenticatorRejectsInsecurePublicURL(t *testing.T) {
	store := testStore(t, true)
	if _, err := NewAuthenticator(store, AuthConfig{Organization: store.Organization, AdminEmail: "admin@example.com", PublicURL: "http://loom.example.com"}); err == nil {
		t.Fatal("non-loopback HTTP public URL was accepted")
	}
}

func TestOAuthStateRejectsTamperingAndExpiry(t *testing.T) {
	store := testStore(t, true)
	auth, err := NewAuthenticator(store, AuthConfig{Organization: store.Organization, AdminEmail: "admin@example.com", PublicURL: "https://loom.example.com", OAuthStateKey: "01234567890123456789012345678901"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	encoded, err := auth.sealOAuthState(oauthState{Provider: "test", State: "state", Verifier: "verifier", ExpiresAt: now.Add(time.Minute).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := auth.verifyOAuthState(encoded+"tampered", now); ok {
		t.Fatal("tampered OAuth state was accepted")
	}
	expired, err := auth.sealOAuthState(oauthState{Provider: "test", State: "state", Verifier: "verifier", ExpiresAt: now.Add(-time.Minute).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := auth.verifyOAuthState(expired, now); ok {
		t.Fatal("expired OAuth state was accepted")
	}
}

func TestAuthenticatorRequiresStoreOrganizationMatch(t *testing.T) {
	store := testStore(t, true)
	if _, err := NewAuthenticator(store, AuthConfig{Organization: "other-org", AdminEmail: "admin@example.com", PublicURL: "https://loom.example.com"}); err == nil {
		t.Fatal("organization mismatch was accepted")
	}
}
