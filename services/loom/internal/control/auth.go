package control

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/mail"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"

	"github.com/piglor/loom/services/loom/ent"
	"github.com/piglor/loom/services/loom/ent/authidentity"
	"github.com/piglor/loom/services/loom/ent/authoauthstate"
	"github.com/piglor/loom/services/loom/ent/authsession"
	"github.com/piglor/loom/services/loom/ent/user"
)

const (
	// Keep the name usable for local HTTP development too; Secure is still
	// enabled for every HTTPS deployment and the cookie has Path=/ with no
	// Domain attribute.
	authSessionCookie = "loom_session"
	csrfCookie        = "loom_csrf"
	oauthCookie       = "loom_auth_oauth"
	sessionLifetime   = 30 * 24 * time.Hour
	oauthLifetime     = 10 * time.Minute
	maxPasswordBytes  = 1024
)

type AuthUser struct {
	ID           string `json:"id"`
	Organization string `json:"organization"`
	Email        string `json:"email"`
	DisplayName  string `json:"display_name"`
	Role         string `json:"role"`
}

// AuthProvider is the small seam between account/session policy and a social
// login plugin. The control plane owns state, PKCE, cookies and account
// linking; a provider owns its external OAuth protocol and identity mapping.
type AuthProvider interface {
	ID() string
	Name() string
	Enabled() bool
	AuthorizationURL(redirectURI, state, challenge, nonce string) (string, error)
	Authenticate(ctx context.Context, code, verifier, redirectURI, nonce string) (ExternalIdentity, error)
}

type ExternalIdentity struct {
	Provider      string
	Subject       string
	Email         string
	EmailVerified bool
	// EmailLinkingAllowed is provider-specific authority to merge this
	// identity into an existing passwordless account with the same email.
	// A verified email alone is not sufficient for providers where addresses
	// can be reclaimed or are not controlled by the provider.
	EmailLinkingAllowed bool
	DisplayName         string
}

type AuthProviderInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type authSessionRecord struct {
	User     AuthUser
	CSRFHash string
}

func normalizeEmail(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) == 0 || len(value) > 254 || strings.ContainsAny(value, "\r\n") {
		return "", invalid("Enter a valid email address")
	}
	parsed, err := mail.ParseAddress(value)
	if err != nil || parsed.Address != value || !strings.Contains(value, "@") {
		return "", invalid("Enter a valid email address")
	}
	return value, nil
}

func validatePassword(value string) error {
	if len(value) < 12 || len(value) > maxPasswordBytes {
		return invalid("Password must be between 12 and 1024 characters")
	}
	return nil
}

const (
	argonMemory      = 19 * 1024
	argonIterations  = 2
	argonParallelism = 1
	argonKeyLength   = 32
	argonSaltLength  = 16
)

func hashPassword(password string) (string, error) {
	if err := validatePassword(password); err != nil {
		return "", err
	}
	salt := make([]byte, argonSaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", errors.New("password hashing unavailable")
	}
	key := argon2.IDKey([]byte(password), salt, argonIterations, argonMemory, argonParallelism, argonKeyLength)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", argonMemory, argonIterations, argonParallelism,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

func verifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" {
		return false
	}
	var memory, iterations, parallelism uint32
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &parallelism); err != nil || memory == 0 || iterations == 0 || parallelism == 0 || memory > 256*1024 || iterations > 10 || parallelism > 8 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) < 8 || len(salt) > 64 {
		return false
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(expected) != argonKeyLength {
		return false
	}
	actual := argon2.IDKey([]byte(password), salt, iterations, memory, uint8(parallelism), uint32(len(expected)))
	return subtleConstantTimeEqual(actual, expected)
}

func subtleConstantTimeEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var result byte
	for i := range a {
		result |= a[i] ^ b[i]
	}
	return result == 0
}

var invalidPasswordHash struct {
	sync.Once
	value string
}

func dummyPasswordHash() string {
	invalidPasswordHash.Do(func() {
		invalidPasswordHash.value, _ = hashPassword("loom-invalid-password-sentinel")
	})
	return invalidPasswordHash.value
}

