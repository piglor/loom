package google

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/piglor/loom/services/loom/internal/control"
)

const (
	oauthBodyLimit = 128 << 10
	defaultKeyTTL  = 5 * time.Minute
	maxKeyTTL      = 24 * time.Hour
	clockSkew      = 60 * time.Second
)

// OAuthProvider is Google's optional social-login plugin. It keeps the
// provider-specific OAuth and OIDC exchange outside generic account/session
// policy. The ID token is verified locally against Google's rotating JWKS;
// an access token or an unverified profile response is never used as identity.
type OAuthProvider struct {
	clientID string
	secret   string
	client   *http.Client

	keysMu      sync.Mutex
	keys        map[string]*rsa.PublicKey
	keysExpires time.Time
	jwksURL     string
}

func NewOAuthProvider(clientID, secret string) *OAuthProvider {
	return &OAuthProvider{
		clientID: strings.TrimSpace(clientID),
		secret:   strings.TrimSpace(secret),
		client: &http.Client{
			Timeout: 10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		jwksURL: "https://www.googleapis.com/oauth2/v3/certs",
	}
}

func (p *OAuthProvider) ID() string   { return "google" }
func (p *OAuthProvider) Name() string { return "Google" }
func (p *OAuthProvider) Enabled() bool {
	return p != nil && p.clientID != "" && p.secret != "" && p.client != nil
}

func (p *OAuthProvider) AuthorizationURL(redirectURI, state, challenge, nonce string) (string, error) {
	if !p.Enabled() || redirectURI == "" || state == "" || challenge == "" || nonce == "" {
		return "", errors.New("Google OAuth is not configured")
	}
	query := url.Values{
		"client_id":             {p.clientID},
		"redirect_uri":          {redirectURI},
		"response_type":         {"code"},
		"scope":                 {"openid email profile"},
		"state":                 {state},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"nonce":                 {nonce},
		"access_type":           {"online"},
	}
	return "https://accounts.google.com/o/oauth2/v2/auth?" + query.Encode(), nil
}

func (p *OAuthProvider) Authenticate(ctx context.Context, code, verifier, redirectURI, nonce string) (control.ExternalIdentity, error) {
	if !p.Enabled() || code == "" || verifier == "" || redirectURI == "" || nonce == "" {
		return control.ExternalIdentity{}, errors.New("Google OAuth is not configured")
	}
	form := url.Values{
		"client_id":     {p.clientID},
		"client_secret": {p.secret},
		"code":          {code},
		"code_verifier": {verifier},
		"grant_type":    {"authorization_code"},
		"redirect_uri":  {redirectURI},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://oauth2.googleapis.com/token", strings.NewReader(form.Encode()))
	if err != nil {
		return control.ExternalIdentity{}, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := p.client.Do(request)
	if err != nil {
		return control.ExternalIdentity{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return control.ExternalIdentity{}, errors.New("Google token exchange failed")
	}
	var tokenPayload struct {
		IDToken     string `json:"id_token"`
		TokenType   string `json:"token_type"`
		Error       string `json:"error"`
		ErrorDetail string `json:"error_description"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, oauthBodyLimit)).Decode(&tokenPayload); err != nil || tokenPayload.IDToken == "" || tokenPayload.Error != "" || !strings.EqualFold(tokenPayload.TokenType, "bearer") {
		return control.ExternalIdentity{}, errors.New("Google token exchange failed")
	}
	return p.verifyIDToken(ctx, tokenPayload.IDToken, nonce)
}

type idTokenHeader struct {
	Algorithm string `json:"alg"`
	KeyID     string `json:"kid"`
	Type      string `json:"typ"`
}

type idTokenClaims struct {
	Issuer          string          `json:"iss"`
	Audience        json.RawMessage `json:"aud"`
	Subject         string          `json:"sub"`
	ExpiresAt       json.Number     `json:"exp"`
	IssuedAt        json.Number     `json:"iat"`
	AuthorizedParty string          `json:"azp"`
	Nonce           string          `json:"nonce"`
	Email           string          `json:"email"`
	EmailVerified   bool            `json:"email_verified"`
	HostedDomain    string          `json:"hd"`
	Name            string          `json:"name"`
}

func (p *OAuthProvider) verifyIDToken(ctx context.Context, encoded, expectedNonce string) (control.ExternalIdentity, error) {
	parts := strings.Split(encoded, ".")
	if len(parts) != 3 || expectedNonce == "" {
		return control.ExternalIdentity{}, errors.New("Google ID token is invalid")
	}
	headerBytes, err := decodeJWTPart(parts[0])
	if err != nil {
		return control.ExternalIdentity{}, errors.New("Google ID token is invalid")
	}
	var header idTokenHeader
	if err := json.Unmarshal(headerBytes, &header); err != nil || header.Algorithm != "RS256" || header.KeyID == "" || (header.Type != "" && header.Type != "JWT") {
		return control.ExternalIdentity{}, errors.New("Google ID token is invalid")
	}
	claimsBytes, err := decodeJWTPart(parts[1])
	if err != nil {
		return control.ExternalIdentity{}, errors.New("Google ID token is invalid")
	}
	var claims idTokenClaims
	decoder := json.NewDecoder(strings.NewReader(string(claimsBytes)))
	decoder.UseNumber()
	if err := decoder.Decode(&claims); err != nil {
		return control.ExternalIdentity{}, errors.New("Google ID token is invalid")
	}
	expiresAt, err := parseTokenTime(claims.ExpiresAt)
	if err != nil {
		return control.ExternalIdentity{}, errors.New("Google ID token is invalid")
	}
	issuedAt, err := parseTokenTime(claims.IssuedAt)
	if err != nil {
		return control.ExternalIdentity{}, errors.New("Google ID token is invalid")
	}
	now := time.Now()
	if (claims.Issuer != "https://accounts.google.com" && claims.Issuer != "accounts.google.com") || !audienceContains(claims.Audience, p.clientID) || (claims.AuthorizedParty != "" && claims.AuthorizedParty != p.clientID) || strings.TrimSpace(claims.Subject) == "" || claims.Nonce != expectedNonce || expiresAt <= now.Add(-clockSkew).Unix() || issuedAt > now.Add(clockSkew).Unix() || claims.Email == "" || !claims.EmailVerified {
		return control.ExternalIdentity{}, errors.New("Google ID token claims are invalid")
	}

	keys, err := p.publicKeys(ctx, false)
	if err != nil {
		return control.ExternalIdentity{}, errors.New("Google signing keys unavailable")
	}
	key, ok := keys[header.KeyID]
	if !ok {
		// Google rotates signing keys; a cache miss is the one case where a
		// refresh is safe and necessary before rejecting an otherwise valid token.
		keys, err = p.publicKeys(ctx, true)
		if err != nil {
			return control.ExternalIdentity{}, errors.New("Google signing keys unavailable")
		}
		key, ok = keys[header.KeyID]
	}
	if !ok {
		return control.ExternalIdentity{}, errors.New("Google ID token signing key is unknown")
	}
	signature, err := decodeJWTPart(parts[2])
	if err != nil {
		return control.ExternalIdentity{}, errors.New("Google ID token is invalid")
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signature); err != nil {
		return control.ExternalIdentity{}, errors.New("Google ID token signature is invalid")
	}
	return control.ExternalIdentity{
		Provider:            p.ID(),
		Subject:             strings.TrimSpace(claims.Subject),
		Email:               strings.TrimSpace(claims.Email),
		EmailVerified:       true,
		EmailLinkingAllowed: googleEmailLinkingAllowed(claims.Email, claims.HostedDomain),
		DisplayName:         strings.TrimSpace(claims.Name),
	}, nil
}

func googleEmailLinkingAllowed(email, hostedDomain string) bool {
	email = strings.ToLower(strings.TrimSpace(email))
	hostedDomain = strings.TrimSpace(hostedDomain)
	return strings.HasSuffix(email, "@gmail.com") || strings.HasSuffix(email, "@googlemail.com") || hostedDomain != ""
}

func decodeJWTPart(value string) ([]byte, error) {
	if value == "" {
		return nil, errors.New("empty JWT segment")
	}
	return base64.RawURLEncoding.DecodeString(value)
}

func parseTokenTime(value json.Number) (int64, error) {
	if value == "" {
		return 0, errors.New("missing token time")
	}
	return strconv.ParseInt(value.String(), 10, 64)
}

func audienceContains(raw json.RawMessage, expected string) bool {
	var single string
	if json.Unmarshal(raw, &single) == nil {
		return single == expected
	}
	var multiple []string
	if json.Unmarshal(raw, &multiple) != nil {
		return false
	}
	for _, value := range multiple {
		if value == expected {
			return true
		}
	}
	return false
}

func (p *OAuthProvider) publicKeys(ctx context.Context, force bool) (map[string]*rsa.PublicKey, error) {
	now := time.Now()
	p.keysMu.Lock()
	if !force && len(p.keys) > 0 && now.Before(p.keysExpires) {
		keys := p.keys
		p.keysMu.Unlock()
		return keys, nil
	}
	p.keysMu.Unlock()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, p.jwksURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "piglor-loom-auth")
	response, err := p.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, errors.New("Google signing key lookup failed")
	}
	var payload struct {
		Keys []struct {
			KeyType string `json:"kty"`
			KeyID   string `json:"kid"`
			N       string `json:"n"`
			E       string `json:"e"`
			Alg     string `json:"alg"`
			Use     string `json:"use"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, oauthBodyLimit)).Decode(&payload); err != nil {
		return nil, err
	}
	keys := make(map[string]*rsa.PublicKey, len(payload.Keys))
	for _, value := range payload.Keys {
		if value.KeyType != "RSA" || value.KeyID == "" || value.N == "" || value.E == "" || (value.Alg != "" && value.Alg != "RS256") || (value.Use != "" && value.Use != "sig") {
			continue
		}
		modulus, err := base64.RawURLEncoding.DecodeString(value.N)
		if err != nil || len(modulus) < 256 {
			continue
		}
		exponentBytes, err := base64.RawURLEncoding.DecodeString(value.E)
		if err != nil || len(exponentBytes) == 0 || len(exponentBytes) > 4 {
			continue
		}
		exponentBig := new(big.Int).SetBytes(exponentBytes)
		if !exponentBig.IsInt64() || exponentBig.Int64() < 3 {
			continue
		}
		exponent := int(exponentBig.Int64())
		if exponent%2 == 0 {
			continue
		}
		keys[value.KeyID] = &rsa.PublicKey{N: new(big.Int).SetBytes(modulus), E: exponent}
	}
	if len(keys) == 0 {
		return nil, errors.New("Google signing keys are empty")
	}
	ttl := parseKeyTTL(response.Header.Get("Cache-Control"))
	p.keysMu.Lock()
	p.keys = keys
	p.keysExpires = time.Now().Add(ttl)
	p.keysMu.Unlock()
	return keys, nil
}

func parseKeyTTL(cacheControl string) time.Duration {
	for _, directive := range strings.Split(cacheControl, ",") {
		name, value, ok := strings.Cut(strings.TrimSpace(directive), "=")
		if !ok || !strings.EqualFold(name, "max-age") {
			continue
		}
		seconds, err := strconv.ParseInt(strings.Trim(strings.TrimSpace(value), "\""), 10, 64)
		if err == nil && seconds >= 0 {
			ttl := time.Duration(seconds) * time.Second
			if ttl > maxKeyTTL {
				return maxKeyTTL
			}
			return ttl
		}
	}
	return defaultKeyTTL
}
