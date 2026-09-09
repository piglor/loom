// Command loom-server is the incremental Go API and console entry point.
package main

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/piglor/loom/services/loom/internal/control"
	githubintegration "github.com/piglor/loom/services/loom/internal/integrations/github"
	"github.com/piglor/loom/services/loom/internal/orchestration"
)

type reader interface {
	ready(context.Context) error
	list(context.Context) ([]json.RawMessage, error)
	inspect(context.Context, string) (json.RawMessage, error)
}

type postgresReader struct {
	pool         *pgxpool.Pool
	organization string
	store        *control.Store
}

func (p postgresReader) ready(ctx context.Context) error {
	var version int
	return p.pool.QueryRow(ctx, "SELECT version FROM schema_migrations WHERE version=13").Scan(&version)
}

func (p postgresReader) list(ctx context.Context) ([]json.RawMessage, error) {
	return p.store.ListGoals(ctx)
}

func (p postgresReader) inspect(ctx context.Context, id string) (json.RawMessage, error) {
	// One SQL statement gives the entire read model a consistent MVCC snapshot.
	var result json.RawMessage
	err := p.pool.QueryRow(ctx, `SELECT to_jsonb(g) || jsonb_build_object(
		'run',to_jsonb(r),'session',to_jsonb(s),'wait',to_jsonb(w),
		'wait_history',COALESCE((SELECT jsonb_agg(to_jsonb(h) ORDER BY h.generation) FROM wait_history h WHERE h.goal_id=g.id),'[]'::jsonb),
		'attempts',COALESCE((SELECT jsonb_agg(to_jsonb(a) ORDER BY a.phase) FROM attempts a WHERE a.goal_id=g.id),'[]'::jsonb),
		'audit',COALESCE((SELECT jsonb_agg(to_jsonb(a) ORDER BY a.sequence) FROM audit a WHERE a.goal_id=g.id),'[]'::jsonb),
		'metrics',jsonb_build_object(
		'lifetime_seconds',EXTRACT(EPOCH FROM(COALESCE(g.ended_at,statement_timestamp())-g.created_at)),
		'suspended_seconds',CASE WHEN w.armed_at IS NULL THEN 0 ELSE GREATEST(0,EXTRACT(EPOCH FROM(COALESCE(w.closed_at,statement_timestamp())-w.armed_at))) END + (SELECT COALESCE(SUM(EXTRACT(EPOCH FROM(h.closed_at-h.armed_at))),0) FROM wait_history h WHERE h.goal_id=g.id),
		'execution_seconds',(SELECT COALESCE(SUM(duration_ms),0)/1000 FROM attempts WHERE goal_id=g.id),
		'attempts',(SELECT count(*) FROM attempts WHERE goal_id=g.id),
		'wake_ups',(SELECT count(*) FROM attempts WHERE goal_id=g.id AND phase>0 AND state<>'QUEUED' AND outcome IS DISTINCT FROM 'cancelled_unclaimed'),
		'tokens',NULL,'provider_cost',NULL))
		FROM goals g JOIN runs r ON r.goal_id=g.id JOIN sessions s ON s.run_id=r.id
		JOIN waits w ON w.goal_id=g.id WHERE g.id=$1::uuid AND g.organization=$2`, id, p.organization).Scan(&result)
	return result, err
}

func jsonResponse(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func newHandler(data reader, token string, api http.Handler, assets fs.FS) http.Handler {
	mux := http.NewServeMux()
	auth := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
				jsonResponse(w, 401, map[string]string{"detail": "Invalid authentication"})
				return
			}
			next(w, r)
		}
	}
	failure := func(w http.ResponseWriter, err error) {
		if errors.Is(err, pgx.ErrNoRows) {
			jsonResponse(w, 404, map[string]string{"detail": "Resource not found"})
			return
		}
		slog.Warn("read_model_unavailable")
		jsonResponse(w, 503, map[string]string{"detail": "Goal data temporarily unavailable"})
	}
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { jsonResponse(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("GET /v1/goals", auth(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		result, err := data.list(ctx)
		if err != nil {
			failure(w, err)
			return
		}
		jsonResponse(w, 200, result)
	}))
	mux.HandleFunc("GET /v1/goals/{id}", auth(func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if !validUUID(id) {
			jsonResponse(w, 422, map[string]string{"detail": "Invalid Goal ID"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		result, err := data.inspect(ctx, id)
		if err != nil {
			failure(w, err)
			return
		}
		jsonResponse(w, 200, result)
	}))
	mux.Handle("/v1/", api)
	mux.HandleFunc("GET /readyz", auth(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := data.ready(ctx); err != nil {
			jsonResponse(w, http.StatusServiceUnavailable, map[string]string{"detail": "Read database is not ready"})
			return
		}
		jsonResponse(w, 200, map[string]string{"database": "ready"})
	}))
	serveConsole := func(w http.ResponseWriter, status int) {
		body, err := fs.ReadFile(assets, "index.html")
		if err != nil {
			http.Error(w, "Console assets unavailable", 503)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Path != "/" && r.URL.Path != "/goals" && !strings.HasPrefix(r.URL.Path, "/goals/") && r.URL.Path != "/needs-you" {
			if !strings.HasPrefix(r.URL.Path, "/assets/") {
				// Browser navigation gets the recovery UI with a real 404. API,
				// asset and file-like requests retain their resource error behavior.
				if strings.Contains(r.Header.Get("Accept"), "text/html") && path.Ext(r.URL.Path) == "" {
					serveConsole(w, http.StatusNotFound)
				} else {
					http.NotFound(w, r)
				}
				return
			}
			name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
			if !strings.HasPrefix(name, "assets/") {
				http.NotFound(w, r)
				return
			}
			if _, err := fs.Stat(assets, name); err != nil {
				http.NotFound(w, r)
				return
			}
			http.FileServer(http.FS(assets)).ServeHTTP(w, r)
			return
		}
		serveConsole(w, http.StatusOK)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		mux.ServeHTTP(w, r)
	})
}

func validUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i, c := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
			return false
		}
	}
	return true
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	operation := run
	if len(os.Args) > 2 {
		slog.Error("invalid_command")
		os.Exit(2)
	}
	if len(os.Args) == 2 {
		switch os.Args[1] {
		case "serve":
			operation = run
		case "migrate":
			operation = migrate
		case "worker":
			operation = worker
		case "healthcheck":
			operation = healthcheck
		default:
			slog.Error("invalid_command")
			os.Exit(2)
		}
	}
	if err := operation(); err != nil {
		slog.Error("server_stopped", "error", err)
		os.Exit(1)
	}
}