func randomSecret(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func secretDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func authUserFromEntity(value *ent.User) AuthUser {
	return AuthUser{ID: value.ID, Organization: value.Organization, Email: value.Email, DisplayName: value.DisplayName, Role: value.Role}
}

func (s *Store) BootstrapAdmin(ctx context.Context, email, password string) error {
	normalized, err := normalizeEmail(email)
	if err != nil {
		return err
	}
	if err := validatePassword(password); err != nil {
		return err
	}
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	return s.tx(ctx, func(t *transaction) error {
		existing, queryErr := t.client.User.Query().Where(user.OrganizationEQ(s.Organization), user.EmailEQ(normalized)).Only(ctx)
		if queryErr == nil {
			if existing.Role != "admin" || existing.PasswordHash == nil {
				update := t.client.User.UpdateOneID(existing.ID).SetRole("admin").SetUpdatedAt(t.now)
				if existing.PasswordHash == nil {
					update.SetPasswordHash(hash)
				}
				return update.Exec(ctx)
			}
			return nil
		}
		if !ent.IsNotFound(queryErr) {
			return queryErr
		}
		_, err = t.client.User.Create().SetID(ID()).SetOrganization(s.Organization).SetEmail(normalized).SetDisplayName("Administrator").SetRole("admin").SetPasswordHash(hash).SetCreatedAt(t.now).SetUpdatedAt(t.now).Save(ctx)
		return err
	})
}

func (s *Store) userByEmail(ctx context.Context, email string) (AuthUser, string, error) {
	var result AuthUser
	var passwordHash string
	err := s.tx(ctx, func(t *transaction) error {
		value, err := t.client.User.Query().Where(user.OrganizationEQ(s.Organization), user.EmailEQ(email), user.DisabledAtIsNil()).Only(ctx)
		if ent.IsNotFound(err) {
			return unauthorized()
		}
		if err != nil {
			return err
		}
		result = authUserFromEntity(value)
		if value.PasswordHash != nil {
			passwordHash = *value.PasswordHash
		}
		return nil
	})
	return result, passwordHash, err
}

func (s *Store) RegisterUser(ctx context.Context, email, password string) (AuthUser, error) {
	normalized, err := normalizeEmail(email)
	if err != nil {
		return AuthUser{}, err
	}
	hash, err := hashPassword(password)
	if err != nil {
		return AuthUser{}, err
	}
	var result AuthUser
	err = s.tx(ctx, func(t *transaction) error {
		value, queryErr := t.client.User.Create().SetID(ID()).SetOrganization(s.Organization).SetEmail(normalized).SetRole("member").SetPasswordHash(hash).SetCreatedAt(t.now).SetUpdatedAt(t.now).Save(ctx)
		if ent.IsConstraintError(queryErr) {
			return conflict("An account with that email already exists")
		}
		if queryErr != nil {
			return queryErr
		}
		result = authUserFromEntity(value)
		return nil
	})
	return result, err
}

func (s *Store) SignInPassword(ctx context.Context, email, password string) (AuthUser, error) {
	normalized, err := normalizeEmail(email)
	if err != nil {
		return AuthUser{}, unauthorized()
	}
	result, encoded, err := s.userByEmail(ctx, normalized)
	if err != nil || encoded == "" {
		_ = verifyPassword(dummyPasswordHash(), password)
		return AuthUser{}, unauthorized()
	}
	if !verifyPassword(encoded, password) {
		return AuthUser{}, unauthorized()
	}
	return result, nil
}

func (s *Store) SignInExternal(ctx context.Context, identity ExternalIdentity) (AuthUser, error) {
	provider := strings.TrimSpace(identity.Provider)
	if provider == "" || strings.TrimSpace(identity.Subject) == "" {
		return AuthUser{}, invalid("External identity is missing")
	}
	if !identity.EmailVerified {
		return AuthUser{}, invalid("External identity email is not verified")
	}
	normalized, err := normalizeEmail(identity.Email)
	if err != nil {
		return AuthUser{}, invalid("The identity provider did not provide a verified email")
	}
	var result AuthUser
	err = s.tx(ctx, func(t *transaction) error {
		linked, identityErr := t.client.AuthIdentity.Query().Where(authidentity.OrganizationEQ(s.Organization), authidentity.ProviderEQ(provider), authidentity.SubjectEQ(identity.Subject)).Only(ctx)
		if identityErr == nil {
			value, userErr := t.client.User.Query().Where(user.IDEQ(linked.UserID), user.OrganizationEQ(s.Organization), user.DisabledAtIsNil()).Only(ctx)
			if userErr != nil {
				return userErr
			}
			result = authUserFromEntity(value)
			return nil
		}
		if !ent.IsNotFound(identityErr) {
			return identityErr
		}
		value, userErr := t.client.User.Query().Where(user.OrganizationEQ(s.Organization), user.EmailEQ(normalized), user.DisabledAtIsNil()).Only(ctx)
		created := false
		if ent.IsNotFound(userErr) {
			value, userErr = t.client.User.Create().SetID(ID()).SetOrganization(s.Organization).SetEmail(normalized).SetDisplayName(strings.TrimSpace(identity.DisplayName)).SetRole("member").SetCreatedAt(t.now).SetUpdatedAt(t.now).Save(ctx)
			created = userErr == nil
		}
		if userErr != nil {
			return userErr
		}
		// Do not silently merge an external identity into an existing password
		// account. Email registration is intentionally not email-verified yet;
		// automatic merging here would let an address claim another account. A
		// provider must explicitly attest that its address is stable enough for
		// linking (for example, Gmail or a signed Google Workspace domain).
		if !created && (value.PasswordHash != nil || !identity.EmailLinkingAllowed) {
			return conflict("An account with that email already exists; sign in with email")
		}
		if _, createErr := t.client.AuthIdentity.Create().SetID(ID()).SetOrganization(s.Organization).SetUserID(value.ID).SetProvider(provider).SetSubject(identity.Subject).SetEmail(normalized).SetCreatedAt(t.now).Save(ctx); createErr != nil {
			if ent.IsConstraintError(createErr) {
				return conflict("This account is already linked")
			}
			return createErr
		}
		result = authUserFromEntity(value)
		return nil
	})
	return result, err
}

func (s *Store) CreateAuthSession(ctx context.Context, user AuthUser, now time.Time) (token, csrf string, err error) {
	if user.ID == "" || user.Organization != s.Organization {
		return "", "", unauthorized()
	}
	token, err = randomSecret(32)
	if err != nil {
		return "", "", err
	}
	csrf, err = randomSecret(32)
	if err != nil {
		return "", "", err
	}
	err = s.tx(ctx, func(t *transaction) error {
		_, createErr := t.client.AuthSession.Create().SetID(ID()).SetOrganization(s.Organization).SetUserID(user.ID).SetTokenHash(secretDigest(token)).SetCsrfHash(secretDigest(csrf)).SetCreatedAt(now).SetLastSeenAt(now).SetExpiresAt(now.Add(sessionLifetime)).Save(ctx)
		return createErr
	})
	if err != nil {
		return "", "", err
	}
	return token, csrf, nil
}

func (s *Store) authSession(ctx context.Context, token string, now time.Time) (authSessionRecord, error) {
	var result authSessionRecord
	err := s.tx(ctx, func(t *transaction) error {
		session, err := t.client.AuthSession.Query().Where(authsession.OrganizationEQ(s.Organization), authsession.TokenHashEQ(secretDigest(token)), authsession.RevokedAtIsNil(), authsession.ExpiresAtGT(now)).Only(ctx)
		if ent.IsNotFound(err) {
			return unauthorized()
		}
		if err != nil {
			return err
		}
		value, err := t.client.User.Query().Where(user.IDEQ(session.UserID), user.OrganizationEQ(s.Organization), user.DisabledAtIsNil()).Only(ctx)
		if ent.IsNotFound(err) {
			return unauthorized()
		}
		if err != nil {
			return err
		}
		result = authSessionRecord{User: authUserFromEntity(value), CSRFHash: session.CsrfHash}
		return nil
	})
	return result, err
}

func (s *Store) RevokeAuthSession(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	return s.tx(ctx, func(t *transaction) error {
		_, err := t.client.AuthSession.Update().Where(authsession.OrganizationEQ(s.Organization), authsession.TokenHashEQ(secretDigest(token)), authsession.RevokedAtIsNil()).SetRevokedAt(t.now).Save(ctx)
		return err
	})
}

// CreateOAuthState records a one-time OAuth transaction in the shared
// database. The signed browser cookie carries the verifier/provider details;
// this row is the replay guard shared by all replicas.
func (s *Store) CreateOAuthState(ctx context.Context, state string, expiresAt time.Time) error {
	if strings.TrimSpace(state) == "" || !expiresAt.After(time.Now()) {
		return invalid("OAuth state is expired")
	}
	return s.tx(ctx, func(t *transaction) error {
		if _, err := t.client.AuthOAuthState.Delete().
			Where(authoauthstate.OrganizationEQ(s.Organization), authoauthstate.ExpiresAtLTE(t.now)).
			Exec(ctx); err != nil {
			return err
		}
		_, err := t.client.AuthOAuthState.Create().
			SetID(ID()).
			SetOrganization(s.Organization).
			SetStateHash(secretDigest(state)).
			SetExpiresAt(expiresAt).
			Save(ctx)
		return err
	})
}

// ConsumeOAuthState atomically marks a still-valid state as consumed. Ent's
// conditional update gives concurrent callbacks a single winner without
// provider-specific SQL.
func (s *Store) ConsumeOAuthState(ctx context.Context, state string, now time.Time) error {
	if strings.TrimSpace(state) == "" {
		return unauthorized()
	}
	return s.tx(ctx, func(t *transaction) error {
		count, err := t.client.AuthOAuthState.Update().
			Where(
				authoauthstate.OrganizationEQ(s.Organization),
				authoauthstate.StateHashEQ(secretDigest(state)),
				authoauthstate.ExpiresAtGT(now),
				authoauthstate.ConsumedAtIsNil(),
			).
			SetConsumedAt(now).
			Save(ctx)
		if err != nil {
			return err
		}
		if count != 1 {
			return unauthorized()
		}
		return nil
	})
}

type AuthConfig struct {
	LegacyToken              string
	Organization             string
	AdminEmail               string
	PublicURL                string
	OAuthStateKey            string
	DisableEmailRegistration bool
	Providers                []AuthProvider
}

// DefaultAdminPassword is the convenience password used when a loopback
// self-hosted installation does not provide LOOM_ADMIN_PASSWORD. Operators
// should set an explicit password before exposing the console beyond a trusted
// network.
const DefaultAdminPassword = "loom-admin-1234"

type loginWindow struct {
	started time.Time
	count   int
}

type Authenticator struct {
	store                    *Store
	legacyToken              string
	organization             string
	adminEmail               string
	publicURL                string
	loopbackPublicURL        bool
	disableEmailRegistration bool
	providers                map[string]AuthProvider
	secureCookies            bool
	oauthKey                 []byte
	limiter                  struct {
		mu      sync.Mutex
		entries map[string]loginWindow
	}
}

func NewAuthenticator(store *Store, config AuthConfig) (*Authenticator, error) {
	if store == nil || strings.TrimSpace(config.Organization) == "" || config.Organization != store.Organization {
		return nil, errors.New("invalid authentication configuration")
	}
	if config.LegacyToken != "" && len(config.LegacyToken) < 32 {
		return nil, errors.New("LOOM_API_TOKEN must contain at least 32 characters")
	}
	adminEmail, err := normalizeEmail(config.AdminEmail)
	if err != nil {
		return nil, errors.New("LOOM_ADMIN_EMAIL must be a valid email")
	}
	publicURL, err := url.Parse(strings.TrimSpace(config.PublicURL))
	if err != nil || publicURL.Host == "" || publicURL.User != nil || publicURL.RawQuery != "" || publicURL.Fragment != "" || (publicURL.Scheme != "http" && publicURL.Scheme != "https") {
		return nil, errors.New("LOOM_PUBLIC_URL must be an absolute HTTP or HTTPS URL")
	}
	publicURL.Path = strings.TrimSuffix(publicURL.Path, "/")
	if publicURL.Scheme == "http" && !isLoopbackHost(publicURL.Hostname()) {
		return nil, errors.New("LOOM_PUBLIC_URL must use HTTPS outside local development")
	}
	oauthKey := []byte(config.OAuthStateKey)
	if len(oauthKey) > 0 && len(oauthKey) < 32 {
		return nil, errors.New("LOOM_AUTH_STATE_KEY must contain at least 32 characters")
	}
	if len(oauthKey) == 0 {
		generated, randomErr := randomSecret(32)
		if randomErr != nil {
			return nil, errors.New("authentication randomness unavailable")
		}
		oauthKey = []byte(generated)
	}
	providers := make(map[string]AuthProvider, len(config.Providers))
	for _, provider := range config.Providers {
		if provider == nil || strings.TrimSpace(provider.ID()) == "" || strings.TrimSpace(provider.Name()) == "" {
			return nil, errors.New("invalid authentication provider")
		}
		id := strings.ToLower(strings.TrimSpace(provider.ID()))
		if id != provider.ID() || strings.ContainsAny(id, "/?\\") {
			return nil, errors.New("invalid authentication provider id")
		}
		if _, exists := providers[id]; exists {
			return nil, fmt.Errorf("duplicate authentication provider %q", id)
		}
		providers[id] = provider
	}
	return &Authenticator{store: store, legacyToken: config.LegacyToken, organization: config.Organization, adminEmail: adminEmail, publicURL: publicURL.String(), loopbackPublicURL: isLoopbackHost(publicURL.Hostname()), disableEmailRegistration: config.DisableEmailRegistration, providers: providers, secureCookies: publicURL.Scheme == "https", oauthKey: oauthKey, limiter: struct {
		mu      sync.Mutex
		entries map[string]loginWindow
	}{entries: map[string]loginWindow{}}}, nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (a *Authenticator) Bootstrap(ctx context.Context, password string) error {
	if password == "" {
		if a.loopbackPublicURL {
			password = DefaultAdminPassword
		} else {
			password = a.legacyToken
		}
	}
	if password == "" {
		return errors.New("LOOM_ADMIN_PASSWORD is required outside local development")
	}
	if err := validatePassword(password); err != nil {
		return errors.New("LOOM_ADMIN_PASSWORD must contain at least 12 characters")
	}
	return a.store.BootstrapAdmin(ctx, a.adminEmail, password)
}

func (a *Authenticator) legacyAuthorized(r *http.Request) bool {
	value := r.Header.Get("Authorization")
	if a.legacyToken == "" || !strings.HasPrefix(value, "Bearer ") || len(value) != len("Bearer ")+len(a.legacyToken) {
		return false
	}
	return subtleConstantTimeEqual([]byte(value), []byte("Bearer "+a.legacyToken))
}

func (a *Authenticator) session(r *http.Request) (authSessionRecord, bool) {
	cookie, err := r.Cookie(authSessionCookie)
	if err != nil || cookie.Value == "" {
		return authSessionRecord{}, false
	}
	result, err := a.store.authSession(r.Context(), cookie.Value, time.Now())
	return result, err == nil
}

func (a *Authenticator) csrfValid(r *http.Request, record authSessionRecord) bool {
	cookie, err := r.Cookie(csrfCookie)
	if err != nil || cookie.Value == "" {
		return false
	}
	header := r.Header.Get("X-Loom-CSRF")
	return header != "" && subtleConstantTimeEqual([]byte(secretDigest(cookie.Value)), []byte(record.CSRFHash)) && subtleConstantTimeEqual([]byte(header), []byte(cookie.Value))
}

func (a *Authenticator) Authorized(r *http.Request) bool {
	if a.legacyAuthorized(r) {
		return true
	}
	record, ok := a.session(r)
	if !ok {
		return false
	}
	if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
		return true
	}
	return a.csrfValid(r, record)
}

// AdminAuthorized is the administrator policy used for control-plane writes
// and plugin credential/setup operations. Signed-in members can still inspect
// the read model, but cannot mutate workflows, Goals, workers or integrations.
func (a *Authenticator) AdminAuthorized(r *http.Request) bool {
	if !a.Authorized(r) {
		return false
	}
	principal, ok := a.Principal(r)
	return ok && principal.Role == "admin"
}

func (a *Authenticator) Principal(r *http.Request) (AuthUser, bool) {
	if a.legacyAuthorized(r) {
		return AuthUser{Email: a.adminEmail, Organization: a.organization, DisplayName: "Administrator", Role: "admin"}, true
	}
	record, ok := a.session(r)
	if !ok {
		return AuthUser{}, false
	}
	return record.User, true
}

func (a *Authenticator) setSessionCookies(w http.ResponseWriter, token, csrf string) {
	http.SetCookie(w, &http.Cookie{Name: authSessionCookie, Value: token, Path: "/", MaxAge: int(sessionLifetime.Seconds()), HttpOnly: true, Secure: a.secureCookies, SameSite: http.SameSiteLaxMode})
	http.SetCookie(w, &http.Cookie{Name: csrfCookie, Value: csrf, Path: "/", MaxAge: int(sessionLifetime.Seconds()), HttpOnly: false, Secure: a.secureCookies, SameSite: http.SameSiteLaxMode})
}

func (a *Authenticator) clearSessionCookies(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: authSessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: a.secureCookies, SameSite: http.SameSiteLaxMode})
	http.SetCookie(w, &http.Cookie{Name: csrfCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: false, Secure: a.secureCookies, SameSite: http.SameSiteLaxMode})
}

