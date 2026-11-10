# A Go HTTP service on Cloud Run

How a `cmd/<app>/` service is built here: stdlib HTTP, `log/slog` JSON in the Cloud Logging shape, trace correlation from the request headers, Postgres through `pgxpool` over the Cloud SQL socket, migrations embedded in the same binary, and a minimal golangci-lint v2 config. Every code block is a complete file that compiles with `go vet` on the version its section names; fragments are marked as such. Written from the Go release notes (go.dev/doc/devel/release), the Cloud Logging, Cloud Run, Cloud Trace, Error Reporting and Cloud SQL documentation, pkg.go.dev and golangci-lint.run, 2026-09-30.

## Go version: raise the `go` line first

`go vet` and `go test` check that a file uses no API newer than the module's `go` line (the `stdversion` analyzer; `go test` runs it since 1.27). A module at `go 1.24` that calls `sync.WaitGroup.Go` builds but fails vet with `requires go1.25 or later`. Before using an API from the table, raise the `go` line in `go.mod` to that version; the project's `go.mod` carries no `toolchain` directive.

| API | Since |
|---|---|
| `testing.B.Loop`, `T.Context`, `B.Context`, `omitzero` JSON tag, `os.Root`, `crypto/rand.Text`, `go.mod` `tool` directive | 1.24 |
| `sync.WaitGroup.Go`, `testing/synctest.Test` (GA), container-aware `GOMAXPROCS` (reads the cgroup CPU limit, which is what Cloud Run sets) | 1.25 |
| `go fix` as the modernizer (replaces hand-written rewrite tools), `new(expr)`, `slog.NewMultiHandler`, `ServeMux` trailing-slash redirect is 307 | 1.26 |
| `httptest.NewTestServer`, `synctest.Sleep`, `encoding/json` backed by v2 (rejects duplicate object names and invalid UTF-8; error text differs), generic methods | 1.27 |

Supported releases are the two newest majors plus the current one. `cloud.google.com/go/cloudsqlconn` needs 1.26+; `pgx/v5` needs 1.25+.

## HTTP core

One `http.ServeMux` with method patterns (1.22+), handlers that return errors through a small adapter, timeouts on the server, graceful shutdown on `SIGTERM` (Cloud Run sends it, then gives the instance ten seconds).

