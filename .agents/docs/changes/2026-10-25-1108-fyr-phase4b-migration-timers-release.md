---
owner: main
date: 2026-10-25
---
# fyr Phase 4b: migration, timers, release, the crossing guide     (date: 2026-10-25)

## Summary
A home on v2.7.0 now crosses to `fyr` with one `fyr install` (plan `2026-10-24-fyr-agy-only.md` Phase 4b, D13, D14, D29, D30):
- **The state directory moves first.** Every verb that writes under it renames `~/.config/sh-agent` to `~/.config/fyr` before anything opens it, and says `migrated ~/.config/sh-agent → ~/.config/fyr`.
  - Both present: neither is touched, and one line says fyr reads the new one (F3).
  - A symlinked old dir: the writing verb is refused (D30).
  - Read-only verbs and modes never migrate. They name the old dir once on stderr and read nothing from it.
- **The resync removes the old files.** It reads the old manifest from the moved dir and removes the `~/.claude` files and `~/.local/bin/sh-agent` by today's rule. A symlinked entry stays (F2).
- **The dream timer is renamed** `fyr-dream` / `com.fyr.dream`. `install` replaces this home's unit under the old name, installing the new unit first and removing the old one after.
- **The release names are fyr's.** Asset `fyr-<os>-<arch>`, `ReleaseRepo = "github.com/kesokaj/fyr"`, and one sums marker, `# fyr vX.Y.Z`. The old `# styrman-harness` marker is refused.
- **`deploy/remote.md` has § Crossing to fyr**, and its release step names `fyr-*`, `fyr-vX.Y.Z.zip` and the one marker.
- **The kit preamble watches both real state dirs** until every place has crossed.
- **BUG-176 and BUG-177 are recorded**, and BUG-176's line is fixed.

## Type
task (plan phase)

## Files changed
- **Go, new:**
  - `internal/harness/migrate.go`: `OldStateDir`, `MigrateStateDir`, `StateDirNotice`.
  - `internal/harness/migrate_test.go`: the four cases, plus the notice.
  - `cmd/fyr/migrate_test.go`: 11 writing and 16 read-only invocations, plus the symlink refusal stopping `install` before any write.
  - `cmd/fyr/crossing_test.go`: `TestCrossingFromV270`, which builds v2.7.0 from `git archive v2.7.0` and installs it with a dream unit, then runs one `fyr install` (D14). It skips without the tag, `go`, `git` or `tar`, and under `-short`.
- **Go, changed:**
  - `cmd/fyr/main.go`: `writesState` and `migrateStateDir` run before the source is resolved and on the `--from-zip` branch, after its refusals. `installFiles` calls `dream.ReplaceOldTimer` after the install. `stderr` is a test seam.
  - `internal/dream/timer.go`:
    - the new names, with `oldUnitName`/`oldPlistName`;
    - `namesFor` behind `UnitFor` and `oldUnitFor`;
    - `timerUnit` (`unitOn`, `present`, `remove`), shared by `RemoveTimer` and the new `ReplaceOldTimer`;
    - `LegacyTimerFiles` and its notice now name the old bare names.
  - `internal/harness/release.go`: the asset name, `ReleaseRepo` and the marker.
  - `statedir_test.go`: exempts the `OldStateDir` constant's line.
- **Tests renamed or extended:**
  - `timer_test.go` (new names; `TestReplaceOldTimer`, seven cases);
  - `release_test.go` (`fyr-*`, `# fyr`, the old-marker refusal, `TestReleaseNames`);
  - `zip_test.go`, `cmd/fyr/zip_test.go` (release zips `fyr-vX.Y.Z.zip`);
  - `catalog_test.go` (the guard cases for `~/.config/sh-agent`).
- **Content:**
  - `skills/bug/SKILL.md:55` (BUG-176);
  - `skills/preflight/SKILL.md:31` ("an installed command that changes its name" is a MAJOR).
- **Docs:**
  - `deploy/remote.md` (§ Crossing to fyr; every living command, asset, marker and `--repo`; the timer example `fyr-update.service`; v3.0.0 as a MAJOR);
  - `specs/fyr/delivery.md` § The model (the crossing claim, two sentences);
  - `README.md` § Install without cloning and § Install from a zip;
  - `deploy/local.md:69`, `specs/fyr/dream.md:49` (the timer names and the replacement);
  - `testing/kit-preamble.sh`, `testing/guide.md` § 4 (the guard).
- **Trail:**
  - `bugs/BUG-176-*.md`, `bugs/BUG-177-*.md` and their `INDEX.md` rows;
  - the plan (D30, Phase 4b ticked);
  - `testing/2026-10-25-verify-fyr-crossing-local.md`.

## Agents dispatched
- **`documentation`**: `remote.md`, `delivery.md`, README and the two timer docs.
  - The main agent read its diff and changed two things in `remote.md` § Crossing to fyr. The opening restated part of the delivery claim, so it now points at it in one sentence (project `AGENTS.md` rule 6).
  - The checkout place's `go run ./cmd/fyr install` gained `--repo "$PWD"`. Without it, the dev box's home, last installed by the v2.7.0 release binary, is refused by `knownCheckout`.
- **No review dispatches**, as in Phases 2 to 4a: Phase 5 holds `verifier`, `reviewer --code --docs` and `security` over the whole feature. The main agent read its own diff.

## Verification
`testing/2026-10-25-verify-fyr-crossing-local.md`: PASS. In short:
- **Red first.** The new tests failed to compile, and with the migration test set aside, seven release and zip tests failed on behaviour.
- **Mutations.** Dropping `dream` from the writing verbs fails `TestStateDirMigrationByVerb`. Skipping `ReplaceOldTimer` fails `TestCrossingFromV270`. The preamble at HEAD fails the three new guard cases.
- **Full suite** under `-race`: exit 0.
- **The checkpoint crossing.** A scratch home was installed by a release-built v2.7.0 with a dream unit, behind a recording fake `systemctl`, then migrated by one `go run ./cmd/fyr install`. Result: 897 old files removed, the timer replaced, `fyr status` clean with `drift none`.
- **The release-step dry run** (`remote.md` steps 1 to 3, version v9.9.9, scratch) lists the three `fyr-*` binaries, `fyr-v9.9.9.zip` and `SHA256SUMS` with one marker line. The zip check printed CHECKED.
- **The real home.** `~/.config/sh-agent` and its units were untouched, and no `~/.config/fyr` was created.

## Deviations from the plan's boxes
Recorded as plan D30 (by: implementer, open to the operator's veto):
- **A symlinked `~/.config/sh-agent` refuses a writing verb.** Interfaces named only the plain-directory case.
- **The read-only notice goes to stderr**, and `migrated …` goes to stdout.
- **The timer replacement uses the installing shell's PATH.** A refused new unit keeps the old one and fails the install.
- **`LegacyTimerFiles` looks for the old bare names only.**
- **The bug-skill line says "names a tool-call budget"**, not the box's "a tool-call budget the dispatch names", which would repeat its subject.
- **The global preflight skill names the rule, not the tag.** `v3.0.0` is named in `deploy/remote.md` § Releases. Step 6's bridge check had no `sh-agent` wording to drop.

## Not built, named for Phase 5
- **`uninstall` and `dream --remove-timer` leave an old-named unit.** Before a crossing install it is named nowhere.
- **F4:** `status` does not name a leftover `sh-agent` on PATH. § Crossing to fyr checks it with `ls`.
- **No Antigravity behavioural run** of the changed skills (`bug`, `preflight`). Phase 5 and preflight's rule owe it.