func (a *Authenticator) ensureCSRF(w http.ResponseWriter, r *http.Request) string {
	if cookie, err := r.Cookie(csrfCookie); err == nil && len(cookie.Value) >= 32 {
		return cookie.Value
	}
	csrf, err := randomSecret(32)
	if err != nil {
		return ""
	}
	http.SetCookie(w, &http.Cookie{Name: csrfCookie, Value: csrf, Path: "/", MaxAge: int(sessionLifetime.Seconds()), HttpOnly: false, Secure: a.secureCookies, SameSite: http.SameSiteLaxMode})
	return csrf
}

func (a *Authenticator) allowLogin(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	now := time.Now()
	a.limiter.mu.Lock()
	defer a.limiter.mu.Unlock()
	for key, entry := range a.limiter.entries {
		if !entry.started.IsZero() && now.Sub(entry.started) >= time.Minute {
			delete(a.limiter.entries, key)
		}
	}
	entry := a.limiter.entries[host]
	if entry.started.IsZero() || now.Sub(entry.started) >= time.Minute {
		entry = loginWindow{started: now}
	}
	entry.count++
	a.limiter.entries[host] = entry
	return entry.count <= 12
}

type authCredentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type authUserResponse struct {
	User AuthUser `json:"user"`
}

func (a *Authenticator) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v1/auth/config":
		a.config(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/auth/session":
		a.currentSession(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/auth/login":
		a.login(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/auth/register":
		a.register(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/auth/logout":
		a.logout(w, r)
	case r.Method == http.MethodGet:
		if providerID, ok := authProviderRoute(r.URL.Path, "start"); ok {
			a.socialStart(w, r, providerID)
			return
		}
		if providerID, ok := authProviderRoute(r.URL.Path, "callback"); ok {
			a.socialCallback(w, r, providerID)
			return
		}
		http.NotFound(w, r)
	default:
		http.NotFound(w, r)
	}
}

func authProviderRoute(path, action string) (string, bool) {
	const prefix = "/v1/auth/"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, "/"+action) {
		return "", false
	}
	id := strings.TrimSuffix(strings.TrimPrefix(path, prefix), "/"+action)
	if id == "" || strings.Contains(id, "/") {
		return "", false
	}
	return id, true
}

func (a *Authenticator) config(w http.ResponseWriter, r *http.Request) {
	csrf := a.ensureCSRF(w, r)
	if csrf == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"detail": "Authentication is temporarily unavailable"})
		return
	}
	providers := make([]AuthProviderInfo, 0, len(a.providers))
	for id, provider := range a.providers {
		if provider.Enabled() {
			providers = append(providers, AuthProviderInfo{ID: id, Name: provider.Name()})
		}
	}
	sort.Slice(providers, func(i, j int) bool { return providers[i].ID < providers[j].ID })
	writeJSON(w, http.StatusOK, map[string]any{"email_registration_enabled": !a.disableEmailRegistration, "bootstrap_email": a.adminEmail, "social_providers": providers})
}

