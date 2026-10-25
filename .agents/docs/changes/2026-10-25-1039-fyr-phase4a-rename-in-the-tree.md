---
owner: main
date: 2026-10-25
---
# fyr Phase 4a: the rename in the tree, mechanical     (date: 2026-10-25)

## Summary
The tree is `fyr` (plan `2026-10-24-fyr-agy-only.md` Phase 4a, D12, D15, D22):
- module `github.com/kesokaj/fyr`, root package `fyr`, the binary at `cmd/fyr`;
- every path into the state directory reads `harness.StateDir` (`.config/fyr`);
- search honours `.fyr-noindex` and still honours the old `.sh-agent-noindex`;
- `fyr` reads `FYR_*` only, and names each set `SH_AGENT_*` variable once on stderr.

The living text says `fyr`. A scratch-home install from this tree is `fyr`: `~/.local/bin/fyr`, `~/.config/fyr/state.json`, `drift none` (§ 3 and § 3a). No migration exists yet. That is Phase 4b's, along with the release names, the dream unit names and `deploy/remote.md`.

## Type
task (plan phase)

## Files changed
From `git show --stat c565b27` (117 files, +1147 −918; Go: 70 files, +758 −531):
- **Moves (`git mv`):**
  - `cmd/sh-agent/` → `cmd/fyr/`;
  - `.agents/docs/specs/sh-agent/` → `specs/fyr/`;
  - `testing/sh-agent-matrix.md` → `testing/fyr-matrix.md`;
  - `skills/google-catalog/catalog/.sh-agent-noindex` → `.fyr-noindex`.
- **Go:**
  - `go.mod` and every import path.
  - `package styrman` → `fyr` (`embed.go` and the root tests), and `source.go`'s alias.
  - `internal/harness/state.go`: new `const StateDir`. Every `filepath.Join(…, ".config", "sh-agent", …)` in code and tests now reads it; `metrics` imports `harness` for it.
  - `internal/search/reconcile.go`: `noIndexMarker = ".fyr-noindex"`, `oldNoIndexMarker = ".sh-agent-noindex"`, `isNoIndexMarker`. The header says `left out by a noindex marker:`.
  - `cmd/fyr/main.go`: `oldEnvNotices`, called from `main` outside the rebuilt child. `SH_AGENT_REPO` → `FYR_REPO` and `SH_AGENT_CHILD` → `FYR_CHILD`; the test-only variables were renamed too.
  - `internal/metrics/{verb,show}.go`: a `fyr` verb table (no `report`). The `sh-agent` table is kept, and `isSQLiteVerb` counts both names: earlier transcripts and stored rows carry `sh-agent search` (D7, D20).
  - `internal/harness/render.go`: the context marker line. `internal/redact`: `[redacted by fyr]`.
  - Every message, comment and doc comment naming the binary.
- **Content:** `context/AGENTS.md` (5 mentions); the 5 agent bodies that named the binary; the skills (`sh-agent` → `fyr`, `~/.config/sh-agent` → `~/.config/fyr`); `google-catalog/catalog/MANIFEST.md`'s hand-written Notes line; `refresh-catalog.sh` (the marker it writes).
- **Docs:**
  - `README.md` (title, quick start, verbs table, map labels, layout);
  - `AGENTS.md` (title, rules, setup commands, layout, key files);
  - `deploy/local.md`;
  - `specs/fyr/*.md` (search.md describes both markers; delivery.md says the tags through v2.7.0 ship `sh-agent`);
  - `testing/{guide.md,skill-matrix.md,kit-preamble.sh,agy-rel/run.sh}` (the wrapper is `fyr`, the grant is `command(fyr)`);
  - `fyr-matrix.md` (title, one note, one command cell);
  - `.gitignore` (`/fyr`).
- **Fixture:** `internal/search/testdata/recall.tsv` (gold paths under `cmd/fyr/`).

## Left for Phase 4b, and kept as history
The Phase 4a grep (`git grep -n 'sh-agent'` over the living files) lists these, each kept on purpose:
- **The old marker** and its test: `reconcile.go`, `search_test.go` (`TestOldNoIndexMarkerStillCounts`), `specs/fyr/search.md` (D12).
- **The env notice:** `main.go`, `usage_test.go`, `harness_test.go`, in upper case (`SH_AGENT_`), so the lower-case grep does not list them (D15).
- **`deploy/remote.md`:** rewritten in 4b.
- **The release names, 4b's `release.go` box:**
  - `release.go`'s asset name, `ReleaseRepo` and sums marker, and `release_test.go`;
  - README's § Install without cloning and § Install from a zip, which describe the published v2.7.0 assets. They were restored to their text at `5da24a5` after the scripted pass, so that no step mixes an old asset with the new installed name.
- **The dream unit names, 4b's timer box:** `sh-agent-dream` and `com.styrman.sh-agent-dream` in `timer.go`, `timer_test.go`, `memory_test.go`, `deploy/local.md:69` and `specs/fyr/dream.md:49`. Their text and the binary path in the unit are `fyr`.
- **History:**
  - `internal/metrics/{verb,show}.go` and `verb_test.go`: the old verb name (D20's historical-metrics exemption);
  - `fyr-matrix.md`'s dated records, which quote what the binary printed then, as its new note says;
  - the plan names `sh-agent-works-everywhere` and `recon-sh-agent-audit`: trail file names, in `apply.go:83`, `harness_test.go:1600` and the matrix.
- **The repository name `styrman-harness`:** clone URLs, `ReleaseRepo`, the README map's repository node, `errPlainCopy` and `adopt`'s message. The repository is renamed in Phase 6, and the release identity in 4b.

## Deviations from the plan's box
Recorded as plan D28 (by: implementer, open to the operator's veto).
- **Release names and timer unit names stay** (above). The box's grep expectation did not list them, but 4b's own boxes test and change them (`release.go`, "the timer rename"). Renaming them here would have done 4b's tests-first work without its tests.
- **The catalog's marker file was renamed** with `git mv`, and `refresh-catalog.sh` writes `.fyr-noindex`. The project `AGENTS.md` says the catalog changes only through its refresh script. The marker is the harness's own file, not an entry: `CHECKSUMS` does not cover it, and the script now writes what the tree holds. The MANIFEST's Notes line ("a new skill goes through `fyr adopt`") is hand-written text that the script keeps byte for byte.
- **The search header no longer names a marker file:** `left out by a noindex marker:`. With two names, either fixed name would be false for some directory.
- **`SH_AGENT_*` notices skip empty values.** An exported empty variable changes nothing.
- **The `fyr-matrix.md` records are not rewritten** (D8's reasoning applied to a living file's dated rows). A note says why.

## Agents dispatched
None. The main agent read its own diff. As in Phases 2, 3a and 3b, Phase 5 holds the `verifier`, `reviewer --code --docs` and `security` dispatches over the whole feature.

## Verification
`testing/2026-10-25-verify-fyr-rename.md` (PASS):
- red, then green, as the box asks;
- `gofmt` empty, `go vet` ok, `go test -race -count=1 -timeout 30m ./...` exit 0, all 9 packages;
- size pins 18,112 / 14,623 / 19,165;
- § 3 and § 3a scratch installs are `fyr`;
- govulncheck: 15 standard-library advisories of go1.24.4, the same on the base.

No Antigravity behavioural run; Phase 5 owes it.

## Process note
The rename commit was first written with a `Claude-Session:` trailer, against the global git rule (author from the local config only, no session links). It was amended before the push. The pushed `c565b27` carries none.
