---
trigger: model_decision
description: "Apply when touching harness delivery paths, README How it works map, install/update/release behaviour, SPEC-fyr-delivery, or TestHarnessMap."
---

# Delivery map contract

The map in `README.md` § How it works is the contract for how the harness reaches a machine and how experience comes back:

1. **Three ways in, no way back, nothing else.** A place gets the harness from a checkout kept current with `fyr update`; from a release binary with the harness built in (`fyr install`, updated by `fyr update`); or as a zip place via a hand-downloaded repository zip (`fyr update --from-zip <file>`). Experience stays on its machine; harness-target proposals reach this checkout only via `/fyr-retro --harness` or an eval-gated `fyr auto` commit pushed by the operator (`R9`, `ADR-090`, `ADR-062`). No service, push to machines, project-vendored copy, or transfer timer (`R8`, `ADR-030`, `ADR-033`, `ADR-038`, `ADR-062`). Dream (`ADR-047a`, `ADR-073`) and `fyr auto` timers (`ADR-090`) send only by-design content to the CLI's own model provider (redacted dreamer/learning inputs, `fyr eval` / `fyr work` sandboxes) or skills.sh/npm (`missing-skill`, `ADR-065`, `ADR-076`); proposals wait in `~/.config/fyr/brain.db`.
2. **A tag is a release.** `/fyr-preflight vX.Y.Z` makes an annotated tag and publishes binaries, a tree zip, and checksums — unsigned (`ADR-053`); `SPEC-fyr-delivery` states what each place fetches (`ADR-033`).
3. **Changing a path is a design question.** Adding, removing or rerouting any line on the map goes to the operator first, gets an ADR, and lands in ONE commit with code, docs and map (`/fyr-review --docs` checks it).
4. **Dashed means a gap, and it stays honest.** Dashed lines mean manual or single-machine until fixed for every place; only the olive provider lines stay dashed (`ADR-049`, `ADR-065`, `ADR-076`, `ADR-090`).
5. **The map is pinned by a test.** `TestHarnessMap` fails if the map is missing, names a nonexistent verb, omits `install`/`update`/`dream`/`auto`, or names `report` (`ADR-516`) or the feedback repo. Any interactive artifact page is a derived view, never the source.
6. **The delivery claim has one home.** Stated once in `SPEC-fyr-delivery` § The model; other docs link in one sentence and never restate it (`RETRO-2026-09-18-retro-2`).
