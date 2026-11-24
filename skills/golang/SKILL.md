---
name: golang
description: "Go conventions: cmd/<app> + internal/ layout, stdlib-first packages, slog structured logs, table-driven -race tests."
---

# Golang

- **Layout & wiring:** `cmd/<app>/` + `internal/` (`pkg/` only for cross-module imports); wire dependencies by hand in `main()` (no DI framework, no global mutable state, no `init()` except driver registration).
- **Errors & guards:** Never `panic()` for recoverable errors; wrap with `fmt.Errorf("...: %w", err)`, match with `errors.Is`/`errors.As`, handle every error, and fail closed when a destructive-action guard errors.
- **Stdlib, logging & concurrency:** Stdlib first (justify any external module); `log/slog` JSON (`InfoContext`, never log secrets/PII); `context.Context` first; `wg.Go`/`errgroup` + `-race` table tests + `synctest.Test` (no real sleeps); no `toolchain` directive in `go.mod`.
- **References:** Load `references/service.md` (HTTP, Cloud Logging, pgx, lint, Go versions) and `references/testing.md` (red-first, fakes, `httptest`, `-race`, fuzzing) on demand.

## Coding standards

### Errors
- Never `panic()` for a recoverable error; return it. A panic is for an unrecoverable bug; recovery middleware turns it into a logged 500 with stack.
- Wrap with context: `fmt.Errorf("loading config: %w", err)`; match with `errors.Is` / `errors.As`, never by error string.
- Handle every error at the call site; `_ = f()` is allowed only with an inline comment explaining why nothing can be done (e.g. response already started, best-effort close).
- Guards before destructive actions preserve data on error: `if err != nil { return err }` before deleting, never `if err == nil && somethingToLose { keep } else { delete }`. Test the guard-error path.

### Structure and style
- Layout `cmd/<app>/` + `internal/`; `pkg/` only for code another module imports. One `main` wires dependencies by hand; no DI framework, no global mutable state, no `init()` except driver registration.
- Keep `gofmt`, `goimports`, `go vet`, and `staticcheck` (golangci-lint v2, `references/service.md` § Lint) clean. Fix failures in source; `//nolint:<linter> // <reason>` or `t.Skip` requires a demonstrated false positive or absent environment.
- Exported symbols carry doc comments; comments explain why, not what.
- Never pass user, home, env, or flag paths into `filepath.Glob` (`[`, `*`, `?` escape directories): read the parent with `os.ReadDir` and match names; `filepath.Glob` takes literal patterns only.
- JSON struct tags use lowerCamelCase (`json:"pageSize"`, `json:"createTime"`) per `json-api-style`; prefer `omitzero` (Go 1.24+) over `omitempty` for structs and times.

### Go version
- Raise the `go` line in `go.mod` before using newer stdlib APIs (`go vet`/`go test` fail with `requires go1.N or later` otherwise); the version table and supported-release rule live in `references/service.md` § Go version. `go.mod` carries no `toolchain` directive (pins break offline builds).
- Go 1.27's `encoding/json` is backed by v2: it rejects duplicate object keys and invalid UTF-8 with new error messages; match errors by type, never message text.

### Logging
- Use `log/slog` JSON in the Cloud Logging shape (`severity`, `message`, `timestamp`, `logging.googleapis.com/trace`) from `references/service.md` § Logging, locally and in production.
- Attach contextual fields (request trace, resource names) and use `log.InfoContext(ctx, ...)` in handlers.
- Never log tokens, passwords, API keys, or PII; log URL paths without query strings unless explicitly requested.

### Concurrency
- `context.Context` is the first parameter of anything that waits and the sole cancellation channel.
- Every goroutine has an owner that stops it and collects its error: `wg.Go` (Go 1.25+) with `sync.WaitGroup`, or `errgroup.Group` when first-error cancellation is needed (justify the import like any dependency).
- Channels for hand-off/ownership, mutex for small shared state; run `-race` on every test (`references/testing.md`); test timing inside `synctest.Test`, never real sleeps.

### Dependencies
- Standard library first; justify every external module in the handoff/change notes, naming the rejected stdlib alternative. Accepted service defaults: `github.com/jackc/pgx/v5`, `github.com/golang-migrate/migrate/v4`, `cloud.google.com/go/cloudsqlconn` (IAM DB auth only), OpenTelemetry (when spans are needed), `cloud.google.com/go/profiler` (when profiling is needed).
- No assertion, mock, or DI libraries: use `testing`, hand-written fakes, and explicit constructors from `main()`.

### Runtime data and code shape
- Service runtime data (DB files, uploads) is neither scratch nor source: default outside the repo via flag/env, or gitignore unavoidable repo-local defaults in the same change. Plain `go run` must never dirty the tree.
- Before claiming done, apply `review-standards` § 8: fail-closed guards, dry-run/read-only path parity, two-way embedded artifact pins, and bare verification commands never chained to commit.

### Testing HTTP shutdown and OS-specific symbols
- **HTTP server shutdown tests:** Against local `http.Server` instances closed via `Server.Shutdown(ctx)`, use a custom `http.Client` with `Transport: &http.Transport{DisableKeepAlives: true}` and drain response bodies (`io.Copy(io.Discard, resp.Body); resp.Body.Close()`); open keep-alive sockets or unread bodies block `Shutdown` until context deadline.
- **OS-specific test symbols:** Isolate tests referencing OS-specific declarations (such as `*_linux.go`) in build-tagged files (`*_linux_test.go` or `//go:build linux`), never behind `if runtime.GOOS == "linux"` in unconstrained `*_test.go`, so cross-OS checks (`GOOS=darwin go vet ./...`) compile cleanly.

## Progressive references

| Load when | File | Contents |
|---|---|---|
| Writing HTTP handlers, Cloud Logging, DB pools, lint config, or checking Go versions | `references/service.md` | HTTP mux/error handlers/JSON/timeouts/shutdown, Cloud Logging & trace correlation, `pgxpool`/`cloudsqlconn`/embedded migrations, golangci-lint v2, deprecated stdlib, Go version table |
| Writing tests, fakes, `httptest`, timing tests, benchmarks, or fuzzers | `references/testing.md` | Red-first & must-fail mutations, table tests, hand-written fakes, `httptest`/`t.Context`, `-race`, `synctest.Test`, `b.Loop`, fuzzing, layout |

Container builds and deploys live in `docker` (`golang:<major.minor>` base tag) and `cloud-run-deploy`.

## Further reading

- Go release notes: <https://go.dev/doc/devel/release>
- Google Go Style Guide: <https://google.github.io/styleguide/go/>
