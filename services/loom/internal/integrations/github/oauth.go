package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/piglor/loom/services/loom/internal/control"
)

const oauthBodyLimit = 128 << 10

// OAuthProvider is GitHub's optional social-login plugin. It owns all
// GitHub-specific endpoints and identity rules; control.Authenticator only
// coordinates generic OAuth state and account/session policy.
type OAuthProvider struct {
	clientID string
	secret   string
	client   *http.Client
}

func NewOAuthProvider(clientID, secret string) *OAuthProvider {
	return &OAuthProvider{
		clientID: strings.TrimSpace(clientID),
		secret:   strings.TrimSpace(secret),
		client:   &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}
}

func (p *OAuthProvider) ID() string   { return "github" }
func (p *OAuthProvider) Name() string { return "GitHub" }
func (p *OAuthProvider) Enabled() bool {
	return p != nil && p.clientID != "" && p.secret != "" && p.client != nil
}

func (p *OAuthProvider) AuthorizationURL(redirectURI, state, challenge, nonce string) (string, error) {
	if !p.Enabled() || redirectURI == "" || state == "" || challenge == "" || nonce == "" {
		return "", errors.New("GitHub OAuth is not configured")
	}
	query := url.Values{
		"client_id":             {p.clientID},
		"redirect_uri":          {redirectURI},
		"scope":                 {"read:user user:email"},
		"state":                 {state},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	return "https://github.com/login/oauth/authorize?" + query.Encode(), nil
}

func (p *OAuthProvider) Authenticate(ctx context.Context, code, verifier, redirectURI, nonce string) (control.ExternalIdentity, error) {
	if !p.Enabled() || code == "" || verifier == "" || redirectURI == "" || nonce == "" {
		return control.ExternalIdentity{}, errors.New("GitHub OAuth is not configured")
	}
	form := url.Values{"client_id": {p.clientID}, "client_secret": {p.secret}, "code": {code}, "redirect_uri": {redirectURI}, "code_verifier": {verifier}}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://github.com/login/oauth/access_token", strings.NewReader(form.Encode()))
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
		return control.ExternalIdentity{}, errors.New("GitHub token exchange failed")
	}
	var tokenPayload struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		Error       string `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, oauthBodyLimit)).Decode(&tokenPayload); err != nil || tokenPayload.AccessToken == "" || tokenPayload.Error != "" || !strings.EqualFold(tokenPayload.TokenType, "bearer") {
		return control.ExternalIdentity{}, errors.New("GitHub token exchange failed")
	}
	return p.identity(ctx, tokenPayload.AccessToken)
}

func (p *OAuthProvider) identity(ctx context.Context, token string) (control.ExternalIdentity, error) {
	profileRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/user", nil)
	if err != nil {
		return control.ExternalIdentity{}, err
	}
	p.setHeaders(profileRequest, token)
	response, err := p.client.Do(profileRequest)
	if err != nil {
		return control.ExternalIdentity{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return control.ExternalIdentity{}, errors.New("GitHub identity lookup failed")
	}
	var profile struct {
		ID    int64  `json:"id"`
		Name  string `json:"name"`
		Login string `json:"login"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, oauthBodyLimit)).Decode(&profile); err != nil || profile.ID == 0 {
		return control.ExternalIdentity{}, errors.New("GitHub identity lookup failed")
	}
	emailRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/user/emails", nil)
	if err != nil {
		return control.ExternalIdentity{}, err
	}
	p.setHeaders(emailRequest, token)
	emailResponse, err := p.client.Do(emailRequest)
	if err != nil {
		return control.ExternalIdentity{}, err
	}
	defer emailResponse.Body.Close()
	if emailResponse.StatusCode < 200 || emailResponse.StatusCode >= 300 {
		return control.ExternalIdentity{}, errors.New("GitHub email lookup failed")
	}
	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if err := json.NewDecoder(io.LimitReader(emailResponse.Body, oauthBodyLimit)).Decode(&emails); err != nil {
		return control.ExternalIdentity{}, errors.New("GitHub email lookup failed")
	}
	for _, candidate := range emails {
		if candidate.Primary && candidate.Verified {
			return control.ExternalIdentity{Provider: p.ID(), Subject: fmt.Sprintf("%d", profile.ID), Email: candidate.Email, EmailVerified: true, EmailLinkingAllowed: true, DisplayName: firstNonEmpty(profile.Name, profile.Login)}, nil
		}
	}
	return control.ExternalIdentity{}, errors.New("GitHub did not provide a verified primary email")
}

func (p *OAuthProvider) setHeaders(request *http.Request, token string) {
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	request.Header.Set("User-Agent", "piglor-loom-auth")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