```go
// cmd/app/main.go — a complete service: routing, JSON helpers, timeouts, shutdown.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// apiError is what a handler returns to describe a client-visible failure.
type apiError struct {
	Status int    `json:"-"`
	Code   string `json:"status"` // the google.rpc.Code name, see the json-api-style skill
	Reason string `json:"-"`      // ErrorInfo.reason: UPPER_SNAKE_CASE, what the client can act on
	Field  string `json:"-"`      // when set, a BadRequest.fieldViolations entry names the field (validation errors)
	Msg    string `json:"message"`
}

// errorDomain is the ErrorInfo.domain of this service (AIP-193): its API's domain name.
const errorDomain = "greet.example.com"

func (e *apiError) Error() string { return e.Msg }

// handler is a handler that returns an error; adapt turns it into http.Handler.
type handler func(w http.ResponseWriter, r *http.Request) error

func adapt(log *slog.Logger, h handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		err := h(w, r)
		if err == nil {
			return
		}
		var ae *apiError
		if !errors.As(err, &ae) {
			log.ErrorContext(r.Context(), "handler failed", "err", err, "path", r.URL.Path)
			ae = &apiError{Status: http.StatusInternalServerError, Code: "INTERNAL", Reason: "INTERNAL", Msg: "internal error"}
		}
		details := []any{map[string]any{ // json-api-style rule 7: every error carries an ErrorInfo
			"@type": "type.googleapis.com/google.rpc.ErrorInfo", "reason": ae.Reason, "domain": errorDomain,
		}}
		if ae.Field != "" { // a validation error also names the field, which the frontend form renders
			details = append(details, map[string]any{
				"@type": "type.googleapis.com/google.rpc.BadRequest",
				"fieldViolations": []map[string]string{{"field": ae.Field, "description": ae.Msg}},
			})
		}
		writeJSON(w, ae.Status, map[string]any{"error": map[string]any{
			"code": ae.Status, "status": ae.Code, "message": ae.Msg, "details": details,
		}})
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v) // the response has started; nothing left to report to the client
}

// readJSON decodes a body of at most 1 MiB, refusing unknown fields.
func readJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		// the decoder's message can echo the body: log it, never return it
		slog.DebugContext(r.Context(), "malformed body", "err", err)
		return &apiError{Status: http.StatusBadRequest, Code: "INVALID_ARGUMENT", Reason: "MALFORMED_BODY", Msg: "malformed JSON body"}
	}
	return nil
}

type greeting struct {
	Name string `json:"name"`
}

func hello(w http.ResponseWriter, r *http.Request) error {
	var g greeting
	if err := readJSON(r, &g); err != nil {
		return err
	}
	if g.Name == "" {
		return &apiError{Status: http.StatusBadRequest, Code: "INVALID_ARGUMENT", Reason: "NAME_REQUIRED", Field: "name", Msg: "name is required"}
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "hello, " + g.Name})
	return nil
}

func newMux(log *slog.Logger) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("GET /healthz", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	mux.Handle("POST /hello", adapt(log, hello))
	mux.Handle("GET /items/{id}", adapt(log, func(w http.ResponseWriter, r *http.Request) error {
		writeJSON(w, http.StatusOK, map[string]string{"id": r.PathValue("id")})
		return nil
	}))
	return mux
}

func run(ctx context.Context, log *slog.Logger, addr string) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           newMux(log),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
		// No BaseContext tied to ctx: SIGTERM must start Shutdown below, not cancel every in-flight request's r.Context().
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("listening", "addr", addr)
	select {
	case err := <-errc:
		return fmt.Errorf("serving: %w", err)
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutting down: %w", err)
	}
	return nil
}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	port := os.Getenv("PORT") // Cloud Run sets it; 8080 locally
	if port == "" {
		port = "8080"
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, log, ":"+port); err != nil {
		log.Error("exit", "err", err)
		os.Exit(1)
	}
}
```

Rules the sample shows: the mux is built in one function so a test can serve it with `httptest`; dependencies are wired by hand in `main`; `PORT` comes from the environment because Cloud Run sets it; a handler never writes a 5xx body itself, the adapter does, and the adapter logs the underlying error once. Middleware is a function `func(http.Handler) http.Handler` applied around the mux in `run`; a recovery middleware logs `string(debug.Stack())` under `stack_trace` (below) and returns 500.

## Logging on Cloud Run: the Cloud Logging JSON contract