func database() (*sql.DB, error) {
	value := os.Getenv("LOOM_DATABASE_URL")
	if value == "" {
		return nil, errors.New("LOOM_DATABASE_URL is required")
	}
	config, err := pgx.ParseConfig(value)
	if err != nil {
		return nil, errors.New("Invalid database configuration")
	}
	db := stdlib.OpenDB(*config)
	db.SetMaxOpenConns(10)
	return db, nil
}

func migrate() error {
	db, err := database()
	if err != nil {
		return err
	}
	defer db.Close()
	org := os.Getenv("LOOM_ORGANIZATION")
	if org == "" {
		org = "local"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	return (&control.Store{DB: db, Organization: org, EnableCodex: os.Getenv("LOOM_ENABLE_CODEX_CONTAINER") == "true"}).Migrate(ctx)
}

func worker() error {
	db, err := database()
	if err != nil {
		return err
	}
	defer db.Close()
	org := os.Getenv("LOOM_ORGANIZATION")
	if org == "" {
		org = "local"
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return orchestration.Run(ctx, &control.Store{DB: db, Organization: org, EnableCodex: os.Getenv("LOOM_ENABLE_CODEX_CONTAINER") == "true"})
}

func healthcheck() error {
	addr := os.Getenv("LOOM_LISTEN_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return errors.New("Invalid listen address")
	}
	req, err := http.NewRequest(http.MethodGet, "http://127.0.0.1:"+port+"/readyz", nil)
	if err != nil {
		return errors.New("Invalid healthcheck request")
	}
	req.Header.Set("Authorization", "Bearer "+os.Getenv("LOOM_API_TOKEN"))
	client := &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return errors.New("Readiness request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return errors.New("Readiness check failed")
	}
	return nil
}

func run() error {
	token := os.Getenv("LOOM_API_TOKEN")
	if len(token) < 32 {
		return errors.New("LOOM_API_TOKEN must contain at least 32 characters")
	}
	config, err := pgxpool.ParseConfig(os.Getenv("LOOM_DATABASE_URL"))
	if err != nil {
		return errors.New("Invalid database configuration")
	}
	if os.Getenv("LOOM_DATABASE_URL") == "" {
		return errors.New("LOOM_DATABASE_URL is required")
	}
	config.MaxConns = 10
	config.ConnConfig.RuntimeParams["default_transaction_read_only"] = "on"
	config.ConnConfig.RuntimeParams["statement_timeout"] = "5000"
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		return errors.New("Cannot create database pool")
	}
	defer pool.Close()
	db, err := database()
	if err != nil {
		return err
	}
	defer db.Close()
	org := os.Getenv("LOOM_ORGANIZATION")
	if org == "" {
		org = "local"
	}
	dir := os.Getenv("LOOM_WEB_DIR")
	if dir == "" {
		dir = "apps/web/dist"
	}
	if _, err := os.Stat(path.Join(dir, "index.html")); err != nil {
		return errors.New("Build console assets before starting the server")
	}
	addr := os.Getenv("LOOM_LISTEN_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	store := &control.Store{DB: db, Organization: org, EnableCodex: os.Getenv("LOOM_ENABLE_CODEX_CONTAINER") == "true"}
	api := http.NewServeMux()
	api.Handle("/v1/github/", githubintegration.NewHandler(store, token, os.Getenv("LOOM_GITHUB_WEBHOOK_SECRET"), githubintegration.NewAPI(os.Getenv("LOOM_GITHUB_API_TOKEN"))))
	api.Handle("/v1/", control.NewAPIHandler(store, token))
	server := &http.Server{Addr: addr, Handler: newHandler(postgresReader{pool, org, store}, token, api, os.DirFS(dir)), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 45 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 32 << 10}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	slog.Info("server_started", "address", addr)
	err = server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
