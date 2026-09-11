package secrets

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"
)

const maxResponse = 1 << 20

var referencePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9/_-]{0,240}$`)

type OpenBaoConfig struct {
	Address       string
	Mount         string
	RoleID        string
	SecretID      string
	Token         string
	CACertificate string
}

type OpenBao struct {
	base     *url.URL
	mount    string
	roleID   string
	secretID string
	static   string
	client   *http.Client

	mu          sync.Mutex
	token       string
	tokenExpiry time.Time
}

func OpenBaoFromEnvironment() (*OpenBao, error) {
	secretID, err := readCredential("LOOM_OPENBAO_SECRET_ID", "LOOM_OPENBAO_SECRET_ID_FILE")
	if err != nil {
		return nil, err
	}
	token, err := readCredential("LOOM_OPENBAO_TOKEN", "LOOM_OPENBAO_TOKEN_FILE")
	if err != nil {
		return nil, err
	}
	return NewOpenBao(OpenBaoConfig{
		Address: os.Getenv("LOOM_OPENBAO_ADDR"), RoleID: os.Getenv("LOOM_OPENBAO_ROLE_ID"),
		SecretID: secretID, Token: token, Mount: os.Getenv("LOOM_OPENBAO_MOUNT"),
		CACertificate: os.Getenv("LOOM_OPENBAO_CA_CERT"),
	})
}

func readCredential(valueName, fileName string) (string, error) {
	file := os.Getenv(fileName)
	if file == "" {
		return strings.TrimSpace(os.Getenv(valueName)), nil
	}
	body, err := os.ReadFile(file)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", fileName, err)
	}
	return strings.TrimSpace(string(body)), nil
}

func NewOpenBao(config OpenBaoConfig) (*OpenBao, error) {
	if config.Address == "" {
		return &OpenBao{}, nil
	}
	base, err := url.Parse(config.Address)
	if err != nil || (base.Scheme != "https" && base.Scheme != "http") || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return nil, errors.New("invalid OpenBao address")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if config.CACertificate != "" {
		body, readErr := os.ReadFile(config.CACertificate)
		if readErr != nil {
			return nil, errors.New("invalid OpenBao CA certificate")
		}
		roots, rootErr := x509.SystemCertPool()
		if rootErr != nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM(body) {
			return nil, errors.New("invalid OpenBao CA certificate")
		}
		transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}
	}
	mount := strings.Trim(config.Mount, "/")
	if mount == "" {
		mount = "loom"
	}
	if !referencePattern.MatchString(mount) || strings.Contains(mount, "/") {
		return nil, errors.New("invalid OpenBao mount")
	}
	base.Path = strings.TrimSuffix(base.Path, "/")
	return &OpenBao{base: base, mount: mount, roleID: config.RoleID, secretID: config.SecretID, static: config.Token, client: &http.Client{Transport: transport, Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (o *OpenBao) configured() bool { return o.base != nil }

func (o *OpenBao) endpoint(parts ...string) string {
	u := *o.base
	u.Path = strings.TrimSuffix(u.Path, "/") + "/v1/" + path.Join(parts...)
	return u.String()
}

func (o *OpenBao) Status(ctx context.Context) Status {
	if !o.configured() {
		return StatusUnconfigured
	}
	// The bundled OpenBao starts before an operator has entered its AppRole
	// credentials. Keep the process healthy and let the plugin store explain
	// the missing setup instead of failing the whole server at startup.
	if o.static == "" && (o.roleID == "" || o.secretID == "") {
		return StatusNeedsCredentials
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.endpoint("sys", "health"), nil)
	if err != nil {
		return StatusUnavailable
	}
	response, err := o.client.Do(req)
	if err != nil {
		return StatusUnavailable
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusOK, http.StatusTooManyRequests:
		return StatusReady
	case http.StatusServiceUnavailable:
		return StatusSealed
	default:
		return StatusUnavailable
	}
}

func (o *OpenBao) authenticate(ctx context.Context, force bool) (string, error) {
	if !o.configured() {
		return "", ErrNotConfigured
	}
	if o.static != "" {
		return o.static, nil
	}
	if o.roleID == "" || o.secretID == "" {
		return "", ErrNotConfigured
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if !force && o.token != "" && time.Now().Before(o.tokenExpiry.Add(-30*time.Second)) {
		return o.token, nil
	}
	body, _ := json.Marshal(map[string]string{"role_id": o.roleID, "secret_id": o.secretID})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.endpoint("auth", "approle", "login"), bytes.NewReader(body))
	if err != nil {
		return "", ErrUnavailable
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := o.client.Do(req)
	if err != nil {
		return "", ErrUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", ErrUnavailable
	}
	var result struct {
		Auth struct {
			ClientToken   string `json:"client_token"`
			LeaseDuration int    `json:"lease_duration"`
		} `json:"auth"`
	}
	if decodeLimited(response.Body, &result) != nil || result.Auth.ClientToken == "" {
		return "", ErrUnavailable
	}
	o.token = result.Auth.ClientToken
	o.tokenExpiry = time.Now().Add(time.Duration(result.Auth.LeaseDuration) * time.Second)
	return o.token, nil
}

func (o *OpenBao) request(ctx context.Context, method, endpoint string, body any, destination any) (*http.Response, error) {
	var encoded []byte
	var err error
	if body != nil {
		encoded, err = json.Marshal(body)
		if err != nil {
			return nil, err
		}
	}
	for attempt := 0; attempt < 2; attempt++ {
		token, authErr := o.authenticate(ctx, attempt == 1)
		if authErr != nil {
			return nil, authErr
		}
		req, requestErr := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(encoded))
		if requestErr != nil {
			return nil, requestErr
		}
		req.Header.Set("X-Vault-Token", token)
		req.Header.Set("Content-Type", "application/json")
		response, requestErr := o.client.Do(req)
		if requestErr != nil {
			return nil, ErrUnavailable
		}
		if response.StatusCode == http.StatusForbidden && attempt == 0 && o.static == "" {
			response.Body.Close()
			continue
		}
		if destination != nil && response.StatusCode >= 200 && response.StatusCode < 300 {
			if decodeLimited(response.Body, destination) != nil {
				response.Body.Close()
				return nil, ErrUnavailable
			}
		}
		return response, nil
	}
	return nil, ErrUnavailable
}

func decodeLimited(reader io.Reader, destination any) error {
	limited := io.LimitReader(reader, maxResponse+1)
	body, err := io.ReadAll(limited)
	if err != nil || len(body) > maxResponse {
		return ErrUnavailable
	}
	return json.Unmarshal(body, destination)
}

func validReference(reference string) bool {
	return referencePattern.MatchString(reference) && !strings.Contains(reference, "//") && !strings.Contains(reference, "..")
}

func (o *OpenBao) Put(ctx context.Context, reference string, values map[string]string) (int, error) {
	if !validReference(reference) || len(values) == 0 {
		return 0, errors.New("invalid secret reference or value")
	}
	for _, value := range values {
		if value == "" {
			return 0, errors.New("secret values cannot be empty")
		}
	}
	var result struct {
		Data struct {
			Version int `json:"version"`
		} `json:"data"`
	}
	response, err := o.request(ctx, http.MethodPost, o.endpoint(o.mount, "data", reference), map[string]any{"data": values}, &result)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 || result.Data.Version < 1 {
		return 0, ErrUnavailable
	}
	return result.Data.Version, nil
}

func (o *OpenBao) Get(ctx context.Context, reference string) (map[string]string, int, error) {
	if !validReference(reference) {
		return nil, 0, errors.New("invalid secret reference")
	}
	var result struct {
		Data struct {
			Data     map[string]string `json:"data"`
			Metadata struct {
				Version int `json:"version"`
			} `json:"metadata"`
		} `json:"data"`
	}
	response, err := o.request(ctx, http.MethodGet, o.endpoint(o.mount, "data", reference), nil, &result)
	if err != nil {
		return nil, 0, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return nil, 0, ErrNotFound
	}
	if response.StatusCode != http.StatusOK {
		return nil, 0, ErrUnavailable
	}
	return result.Data.Data, result.Data.Metadata.Version, nil
}

func (o *OpenBao) Delete(ctx context.Context, reference string) error {
	if !validReference(reference) {
		return errors.New("invalid secret reference")
	}
	// Delete the current version rather than the metadata key. Metadata DELETE
	// permanently destroys every version and is reserved for operators; the
	// AppRole policy intentionally cannot perform that irreversible action.
	response, err := o.request(ctx, http.MethodDelete, o.endpoint(o.mount, "data", reference), nil, nil)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return ErrUnavailable
	}
	return nil
}