func (a *Authenticator) currentSession(w http.ResponseWriter, r *http.Request) {
	user, ok := a.Principal(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"detail": "Sign in required"})
		return
	}
	writeJSON(w, http.StatusOK, authUserResponse{User: user})
}

func (a *Authenticator) login(w http.ResponseWriter, r *http.Request) {
	if !a.csrfHeaderMatches(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"detail": "Invalid request origin"})
		return
	}
	if !a.allowLogin(r) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"detail": "Too many sign-in attempts; try again shortly"})
		return
	}
	var request authCredentials
	if !decodeJSON(w, r, &request) {
		return
	}
	user, err := a.store.SignInPassword(r.Context(), request.Email, request.Password)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"detail": "Invalid email or password"})
		return
	}
	token, csrf, err := a.store.CreateAuthSession(r.Context(), user, time.Now())
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"detail": "Could not create a session"})
		return
	}
	a.setSessionCookies(w, token, csrf)
	writeJSON(w, http.StatusOK, authUserResponse{User: user})
}

func (a *Authenticator) register(w http.ResponseWriter, r *http.Request) {
	if a.disableEmailRegistration {
		writeJSON(w, http.StatusNotFound, map[string]string{"detail": "Email registration is disabled"})
		return
	}
	if !a.csrfHeaderMatches(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"detail": "Invalid request origin"})
		return
	}
	if !a.allowLogin(r) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"detail": "Too many account attempts; try again shortly"})
		return
	}
	var request authCredentials
	if !decodeJSON(w, r, &request) {
		return
	}
	user, err := a.store.RegisterUser(r.Context(), request.Email, request.Password)
	if err != nil {
		writeFailure(w, err)
		return
	}
	token, csrf, err := a.store.CreateAuthSession(r.Context(), user, time.Now())
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"detail": "Could not create a session"})
		return
	}
	a.setSessionCookies(w, token, csrf)
	writeJSON(w, http.StatusCreated, authUserResponse{User: user})
}

