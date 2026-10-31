---
name: golang
description: "Go standards for the services built here (cmd/<app> + internal, log/slog JSON, stdlib first) and two references: service.md (HTTP core, Cloud Logging JSON contract and trace correlation on Cloud Run, Error Reporting, pgx over the Cloud SQL socket, embedded migrations, golangci-lint v2) and testing.md (table tests, hand-written fakes, httptest, -race, synctest.Test, b.Loop). Use whenever writing, reviewing or testing Go code, raising the go line, or choosing a Go dependency. Do NOT use for infrastructure (terraform), Kubernetes manifests (gke-basics), container builds (docker), the deploy itself (cloud-run-deploy), or frontend code (frontend-ui-modern, typescript)."
---

# Golang

The rules below apply to every Go file in this environment; the two references hold the patterns. Read `references/service.md` before the first handler of an HTTP service and `references/testing.md` before its first test; read the matching section when its topic comes up (logging, database, lint, concurrency).

**When NOT to use this skill:** frontend-only work (`frontend-ui-modern`, `frontend-ui-economist`, `typescript`), infrastructure-as-code (`terraform`), Kubernetes manifests (`gke-basics`), container builds (`docker`), the deploy mechanics (`cloud-run-deploy`), shell automation (`bash`).

## Coding standards

### Errors
- Never `panic()` for a recoverable error; return it. A panic is for a programming error that cannot continue, and a recovery middleware turns it into a logged 500 with the stack.
- Wrap with context: `fmt.Errorf("loading config: %w", err)`; match with `errors.Is` / `errors.As`, never by string.
- Handle every error at the call site; `_ = f()` is accepted only where the comment beside it says why nothing can be done (a response already started, a best-effort close).
- A guard in front of a destructive action keeps the data when its check ERRORS: `if err != nil { return err }` before the delete, never `if err == nil && somethingToLose { keep } else { delete }`. The test covers the case where the check itself cannot run.

### Structure and style
- Layout `cmd/<app>/` + `internal/`; `pkg/` only for code another module imports. One `main` wires dependencies by hand; no DI framework, no global mutable state, no `init()` except a driver registration.
- `gofmt` and `goimports` always; `go vet` and `staticcheck` (through golangci-lint v2, `references/service.md` § Lint) clean. A vet, lint or test failure is fixed in the source; a `//nolint:<linter> // <reason>` or a `t.Skip` is for a demonstrated false positive or an absent environment and says so.
- Exported symbols carry a doc comment; comments say why, not what.
- A path from the user, the home, the environment or a flag is never part of a `filepath.Glob` pattern (`[`, `*`, `?` in it reach other directories): list the parent with `os.ReadDir` and match names; a glob takes literal patterns only.
- JSON struct tags are lowerCamelCase (`json:"pageSize"`, `json:"createTime"`), the wire shape the `json-api-style` skill defines; `omitzero` (1.24) over `omitempty` for structs and times.

### Go version
- Raise the `go` line in `go.mod` before using a newer API: `go vet` and `go test` fail with `requires go1.N or later` otherwise (verified 2026-09-30: `WaitGroup.Go` on a `go 1.24` module). The version table and the supported-release rule are `references/service.md` § Go version. `go.mod` carries no `toolchain` directive (a pin makes the module unbuildable offline).
- 1.27's `encoding/json` is backed by v2: it rejects duplicate object names and invalid UTF-8, and error messages changed; a test that matches a JSON error string is rewritten to match the type.

### Logging
- `log/slog` with JSON output, in the Cloud Logging shape (`severity`, `message`, `timestamp`, `logging.googleapis.com/trace`) from `references/service.md` § Logging; the same handler locally.
- Contextual fields on every record (request trace, resource names); `log.InfoContext(ctx, …)` in request handlers so the trace rides along.
- Never log tokens, passwords, API keys or PII; a query string is logged as its path only unless the operator says otherwise.

### Concurrency
- `context.Context` is the first parameter of anything that waits, and the only way to cancel it.
- Every goroutine has an owner that stops it and a place its error goes: `wg.Go` (1.25) with a `sync.WaitGroup`, or an `errgroup.Group` when the first error must cancel the rest (that import is justified in the change entry like any other).
- Channels for hand-off and ownership, a mutex for a small shared state; `-race` on every test run (`references/testing.md`); timing code is tested in a `synctest.Test` bubble, never with real sleeps.

### Dependencies
- Standard library first; every external module is justified in the change entry before it is added, naming the stdlib alternative it rejected. The accepted ones for a service here: `github.com/jackc/pgx/v5` (Postgres), `github.com/golang-migrate/migrate/v4` (migrations), `cloud.google.com/go/cloudsqlconn` only for IAM database auth, OpenTelemetry only when spans are needed, `cloud.google.com/go/profiler` when profiling is.
- No assertion, mock or DI library: `testing`, hand-written fakes, constructors called from `main()`.

### Runtime data and code shape
- A service's own RUNTIME data (a database, an upload directory) is not scratch and not source: its default path comes from a flag or an environment variable with a default outside the repository, and a default inside it that cannot be avoided is gitignored in the same change. A plain `go run` must never dirty the tree.
- Before claiming done, apply `review-standards` § 8: a guard keeps the data when its check errors, a read-only mode resolves every guard and path the real run does, an embedded artifact is pinned both ways to what git tracks, a verification command is never chained to a commit.

## References

| Topic | File |
|---|---|
| HTTP core (mux, handlers that return errors, JSON helpers, timeouts, shutdown), logging on Cloud Run (the JSON contract, trace correlation, Error Reporting, when OpenTelemetry and Profiler are justified), database (pgxpool over `/cloudsql`, cloudsqlconn for IAM auth, embedded migrations as a Cloud Run Job), golangci-lint v2, deprecated stdlib, the Go version table | `references/service.md` |
| Tests: what a test proves (red first, must-fail mutations), table tests, fakes by hand, `httptest` (including `NewTestServer`), `t.Context`, `-race`, `synctest.Test`, `b.Loop`, fuzzing, layout | `references/testing.md` |

The container image and the deploy are the `docker` and `cloud-run-deploy` skills; the image's base is `golang:<major.minor of the go.mod go line>` (docker skill; a three-part tag freezes a patch).

## Further reading

- Go release notes — <https://go.dev/doc/devel/release> (the version facts above come from here)
- Google Go Style Guide — <https://google.github.io/styleguide/go/> (a reference, not adopted wholesale)
