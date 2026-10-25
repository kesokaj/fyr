# Testing Go here

The `testing` package, hand-written fakes, `httptest`, `-race`, and the two newer tools (`b.Loop`, `synctest.Test`). No assertion library, no mock generator: a failing test prints what it got and what it wanted, and a fake is a struct the test can read. Every block below is a complete file that compiles on the version its section names (`go.mod` `go` line raised first, see `service.md` § Go version). Written from the Go release notes and `go help testflag`, 2026-09-30.

## What a test proves

A test is evidence only when it has been seen red for the right reason. A new test runs bare before the code exists (or against the old behaviour) and fails with the message the bug or the task predicted; a test that passes before the change is not the test. When a reviewer or a bug record cites a must-fail mutation as evidence, the mutation follows `review-standards` § 2: one unique target, the mutated code compiled and a NAMED test failed, the original restored from a saved copy and `git diff` read. Coverage claims name what is not covered.

Run from the module root, bare, and read the exit status: `go vet ./... && go test -race ./...`. Useful flags: `-run 'Name'`, `-count=1` (no cache), `-shuffle=on`, `-timeout` (default 10m), `-cover` (`-covermode=atomic` is implied under `-race`), `-v` only when reading a failure.

Every Go block below compiles beside the first block's `greet.go` (one package, `<project>/internal/greet`); the blocks that name another package say which file they sit in.

## Table-driven tests

```go
package greet

import "testing"

func Hello(name string) string {
	if name == "" {
		return "hello, stranger"
	}
	return "hello, " + name
}

func TestHello(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{name: "named", in: "Ada", want: "hello, Ada"},
		{name: "empty falls back", in: "", want: "hello, stranger"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Hello(tc.in); got != tc.want {
				t.Errorf("Hello(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
```

`t.Errorf` keeps going (every row reports), `t.Fatalf` stops the subtest (use it when the next line would dereference the failed result). Subtests run in parallel with `t.Parallel()` only when they share no mutable state; the `tests` analyzer in `go vet` (1.24) rejects malformed test names and signatures.

## Fakes by hand, at the boundary

Declare the interface where it is consumed, as small as the consumer needs, and write the fake in the test file: state in, calls out. A fake that needs scripted return sequences says the interface is too wide.

```go
package orders

import (
	"context"
	"errors"
	"testing"
	"time"
)

var ErrNotFound = errors.New("not found")

type Item struct {
	ID    string
	Price int
}

// Catalog is what Service needs from the store; the store package may offer far more.
type Catalog interface {
	Item(ctx context.Context, id string) (Item, error)
}

type Service struct {
	cat Catalog
	now func() time.Time // injected so a test controls the clock
}

func NewService(cat Catalog, now func() time.Time) *Service {
	return &Service{cat: cat, now: now}
}

type Quote struct {
	Total   int
	Expires time.Time
}

func (s *Service) Quote(ctx context.Context, id string, qty int) (Quote, error) {
	it, err := s.cat.Item(ctx, id)
	if err != nil {
		return Quote{}, err
	}
	return Quote{Total: it.Price * qty, Expires: s.now().Add(10 * time.Minute)}, nil
}

// fakeCatalog is the test's catalog: a map and a call counter.
type fakeCatalog struct {
	items map[string]Item
	calls int
}

func (f *fakeCatalog) Item(_ context.Context, id string) (Item, error) {
	f.calls++
	it, ok := f.items[id]
	if !ok {
		return Item{}, ErrNotFound
	}
	return it, nil
}

func TestQuote(t *testing.T) {
	fixed := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	cat := &fakeCatalog{items: map[string]Item{"a": {ID: "a", Price: 250}}}
	svc := NewService(cat, func() time.Time { return fixed })

	q, err := svc.Quote(t.Context(), "a", 3)
	if err != nil {
		t.Fatalf("Quote: %v", err)
	}
	if q.Total != 750 {
		t.Errorf("Total = %d, want 750", q.Total)
	}
	if want := fixed.Add(10 * time.Minute); !q.Expires.Equal(want) {
		t.Errorf("Expires = %v, want %v", q.Expires, want)
	}
	if cat.calls != 1 {
		t.Errorf("catalog called %d times, want 1", cat.calls)
	}

	if _, err := svc.Quote(t.Context(), "missing", 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing item: err = %v, want ErrNotFound", err)
	}
}
```

