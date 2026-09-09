package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
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
func handlerForTest(data reader) http.Handler {
	return newHandler(data, testToken, http.NotFoundHandler(), fstest.MapFS{"index.html": {Data: []byte("<!doctype html><title>Loom</title>")}, "assets/app.js": {Data: []byte("console.log('loom')")}})
}

func TestRoutesAndAuthentication(t *testing.T) {
	h := handlerForTest(fakeReader{})
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
			r := httptest.NewRequest(http.MethodGet, tc.path, nil)
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
	h := handlerForTest(fakeReader{})
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
		r := httptest.NewRequest(http.MethodGet, tc.path, nil)
		r.Header.Set("Accept", tc.accept)
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
		h := handlerForTest(fakeReader{tc.err})
		r := httptest.NewRequest(http.MethodGet, "/v1/goals/"+goalID, nil)
		r.Header.Set("Authorization", "Bearer "+testToken)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status || strings.Contains(w.Body.String(), "secret") {
			t.Fatalf("unsafe response: %s", w.Body.String())
		}
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
	h := newHandler(fakeReader{}, testToken, http.NotFoundHandler(), fstest.MapFS{})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != 503 {
		t.Fatal(w.Code)
	}
}

func TestReadinessChecksDatabase(t *testing.T) {
	for _, tc := range []struct {
		data   reader
		token  string
		status int
	}{
		{fakeReader{}, "", 401}, {fakeReader{errors.New("database unavailable")}, testToken, 503}, {fakeReader{}, testToken, 200},
	} {
		r := httptest.NewRequest(http.MethodGet, "/readyz", nil)
		r.Header.Set("Authorization", "Bearer "+tc.token)
		w := httptest.NewRecorder()
		handlerForTest(tc.data).ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("status %d expected %d", w.Code, tc.status)
		}
	}
}

func TestHealthcheckCommand(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/readyz" || r.Header.Get("Authorization") != "Bearer "+testToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer up.Close()
	_, port, _ := net.SplitHostPort(up.Listener.Addr().String())
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