func (a *Authenticator) csrfHeaderMatches(r *http.Request) bool {
	cookie, err := r.Cookie(csrfCookie)
	if err != nil || cookie.Value == "" {
		return false
	}
	header := r.Header.Get("X-Loom-CSRF")
	return header != "" && subtleConstantTimeEqual([]byte(header), []byte(cookie.Value))
}

func (a *Authenticator) logout(w http.ResponseWriter, r *http.Request) {
	if !a.legacyAuthorized(r) {
		if !a.Authorized(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"detail": "Sign in required"})
			return
		}
		if cookie, err := r.Cookie(authSessionCookie); err == nil {
			_ = a.store.RevokeAuthSession(r.Context(), cookie.Value)
		}
	}
	a.clearSessionCookies(w)
	w.WriteHeader(http.StatusNoContent)
}

type oauthState struct {
	Provider  string `json:"provider"`
	State     string `json:"state"`
	Verifier  string `json:"verifier"`
	Nonce     string `json:"nonce"`
	ExpiresAt int64  `json:"expires_at"`
}

func (a *Authenticator) sealOAuthState(state oauthState) (string, error) {
	payload, err := json.Marshal(state)
	if err != nil {
		return "", err
	}
	body := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, a.oauthKey)
	_, _ = mac.Write([]byte(body))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return body + "." + signature, nil
}