Other boundaries: the filesystem is `t.TempDir()` or `testing/fstest.MapFS`; randomness is an injected `*rand.Rand` with a fixed seed; the environment is `t.Setenv`; a database is a real one in a container for the integration tier, never a mock of SQL. `t.Context()` (1.24) is cancelled when the test ends, before `t.Cleanup` functions run, so goroutines the test started see the cancel.

## HTTP handlers

Build the mux the way `main` does and drive it with `httptest`. `NewRecorder` for a handler, `NewServer` when the code under test is a client, `NewTestServer` (1.27) when the server and the client should meet on an in-memory network with no port.

```go
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /hello", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Name == "" {
			http.Error(w, `{"error":{"code":400,"status":"INVALID_ARGUMENT","message":"name is required"}}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"message": "hello, " + in.Name})
	})
	return mux
}

func TestHello(t *testing.T) {
	mux := newMux()

	t.Run("golden path", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/hello", strings.NewReader(`{"name":"Ada"}`))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body)
		}
		var out map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decoding body %q: %v", rec.Body, err)
		}
		if out["message"] != "hello, Ada" {
			t.Errorf("message = %q, want %q", out["message"], "hello, Ada")
		}
	})

	t.Run("missing name is 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/hello", strings.NewReader(`{}`))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("wrong method is 405", func(t *testing.T) {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hello", nil))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("status = %d, want 405", rec.Code)
		}
	})
}
```

The container smoke (its own tier; the integration tier is the real database, `testing-strategy` skill) runs the built binary: `docker build`, `docker run`, `curl` the health check and one real request (the `docker` skill); the e2e criteria come from the plan.

## Concurrency: `synctest.Test` and `-race`

`-race` is on for every run and finds unsynchronised access; it does not find deadlocks or leaked goroutines. `testing/synctest` (GA in 1.25 as `synctest.Test`; the 1.24 `synctest.Run` experiment is gone) runs the function in a bubble with a fake clock: `time.Sleep` and timers advance instantly when every goroutine in the bubble is blocked, and `synctest.Wait` blocks until they are. A test of a timeout, a ticker or a debounce finishes in microseconds and deterministically.

```go
package debounce

import (
	"testing"
	"testing/synctest"
	"time"
)

// After calls fn once d has passed with no further Reset; a bubble makes that instant to test.
type After struct {
	t  *time.Timer
	fn func()
}

func New(d time.Duration, fn func()) *After {
	a := &After{fn: fn}
	a.t = time.AfterFunc(d, fn)
	a.t.Stop()
	return a
}

func (a *After) Reset(d time.Duration) { a.t.Reset(d) }

func TestAfterFiresOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fired := 0
		a := New(time.Second, func() { fired++ })
		a.Reset(time.Second)
		a.Reset(time.Second) // a second reset within the window postpones, never doubles
		time.Sleep(2 * time.Second)
		synctest.Wait() // every goroutine in the bubble is idle; the timer callback has run
		if fired != 1 {
			t.Errorf("fired %d times, want 1", fired)
		}
	})
}
```

Rules: a goroutine started inside the bubble must finish inside it (the test fails otherwise, which is the leak check you want); real I/O does not belong in a bubble (`httptest.NewServer` in a bubble deadlocks, use `NewTestServer` or a fake); `sync.WaitGroup.Go` (1.25) replaces the `Add`/`Done` pair and the `waitgroupgo` fix rewrites the old form.

## Benchmarks and fuzzing

```go
package greet

import "testing"

func BenchmarkHello(b *testing.B) {
	for b.Loop() { // 1.24: timing starts here, the loop body is not inlined away, no b.N bookkeeping
		_ = Hello("Ada")
	}
}

func FuzzHello(f *testing.F) {
	f.Add("Ada")
	f.Add("")
	f.Fuzz(func(t *testing.T, name string) {
		got := Hello(name)
		if len(got) < len("hello, ") {
			t.Errorf("Hello(%q) = %q: shorter than its prefix", name, got)
		}
	})
}
```

`go test -bench . -benchmem -run '^$'` runs only benchmarks; `go test -fuzz FuzzHello -fuzztime 30s` fuzzes, and a crasher is written under `testdata/fuzz/` and replays as an ordinary test from then on. A benchmark result quoted in a change entry names the machine and the flags.

## Layout and naming

Tests live beside the code (`x_test.go`, same package for unit tests; `package x_test` when the test should see only the exported API). Shared fixtures go in `testdata/` (ignored by the Go tool). A test helper takes `t testing.TB` and calls `t.Helper()` first so failures point at the caller. Integration tests that need a service gate on an environment variable the testing guide names and `t.Skip` with the reason when it is absent, and the skip is counted in the verification log as not exercised, never as a pass.
