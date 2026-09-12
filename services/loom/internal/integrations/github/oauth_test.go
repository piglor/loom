package github

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

type oauthRoundTrip func(*http.Request) (*http.Response, error)

func (f oauthRoundTrip) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestOAuthProviderOwnsGitHubProtocol(t *testing.T) {
	provider := NewOAuthProvider("client-id", "client-secret")
	provider.client = &http.Client{Transport: oauthRoundTrip(func(request *http.Request) (*http.Response, error) {
		respond := func(status int, body string) (*http.Response, error) {
			return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: request}, nil
		}
		switch request.URL.Host + request.URL.Path {
		case "github.com/login/oauth/access_token":
			body, _ := io.ReadAll(request.Body)
			values, _ := url.ParseQuery(string(body))
			if values.Get("client_id") != "client-id" || values.Get("client_secret") != "client-secret" || values.Get("code") != "code" || values.Get("code_verifier") != "verifier" {
				return respond(http.StatusBadRequest, `{}`)
			}
			return respond(http.StatusOK, `{"access_token":"token","token_type":"bearer"}`)
		case "api.github.com/user":
			if request.Header.Get("Authorization") != "Bearer token" {
				return respond(http.StatusUnauthorized, `{}`)
			}
			return respond(http.StatusOK, `{"id":42,"login":"loom-user","name":"Loom User"}`)
		case "api.github.com/user/emails":
			return respond(http.StatusOK, `[{"email":"github@example.com","primary":true,"verified":true}]`)
		default:
			return nil, fmt.Errorf("unexpected OAuth request %s", request.URL.String())
		}
	})}

	if !provider.Enabled() || provider.ID() != "github" || provider.Name() != "GitHub" {
		t.Fatalf("provider metadata is not configured: %+v", provider)
	}
	authorization, err := provider.AuthorizationURL("https://loom.example/callback", "state", "challenge", "nonce")
	if err != nil {
		t.Fatal(err)
	}
	query, err := url.Parse(authorization)
	if err != nil || query.Query().Get("client_id") != "client-id" || query.Query().Get("code_challenge_method") != "S256" {
		t.Fatalf("authorization URL missing OAuth parameters: %s", authorization)
	}
	identity, err := provider.Authenticate(t.Context(), "code", "verifier", "https://loom.example/callback", "nonce")
	if err != nil {
		t.Fatal(err)
	}
	if identity.Provider != "github" || identity.Subject != "42" || identity.Email != "github@example.com" || !identity.EmailVerified || identity.DisplayName != "Loom User" {
		t.Fatalf("unexpected identity: %+v", identity)
	}
}