Never log a query string (`r.URL.Path`, not `r.URL.String()`), an `Authorization`, `Cookie` or `x-goog-iap-jwt-assertion` header (a signed identity assertion carrying the user's email), or a request body: that is the SKILL.md logging rule, and the `httpRequest` key below is where it is most easily broken (`requestUrl` carries the query). Cloud Run reads each stdout line as JSON and lifts a few keys out of the payload into the `LogEntry`: `severity`, `message`, `time` (or `timestamp`), `logging.googleapis.com/trace`, `logging.googleapis.com/spanId`, `logging.googleapis.com/trace_sampled`, `logging.googleapis.com/sourceLocation` (`file`, `line`, `function`), `logging.googleapis.com/labels`, `httpRequest`. Everything else stays in `jsonPayload`.

Three facts drive the handler below:

- `severity` takes `DEFAULT DEBUG INFO NOTICE WARNING ERROR CRITICAL ALERT EMERGENCY`. slog prints `WARN`, which Cloud Logging does not recognise, so `LevelWarn` maps to `WARNING`; `DEBUG`, `INFO`, `ERROR` match as they are.
- `trace` must be `projects/PROJECT_ID/traces/TRACE_ID`, not the bare id, or the container log is not correlated with the request log. `spanId` is 16 hex characters.
- The incoming request carries `traceparent: 00-<32 hex trace id>-<16 hex span id>-<2 hex flags>` (W3C; Cloud Run populates it) and, for older callers, `X-Cloud-Trace-Context: TRACE_ID/SPAN_ID;o=1` where the span id is decimal. Prefer `traceparent`.

```go
// internal/gcplog/gcplog.go — slog in the Cloud Logging shape, trace correlation from the request.
package gcplog

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
)

type traceKey struct{}

// Trace is what middleware extracts from the request headers.
type Trace struct {
	ID      string // projects/<project>/traces/<32 hex>
	SpanID  string // 16 hex
	Sampled bool
}

// New returns a JSON logger whose keys Cloud Logging lifts out of the payload.
func New(w *os.File, level slog.Level) *slog.Logger {
	base := slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level:     level,
		AddSource: true,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if len(groups) > 0 {
				return a
			}
			switch a.Key {
			case slog.LevelKey:
				lvl := a.Value.Any().(slog.Level)
				s := lvl.String()
				if lvl == slog.LevelWarn {
					s = "WARNING"
				}
				return slog.String("severity", s)
			case slog.MessageKey:
				a.Key = "message"
			case slog.TimeKey:
				a.Key = "timestamp"
			case slog.SourceKey:
				a.Key = "logging.googleapis.com/sourceLocation" // slog's Source has file, line, function
			}
			return a
		},
	})
	return slog.New(&traceHandler{Handler: base})
}

// traceHandler adds the three trace keys from the context to every record.
type traceHandler struct{ slog.Handler }

func (h *traceHandler) Handle(ctx context.Context, r slog.Record) error {
	if t, ok := ctx.Value(traceKey{}).(Trace); ok && t.ID != "" {
		r.AddAttrs(
			slog.String("logging.googleapis.com/trace", t.ID),
			slog.String("logging.googleapis.com/spanId", t.SpanID),
			slog.Bool("logging.googleapis.com/trace_sampled", t.Sampled),
		)
	}
	return h.Handler.Handle(ctx, r)
}

func (h *traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &traceHandler{Handler: h.Handler.WithAttrs(attrs)}
}

func (h *traceHandler) WithGroup(name string) slog.Handler {
	return &traceHandler{Handler: h.Handler.WithGroup(name)}
}

// Middleware puts the request's trace into the context; project is the GCP project id.
func Middleware(project string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if t, ok := parse(project, r.Header); ok {
			r = r.WithContext(context.WithValue(r.Context(), traceKey{}, t))
		}
		next.ServeHTTP(w, r)
	})
}

func parse(project string, h http.Header) (Trace, bool) {
	// traceparent: 00-<trace id>-<span id>-<flags>
	if tp := h.Get("traceparent"); tp != "" {
		parts := strings.Split(tp, "-")
		if len(parts) == 4 && isHex(parts[1], 32) && isHex(parts[2], 16) { // length AND alphabet: the id is interpolated into a resource name
			return Trace{
				ID:      fmt.Sprintf("projects/%s/traces/%s", project, parts[1]),
				SpanID:  parts[2],
				Sampled: strings.HasSuffix(parts[3], "1"),
			}, true
		}
	}
	// X-Cloud-Trace-Context: <trace id>/<decimal span id>;o=<0|1>
	if x := h.Get("X-Cloud-Trace-Context"); x != "" {
		id, rest, _ := strings.Cut(x, "/")
		span, opts, _ := strings.Cut(rest, ";")
		t := Trace{ID: fmt.Sprintf("projects/%s/traces/%s", project, id), Sampled: opts == "o=1"}
		if n, err := strconv.ParseUint(span, 10, 64); err == nil {
			t.SpanID = fmt.Sprintf("%016x", n)
		}
		return t, isHex(id, 32)
	}
	return Trace{}, false
}

// isHex reports whether s is exactly n lowercase hex digits.
func isHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
```

Use: `log := gcplog.New(os.Stdout, slog.LevelInfo)` in `main`, `gcplog.Middleware(projectID, mux)` around the mux, and `log.InfoContext(r.Context(), ...)` in handlers so the record carries the request's trace. The project id comes from a flag or `GOOGLE_CLOUD_PROJECT` (set it in the service's env; Cloud Run does not inject it). Locally the handler prints the same JSON; nothing is lost.

