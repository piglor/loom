package google

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

type oauthRoundTrip func(*http.Request) (*http.Response, error)

func (f oauthRoundTrip) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func jwtPart(value any) string {
	body, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(body)
}

func signedIDToken(t *testing.T, key *rsa.PrivateKey, kid, nonce string, overrides map[string]any) string {
	t.Helper()
	now := time.Now().Unix()
	claims := map[string]any{
		"iss":            "https://accounts.google.com",
		"aud":            "client-id",
		"sub":            "google-subject",
		"exp":            now + 300,
		"iat":            now,
		"nonce":          nonce,
		"email":          "google@gmail.com",
		"email_verified": true,
		"name":           "Google User",
	}
	for name, value := range overrides {
		claims[name] = value
	}
	header := jwtPart(map[string]string{"alg": "RS256", "kid": kid, "typ": "JWT"})
	payload := jwtPart(claims)
	unsigned := header + "." + payload
	digest := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func jwksFor(key *rsa.PrivateKey, kid string) string {
	return fmt.Sprintf(`{"keys":[{"kty":"RSA","kid":%q,"alg":"RS256","use":"sig","n":%q,"e":%q}]}`,
		kid,
		base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes()),
		base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.PublicKey.E)).Bytes()))
}

func TestOAuthProviderOwnsGoogleOIDCProtocol(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	const kid = "test-key"
	provider := NewOAuthProvider("client-id", "client-secret")
	provider.client = &http.Client{Transport: oauthRoundTrip(func(request *http.Request) (*http.Response, error) {
		respond := func(status int, body string) (*http.Response, error) {
			return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: request}, nil
		}
		switch request.URL.Host + request.URL.Path {
		case "oauth2.googleapis.com/token":
			body, _ := io.ReadAll(request.Body)
			values, _ := url.ParseQuery(string(body))
			if values.Get("client_id") != "client-id" || values.Get("client_secret") != "client-secret" || values.Get("code") != "code" || values.Get("code_verifier") != "verifier" || values.Get("grant_type") != "authorization_code" {
				return respond(http.StatusBadRequest, `{}`)
			}
			return respond(http.StatusOK, `{"id_token":"`+signedIDToken(t, key, kid, "nonce", nil)+`","token_type":"Bearer"}`)
		case "www.googleapis.com/oauth2/v3/certs":
			return respond(http.StatusOK, jwksFor(key, kid))
		default:
			return nil, fmt.Errorf("unexpected OAuth request %s", request.URL.String())
		}
	})}

	if !provider.Enabled() || provider.ID() != "google" || provider.Name() != "Google" {
		t.Fatalf("provider metadata is not configured: %+v", provider)
	}
	authorization, err := provider.AuthorizationURL("https://loom.example/callback", "state", "challenge", "nonce")
	if err != nil {
		t.Fatal(err)
	}
	query, err := url.Parse(authorization)
	if err != nil || query.Query().Get("client_id") != "client-id" || query.Query().Get("code_challenge_method") != "S256" || query.Query().Get("scope") != "openid email profile" || query.Query().Get("nonce") != "nonce" {
		t.Fatalf("authorization URL missing OIDC parameters: %s", authorization)
	}
	identity, err := provider.Authenticate(t.Context(), "code", "verifier", "https://loom.example/callback", "nonce")
	if err != nil {
		t.Fatal(err)
	}
	if identity.Provider != "google" || identity.Subject != "google-subject" || identity.Email != "google@gmail.com" || !identity.EmailVerified || !identity.EmailLinkingAllowed || identity.DisplayName != "Google User" {
		t.Fatalf("unexpected identity: %+v", identity)
	}
}

func TestOAuthProviderRejectsInvalidGoogleOIDCTokens(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	provider := NewOAuthProvider("client-id", "client-secret")
	provider.client = &http.Client{Transport: oauthRoundTrip(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(jwksFor(key, "test-key"))), Header: make(http.Header), Request: request}, nil
	})}
	valid := signedIDToken(t, key, "test-key", "nonce", nil)
	parts := strings.Split(valid, ".")
	parts[2] = base64.RawURLEncoding.EncodeToString(make([]byte, 256))
	for name, token := range map[string]string{
		"wrong nonce":    signedIDToken(t, key, "test-key", "different", nil),
		"unverified":     signedIDToken(t, key, "test-key", "nonce", map[string]any{"email_verified": false}),
		"wrong audience": signedIDToken(t, key, "test-key", "nonce", map[string]any{"aud": "other-client"}),
		"wrong issuer":   signedIDToken(t, key, "test-key", "nonce", map[string]any{"iss": "https://issuer.example"}),
		"expired":        signedIDToken(t, key, "test-key", "nonce", map[string]any{"exp": time.Now().Add(-5 * time.Minute).Unix()}),
		"bad signature":  strings.Join(parts, "."),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := provider.verifyIDToken(t.Context(), token, "nonce"); err == nil {
				t.Fatal("invalid Google ID token was accepted")
			}
		})
	}
}

func TestGoogleEmailLinkingAuthority(t *testing.T) {
	for email, allowed := range map[string]bool{
		"person@gmail.com":      true,
		"person@googlemail.com": true,
		"person@example.com":    false,
	} {
		if got := googleEmailLinkingAllowed(email, ""); got != allowed {
			t.Errorf("googleEmailLinkingAllowed(%q)=%t, want %t", email, got, allowed)
		}
	}
	if !googleEmailLinkingAllowed("person@example.com", "example.com") {
		t.Error("Google Workspace hosted domain was not treated as authoritative")
	}
}
