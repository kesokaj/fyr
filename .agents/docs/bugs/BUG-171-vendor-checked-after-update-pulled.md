---
owner: main
date: 2026-10-12
id: BUG-171
status: fixed
area: cmd/sh-agent (the --vendor flag, every verb)
---
# BUG-171: `update --vendor bogus` pulled the checkout before refusing, and other verbs exited 1 or 0

## Symptom
Found by the v2.7.0 release gate's reviewer (`reviews/2026-10-12-review-v2.6.2-13c5bde-code-docs.md`, finding 1, low), and widened by the main agent's probe of every verb.
- **update on a checkout:** `update --vendor bogus` fetched and pulled first, then failed with exit 1.
- **update under `--from-zip`:** exit 1.
- **update under `--dry-run`:** the vendor was never checked, exit 0.
- **status:** exit 1.
- **uninstall and report:** they took any word, exit 0.

BUG-168's fix (`3bffdcf`) had made usage errors exit 2 at seven sites. It missed this class.

## Repro
- `TestUpdateRefusesABadVendorFirst` (`cmd/sh-agent/main_test.go`) uses a fixture checkout one commit behind its local origin. Before the fix:
  - on the checkout path, it printed "pulled to …" and exited 1, and the checkout moved;
  - the dry run exited 0;
  - the zip path exited 1.
- `TestUsageErrorsExit2`'s rows `status`, `uninstall --dry-run` and `report --dry-run --vendor bogus` failed.

## Root cause
`--vendor` is registered on every verb's FlagSet (`main.go`, `run`). Each verb resolved it where it first needed it, if at all. `update` needed it only for the install it runs after the pull, `status` returned the error unwrapped (exit 1), and `uninstall` and `report` never read it. A flag every verb has was validated by none of them up front.

## Fix
`cmd/sh-agent/main.go` `run`: right after the home is made absolute, a non-empty `--vendor` goes through `c.vendor()`, which prints the reason and returns exit 2, before any verb runs. One site covers every verb. `.agents/docs/deploy/local.md` says so.

## Prevention
A flag registered on every verb is validated once, before the verb dispatch, not where each verb first reads it. A usage check that sits after a side effect (a fetch, a pull, a write) is a bug even when its exit code is right, and the read-only mode of a verb resolves the same check (review-standards § 8).

## Since
2026-10-25: `--vendor` left with the second vendor (plan `2026-10-24-fyr-agy-only.md`), and `TestUpdateRefusesABadVendorFirst` left with it. `TestUpdateRefusesTheVendorFlagFirst` (`cmd/fyr/main_test.go`) holds the same order: any `--vendor` is a usage error, exit 2, before `update` fetches or pulls, on the checkout, dry-run and zip paths. `TestUsageErrorsExit2` covers the other verbs (audit TR12).