**Error Reporting** needs no client library. It creates an error event from any log entry at severity `ERROR` or above that carries a stack trace: log the panic or 5xx with `slog.String("stack_trace", string(debug.Stack()))`. A message-only entry is forced in with `"@type": "type.googleapis.com/google.devtools.clouderrorreporting.v1beta1.ReportedErrorEvent"`; `serviceContext` (`{"service": …, "version": …}`) groups events by deploy, so add it as a logger attribute in `main` with the revision name from `K_REVISION`.

**Tracing** (spans, not only correlation) is a justified dependency when needed, never a default: the OpenTelemetry SDK with `otlptracegrpc` to `telemetry.googleapis.com:443`, `grpc.WithPerRPCCredentials(oauth.NewApplicationDefault(ctx))`, resource attribute `gcp.project_id`, and the role `roles/telemetry.tracesWriter` on the service account. The Google-specific exporter module `opentelemetry-operations-go/exporter/trace` is deprecated and archived from 2027; do not add it. **Cloud Profiler** is one import and one call when CPU or heap questions come up: `cloud.google.com/go/profiler`, `profiler.Start(profiler.Config{Service: svc, ServiceVersion: rev})` first thing in `main`, role `roles/cloudprofiler.agent`.

## Database: Postgres on Cloud SQL

Three decisions, in order:

1. **Driver:** `github.com/jackc/pgx/v5` with `pgxpool` (a `*pgx.Conn` is not safe for concurrent use). The native interface, not `database/sql`, when the only database is Postgres; `pgx/v5/stdlib` only where a library insists on `*sql.DB` (golang-migrate does).
2. **Connection path:** the Cloud SQL Unix socket. `gcloud run deploy --add-cloudsql-instances PROJECT:REGION:INSTANCE` mounts `/cloudsql/PROJECT:REGION:INSTANCE`; the connection is encrypted by the platform, with no proxy, sidecar or extra module. `cloud.google.com/go/cloudsqlconn` (Go 1.26+) is added only for IAM database authentication: `cloudsqlconn.NewDialer(ctx, cloudsqlconn.WithIAMAuthN(), cloudsqlconn.WithLazyRefresh())` and a `config.ConnConfig.DialFunc` that calls `dialer.Dial(ctx, instance)` (the signatures differ: the pool passes network and address, the dialer wants the instance name; `data-gcp/references/connect.md` has the wrapper); `WithLazyRefresh` because a Cloud Run instance without CPU always allocated cannot refresh certificates in the background. The Auth Proxy is the local-development form of the same connector.
3. **Migrations:** `github.com/golang-migrate/migrate/v4` with the `source/iofs` driver over an `embed.FS`, exposed as a `migrate` subcommand of the service binary and run as a Cloud Run Job (`gcloud run jobs execute --wait`) from `/fyr-preflight` before the new revision receives traffic. Never on instance start: every cold start would race every other. The job runs under its own service account (IAM and logs tell the two apart). The Postgres role comes from the DSN, not the SA: on the socket-and-password path there is one role, `app` (a `cloudsqlsuperuser` member); the owner/runtime split comes with IAM database authentication, where IAM users start with no privileges, the `postgres` user grants the migrate user CREATE on `public` once, and the first migration grants the runtime user DML on what the migrate user creates, with an assertion migration that fails if the runtime user holds CREATE at that point (`data-gcp` skill § Roles, the one recipe).

