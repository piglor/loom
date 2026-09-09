package control

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

const maxRequestBody = 1 << 20

// NewAPIHandler exposes Loom's native mutation and outbound-worker protocol.
// Integration-specific authentication stays in adapters layered above this
// generic control-plane seam.
func NewAPIHandler(store *Store, adminToken string) http.Handler {
	mux := http.NewServeMux()
	admin := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if !bearerMatches(r, adminToken) {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"detail": "Invalid authentication"})
				return
			}
			next(w, r)
		}
	}
	workerToken := func(r *http.Request) (string, bool) {
		value := r.Header.Get("Authorization")
		if !strings.HasPrefix(value, "Bearer ") || len(value) == len("Bearer ") {
			return "", false
		}
		return strings.TrimPrefix(value, "Bearer "), true
	}
	call := func(w http.ResponseWriter, r *http.Request, status int, action func() (any, error)) {
		result, err := action()
		if err != nil {
			writeFailure(w, err)
			return
		}
		writeJSON(w, status, result)
	}

	mux.HandleFunc("POST /v1/goals", admin(func(w http.ResponseWriter, r *http.Request) {
		var request CreateGoal
		if !decodeJSON(w, r, &request) {
			return
		}
		call(w, r, http.StatusCreated, func() (any, error) {
			id, err := store.Create(r.Context(), request)
			return map[string]string{"id": id}, err
		})
	}))
	mux.HandleFunc("POST /v1/events", admin(func(w http.ResponseWriter, r *http.Request) {
		var request Event
		if !decodeJSON(w, r, &request) {
			return
		}
		call(w, r, http.StatusOK, func() (any, error) { return store.Receive(r.Context(), request) })
	}))
	mux.HandleFunc("POST /v1/goals/{id}/cancel", admin(func(w http.ResponseWriter, r *http.Request) {
		call(w, r, http.StatusOK, func() (any, error) {
			err := store.Cancel(r.Context(), r.PathValue("id"))
			return map[string]string{"status": "cancelled"}, err
		})
	}))
	mux.HandleFunc("POST /v1/workers", admin(func(w http.ResponseWriter, r *http.Request) {
		var request Enrollment
		if !decodeJSON(w, r, &request) {
			return
		}
		call(w, r, http.StatusCreated, func() (any, error) { return store.Enroll(r.Context(), request) })
	}))
	mux.HandleFunc("POST /v1/workers/{id}/revoke", admin(func(w http.ResponseWriter, r *http.Request) {
		call(w, r, http.StatusOK, func() (any, error) {
			err := store.RevokeWorker(r.Context(), r.PathValue("id"))
			return map[string]string{"status": "revoked"}, err
		})
	}))
	mux.HandleFunc("GET /v1/worker/commands", func(w http.ResponseWriter, r *http.Request) {
		token, ok := workerToken(r)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"detail": "Invalid worker authentication"})
			return
		}
		call(w, r, http.StatusOK, func() (any, error) { return store.Poll(r.Context(), token) })
	})
	mux.HandleFunc("POST /v1/worker/commands/{id}/claim", func(w http.ResponseWriter, r *http.Request) {
		token, ok := workerToken(r)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"detail": "Invalid worker authentication"})
			return
		}
		var request Claim
		if !decodeJSON(w, r, &request) {
			return
		}
		call(w, r, http.StatusOK, func() (any, error) { return store.Claim(r.Context(), token, r.PathValue("id"), request) })
	})
	mux.HandleFunc("POST /v1/worker/commands/{id}/stop", func(w http.ResponseWriter, r *http.Request) {
		token, ok := workerToken(r)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"detail": "Invalid worker authentication"})
			return
		}
		var request Stop
		if !decodeJSON(w, r, &request) {
			return
		}
		call(w, r, http.StatusOK, func() (any, error) { return store.Report(r.Context(), token, r.PathValue("id"), request) })
	})
	mux.HandleFunc("POST /v1/worker/commands/{id}/session", func(w http.ResponseWriter, r *http.Request) {
		token, ok := workerToken(r)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"detail": "Invalid worker authentication"})
			return
		}
		var request BindSession
		if !decodeJSON(w, r, &request) {
			return
		}
		call(w, r, http.StatusOK, func() (any, error) { return store.BindSession(r.Context(), token, r.PathValue("id"), request) })
	})
	mux.HandleFunc("POST /v1/worker/commands/{id}/wait", func(w http.ResponseWriter, r *http.Request) {
		token, ok := workerToken(r)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"detail": "Invalid worker authentication"})
			return
		}
		var request PrepareWait
		if !decodeJSON(w, r, &request) {
			return
		}
		call(w, r, http.StatusOK, func() (any, error) { return store.Prepare(r.Context(), token, r.PathValue("id"), request) })
	})
	return withRequestTimeout(mux)
}

func bearerMatches(r *http.Request, expected string) bool {
	value := r.Header.Get("Authorization")
	return len(expected) >= 32 && subtle.ConstantTimeCompare([]byte(value), []byte("Bearer "+expected)) == 1
}

func decodeJSON(w http.ResponseWriter, r *http.Request, destination any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"detail": "Request body too large"})
		} else {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"detail": "Invalid request"})
		}
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"detail": "Invalid request"})
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeFailure(w http.ResponseWriter, err error) {
	var fault *Fault
	if errors.As(err, &fault) {
		message := fault.Message
		if fault.Status == http.StatusUnauthorized {
			message = "Invalid worker authentication"
		}
		writeJSON(w, fault.Status, map[string]string{"detail": message})
		return
	}
	slog.Warn("control_plane_request_failed", "error_type", "internal")
	writeJSON(w, http.StatusServiceUnavailable, map[string]string{"detail": "Control plane temporarily unavailable"})
}

func withRequestTimeout(next http.Handler) http.Handler {
	return http.TimeoutHandler(next, 30*time.Second, `{"detail":"Request timed out"}`)
}
