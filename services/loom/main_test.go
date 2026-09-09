package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"

	"github.com/jackc/pgx/v5"
)

const testToken = "operator-test-token-with-at-least-32-characters"
const goalID = "a2222222-2222-4222-8222-222222222222"

type fakeReader struct{ err error }

func (f fakeReader) ready(context.Context) error { return f.err }

func (f fakeReader) list(context.Context) ([]json.RawMessage, error) {
	return []json.RawMessage{json.RawMessage(`{"id":"` + goalID + `","title":"Test"}`)}, f.err
}
func (f fakeReader) inspect(context.Context, string) (json.RawMessage, error) {
	return json.RawMessage(`{"id":"` + goalID + `"}`), f.err
}
func handlerForTest(data reader, upstream string) http.Handler {
	u, _ := url.Parse(upstream)
	return newHandler(data, testToken, u, fstest.MapFS{"index.html": {Data: []byte("<!doctype html><title>Loom</title>")}, "assets/app.js": {Data: []byte("console.log('loom')")}})
}
func TestRoutesAndAuthentication(t *testing.T) {
	h := handlerForTest(fakeReader{}, "http://127.0.0.1:1")
	for _, tc := range []struct {
		path, token string
		status      int
	}{
		{"/", "", 200}, {"/goals/" + goalID, "", 200}, {"/needs-you", "", 200},
		{"/assets/app.js", "", 200}, {"/assets/missing.js", "", 404}, {"/.env", "", 404}, {"/main.go", "", 404},
		{"/healthz", "", 200}, {"/v1/goals", "", 401}, {"/v1/goals", "bad", 401},
		{"/v1/goals", testToken, 200}, {"/v1/goals/" + goalID, "", 401},
		{"/v1/goals/" + goalID, testToken, 200}, {"/v1/goals/not-a-uuid", testToken, 422},
	} {
		t.Run(tc.path+tc.token[:min(3, len(tc.token))], func(t *testing.T) {
			r := httptest.NewRequest("GET", tc.path, nil)
			if tc.token != "" {
				r.Header.Set("Authorization", "Bearer "+tc.token)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
				t.Fatal("missing security headers")
			}
			if strings.Contains(w.Body.String(), testToken) {
				t.Fatal("credential leaked")
			}
		})
	}
}
func TestUnknownNavigationAndResourceErrors(t *testing.T) {
	upstream := httptest.NewServer(http.NotFoundHandler())
	defer upstream.Close()
	h := handlerForTest(fakeReader{}, upstream.URL)
	for _, tc := range []struct {
		path, accept string
		html         bool
	}{
		{"/not-a-loom-page", "text/html", true},
		{"/not-a-loom-page", "application/json", false},
		{"/assets/missing.js", "text/html", false},
		{"/.env", "text/html", false},
		{"/main.go", "text/html", false},
		{"/v1/not-a-route", "text/html", false},
	} {
		r := httptest.NewRequest("GET", tc.path, nil)
		r.Header.Set("Accept", tc.accept)
		r.Header.Set("Authorization", "Bearer "+testToken)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 404 || strings.Contains(w.Body.String(), "<!doctype html>") != tc.html {
			t.Errorf("path=%s accept=%s status=%d html=%v", tc.path, tc.accept, w.Code, tc.html)
		}
	}
}
func TestReadFailures(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
	}{{pgx.ErrNoRows, 404}, {errors.New("password=secret internal hostname"), 503}} {
		h := handlerForTest(fakeReader{tc.err}, "http://127.0.0.1:1")
		r := httptest.NewRequest("GET", "/v1/goals/"+goalID, nil)
		r.Header.Set("Authorization", "Bearer "+testToken)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status || strings.Contains(w.Body.String(), "secret") {
			t.Fatalf("unsafe response: %s", w.Body.String())
		}
	}
}
func TestProxyPreservesAuthorityAndDoesNotRetry(t *testing.T) {
	var calls atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "POST" || r.URL.Path != "/v1/events" || r.Header.Get("Authorization") != "Bearer worker-token" {
			t.Error("changed API request")
		}
		if r.Header.Get("X-Forwarded-For") != "" {
			t.Error("trusted client supplied forwarding header")
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != `{"source":"deployment"}` {
			t.Error("changed request body")
		}
		w.WriteHeader(409)
		_, _ = w.Write([]byte(`{"detail":"conflict"}`))
	}))
	defer up.Close()
	h := handlerForTest(fakeReader{}, up.URL)
	r := httptest.NewRequest("POST", "/v1/events", strings.NewReader(`{"source":"deployment"}`))
	r.Header.Set("Authorization", "Bearer worker-token")
	r.Header.Set("X-Forwarded-For", "privileged-host")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if calls.Load() != 1 || w.Code != 409 {
		t.Fatalf("calls=%d status=%d", calls.Load(), w.Code)
	}
}
func TestProxyUnavailable(t *testing.T) {
	h := handlerForTest(fakeReader{}, "http://127.0.0.1:1")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/v1/events", nil))
	if w.Code != 502 || strings.Contains(w.Body.String(), "127.0.0.1") {
		t.Fatal("unsafe unavailable response")
	}
}
func TestUUIDValidation(t *testing.T) {
	for _, value := range []string{"", "..", strings.Repeat("a", 36), "a2222222-2222-4222-8222-22222222222g"} {
		if validUUID(value) {
			t.Errorf("accepted %q", value)
		}
	}
	if !validUUID(goalID) {
		t.Fatal("rejected UUID")
	}
}
func TestMissingAssets(t *testing.T) {
	u, _ := url.Parse("http://127.0.0.1:1")
	h := newHandler(fakeReader{}, testToken, u, fstest.MapFS{})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != 503 {
		t.Fatal(w.Code)
	}
}