func (a *Authenticator) verifyOAuthState(value string, now time.Time) (oauthState, bool) {
	parts := strings.Split(value, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return oauthState{}, false
	}
	mac := hmac.New(sha256.New, a.oauthKey)
	_, _ = mac.Write([]byte(parts[0]))
	expected, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !hmac.Equal(expected, mac.Sum(nil)) {
		return oauthState{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return oauthState{}, false
	}
	var state oauthState
	if err := json.Unmarshal(payload, &state); err != nil || state.Provider == "" || state.State == "" || state.Verifier == "" || state.Nonce == "" || state.ExpiresAt <= now.Unix() {
		return oauthState{}, false
	}
	return state, true
}

func (a *Authenticator) socialStart(w http.ResponseWriter, r *http.Request, providerID string) {
	if !a.allowLogin(r) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"detail": "Too many sign-in attempts; try again shortly"})
		return
	}
	provider, ok := a.providers[providerID]
	if !ok || !provider.Enabled() {
		writeJSON(w, http.StatusNotFound, map[string]string{"detail": "This sign-in provider is not configured"})
		return
	}
	state, err := randomSecret(32)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"detail": "Could not start sign-in"})
		return
	}
	verifier, err := randomSecret(32)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"detail": "Could not start sign-in"})
		return
	}
	nonce, err := randomSecret(32)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"detail": "Could not start sign-in"})
		return
	}
	challengeDigest := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(challengeDigest[:])
	expiresAt := time.Now().Add(oauthLifetime)
	encoded, err := a.sealOAuthState(oauthState{Provider: providerID, State: state, Verifier: verifier, Nonce: nonce, ExpiresAt: expiresAt.Unix()})
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"detail": "Could not start sign-in"})
		return
	}
	if err := a.store.CreateOAuthState(r.Context(), state, expiresAt); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"detail": "Could not start sign-in"})
		return
	}
	http.SetCookie(w, &http.Cookie{Name: oauthCookie, Value: encoded, Path: "/v1/auth", MaxAge: int(oauthLifetime.Seconds()), HttpOnly: true, Secure: a.secureCookies, SameSite: http.SameSiteLaxMode})
	callback := strings.TrimSuffix(a.publicURL, "/") + "/v1/auth/" + providerID + "/callback"
	location, err := provider.AuthorizationURL(callback, state, challenge, nonce)
	if err != nil {
		a.clearOAuthCookie(w)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"detail": "Could not start sign-in"})
		return
	}
	http.Redirect(w, r, location, http.StatusFound)
}

