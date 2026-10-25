---
owner: main
date: YYYY-MM-DD
status: draft
---
# Spec — <app>: <topic>   (YYYY-MM-DD)

<!-- status: draft (being written) | agreed (the user said yes to this text) | superseded (a newer spec names it) -->
<!-- One topic per file: an integration, a storage format, a CLI surface, a protocol. Keep it to what a reader needs to build or check the thing; the plan holds the phases, the ADR holds the why. -->

## Purpose

<!-- Two or three sentences: who uses this surface, for what, and what it is not. -->

## Interfaces

<!-- Request and response shapes, flags, config keys, environment variables — concrete enough that two implementers converge. One fenced block per shape; a table for flags and keys (name, type, default, meaning). -->

## Data

<!-- Schemas, tables, files, and the invariants that must hold (uniqueness, ordering, what is immutable). Name the migration or the writer that owns each. -->

## Failure modes

<!-- What the caller sees when each part fails: status, error body, exit code, log line. Each row names the trigger and the observable result. -->

## Non-goals

<!-- What this surface deliberately does not do, so a later reader does not add it by accident. -->

## Open questions

<!-- Each as a line ending in the default the implementer takes until the user answers; delete the line when it is settled and record the answer where it belongs (Interfaces, Data, or an ADR). -->

## Verification

<!-- How a reader checks this spec against the code: the test files that pin each interface, the command that exercises each failure mode, the fixture the data invariants are checked on. A section the code cannot be checked against is design, not spec. -->