```go
// internal/db/db.go — pgxpool over the Cloud SQL socket; the DSN comes from configuration, never source.
package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Open connects with dsn, for Cloud Run of the form
// "host=/cloudsql/PROJECT:REGION:INSTANCE user=app dbname=app password=..." (the password from
// Secret Manager through --set-secrets, never --set-env-vars) and locally
// "postgres://app:app@localhost:5432/app". The pool is closed by the caller.
func Open(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parsing dsn: %w", err)
	}
	cfg.MaxConns = 4 // Cloud Run: instances × MaxConns stays under the Cloud SQL connection limit
	cfg.MaxConnIdleTime = 5 * time.Minute
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("opening pool: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pinging database: %w", err)
	}
	return pool, nil
}
```

```go
// internal/db/migrate.go — the `migrate` subcommand: golang-migrate over embedded SQL files.
package db

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	mpg "github.com/golang-migrate/migrate/v4/database/pgx/v5" // the pgx driver, not database/postgres (which pulls lib/pq)
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver
)

//go:embed migrations/*.sql
var migrations embed.FS

// Migrate applies every pending migration in migrations/ (NNNN_name.up.sql, NNNN_name.down.sql).
func Migrate(dsn string) error {
	src, err := iofs.New(migrations, "migrations")
	if err != nil {
		return fmt.Errorf("reading embedded migrations: %w", err)
	}
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer sqlDB.Close()
	drv, err := mpg.WithInstance(sqlDB, &mpg.Config{})
	if err != nil {
		return fmt.Errorf("preparing migration driver: %w", err)
	}
	m, err := migrate.NewWithInstance("iofs", src, "postgres", drv)
	if err != nil {
		return fmt.Errorf("preparing migrations: %w", err)
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("applying migrations: %w", err)
	}
	return nil
}
```

`main` dispatches on `os.Args[1] == "migrate"` to `db.Migrate(dsn)` and exits; the Cloud Run Job runs the same image with `--args migrate`. Query code uses `pool.Query(ctx, …)` with positional parameters (`$1`), `pgx.CollectRows` and `pgx.RowToStructByName` for scanning; no ORM, no query builder, by the Dependencies rule.

## Lint: golangci-lint v2

Install it pinned through the Go toolchain, which verifies the module against the checksum database: `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0`. The project documents that `go install` is not guaranteed to work and prefers its release binaries; when that matters, download the pinned release asset and its `-checksums.txt` into `.agents/scratch/` and verify with `sha256sum -c --ignore-missing` (macOS: `shasum -a 256 -c --ignore-missing`) before extracting — never a script piped into `sh`. Run `golangci-lint run` and `golangci-lint fmt`; `golangci-lint config verify` checks the file. The minimal config for a stdlib-first service, `.golangci.yml` at the module root:

```yaml
version: "2"
linters:
  default: standard        # errcheck, govet, ineffassign, staticcheck, unused
  enable: [gosec, nolintlint, modernize]
formatters:
  enable: [gofmt, goimports]
```

`run.timeout` defaults to no limit in v2; a `//nolint:<linter> // <reason>` is the only accepted suppression (the `nolintlint` linter refuses an unexplained one). `go fix ./...` (1.26+) applies the modernizers; there is no separate tool and no config file for them.

## Deprecated or replaced stdlib

| Instead of | Use |
|---|---|
| `ioutil.*` | `io.*`, `os.*` (since 1.16) |
| `math/rand` seeded by hand | `math/rand/v2`, or `crypto/rand.Text` for tokens (1.24) |
| `interface{}` | `any` |
| a `tools.go` with blank imports | `go get -tool …` and the `tool` directive in `go.mod` (1.24) |
| `sync.WaitGroup` + `Add`/`Done` around a goroutine | `wg.Go(func() { … })` (1.25) |
| `context.Background()` in a test | `t.Context()` (1.24) |
| `for i := 0; i < b.N; i++` | `for b.Loop()` (1.24) |