func (a *Authenticator) socialCallback(w http.ResponseWriter, r *http.Request, providerID string) {
	provider, ok := a.providers[providerID]
	if !ok || !provider.Enabled() {
		a.socialError(w, r)
		return
	}
	now := time.Now()
	cookie, cookieErr := r.Cookie(oauthCookie)
	if r.URL.Query().Get("error") != "" {
		if cookieErr == nil {
			if saved, valid := a.verifyOAuthState(cookie.Value, now); valid && saved.Provider == providerID {
				_ = a.store.ConsumeOAuthState(r.Context(), saved.State, now)
			}
		}
		a.clearOAuthCookie(w)
		a.socialError(w, r)
		return
	}
	if cookieErr != nil {
		a.socialError(w, r)
		return
	}
	saved, ok := a.verifyOAuthState(cookie.Value, now)
	if !ok {
		a.clearOAuthCookie(w)
		a.socialError(w, r)
		return
	}
	state := r.URL.Query().Get("state")
	if saved.Provider != providerID || saved.State == "" || saved.Verifier == "" || saved.Nonce == "" || state == "" || !subtleConstantTimeEqual([]byte(saved.State), []byte(state)) {
		a.clearOAuthCookie(w)
		a.socialError(w, r)
		return
	}
	if err := a.store.ConsumeOAuthState(r.Context(), saved.State, now); err != nil {
		a.clearOAuthCookie(w)
		a.socialError(w, r)
		return
	}
	a.clearOAuthCookie(w)
	code := r.URL.Query().Get("code")
	if code == "" {
		a.socialError(w, r)
		return
	}
	callback := strings.TrimSuffix(a.publicURL, "/") + "/v1/auth/" + providerID + "/callback"
	identity, err := provider.Authenticate(r.Context(), code, saved.Verifier, callback, saved.Nonce)
	if err != nil {
		a.socialError(w, r)
		return
	}
	if identity.Provider == "" {
		identity.Provider = providerID
	}
	if identity.Provider != providerID {
		a.socialError(w, r)
		return
	}
	user, err := a.store.SignInExternal(r.Context(), identity)
	if err != nil {
		a.socialError(w, r)
		return
	}
	sessionToken, csrf, err := a.store.CreateAuthSession(r.Context(), user, time.Now())
	if err != nil {
		a.socialError(w, r)
		return
	}
	a.setSessionCookies(w, sessionToken, csrf)
	http.Redirect(w, r, strings.TrimSuffix(a.publicURL, "/")+"/", http.StatusFound)
}

func (a *Authenticator) clearOAuthCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: oauthCookie, Value: "", Path: "/v1/auth", MaxAge: -1, HttpOnly: true, Secure: a.secureCookies, SameSite: http.SameSiteLaxMode})
}

func (a *Authenticator) socialError(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, strings.TrimSuffix(a.publicURL, "/")+"/?auth_error=social_failed", http.StatusFound)
}