func TestReadinessChecksBothDependencies(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer up.Close()
	for _, tc := range []struct {
		data   reader
		token  string
		status int
	}{
		{fakeReader{}, "", 401}, {fakeReader{errors.New("database unavailable")}, testToken, 503}, {fakeReader{}, testToken, 200},
	} {
		r := httptest.NewRequest("GET", "/readyz", nil)
		r.Header.Set("Authorization", "Bearer "+tc.token)
		w := httptest.NewRecorder()
		handlerForTest(tc.data, up.URL).ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("status %d expected %d", w.Code, tc.status)
		}
	}
}

func TestDroppedMutationIsNotRetried(t *testing.T) {
	var calls atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if !r.Close {
			t.Error("mutation must not use a pooled connection")
		}
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_ = conn.Close()
	}))
	defer up.Close()
	h := handlerForTest(fakeReader{}, up.URL)
	r := httptest.NewRequest("POST", "/v1/goals/"+goalID+"/cancel", nil)
	r.Header.Set("Idempotency-Key", "attempt-1")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if calls.Load() != 1 || w.Code != 502 {
		t.Fatalf("calls=%d status=%d", calls.Load(), w.Code)
	}
}

func TestWebhookBytesAndSignaturePreserved(t *testing.T) {
	body := strings.Repeat(" ", 2<<20) + `{"action":"completed"}`
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ := io.ReadAll(r.Body)
		if string(got) != body || r.Header.Get("X-Hub-Signature-256") != "sha256=signed" || r.Header.Get("X-Github-Delivery") != goalID {
			t.Error("changed signed envelope")
		}
		// Size/signature decisions belong to the authoritative ingress, not proxy.
		w.WriteHeader(413)
	}))
	defer up.Close()
	r := httptest.NewRequest("POST", "/v1/github/webhook", strings.NewReader(body))
	r.Header.Set("X-Hub-Signature-256", "sha256=signed")
	r.Header.Set("X-Github-Delivery", goalID)
	w := httptest.NewRecorder()
	handlerForTest(fakeReader{}, up.URL).ServeHTTP(w, r)
	if w.Code != 413 {
		t.Fatal(w.Code)
	}
}

func TestHealthcheckCommand(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/readyz" || r.Header.Get("Authorization") != "Bearer "+testToken {
			w.WriteHeader(401)
			return
		}
		w.WriteHeader(200)
	}))
	defer up.Close()
	u, _ := url.Parse(up.URL)
	_, port, _ := net.SplitHostPort(u.Host)
	t.Setenv("LOOM_LISTEN_ADDR", "0.0.0.0:"+port)
	t.Setenv("LOOM_API_TOKEN", testToken)
	if err := healthcheck(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOOM_API_TOKEN", "wrong")
	if healthcheck() == nil {
		t.Fatal("accepted failed readiness")
	}
	t.Setenv("LOOM_LISTEN_ADDR", "invalid")
	if healthcheck() == nil {
		t.Fatal("accepted bad address")
	}
}
