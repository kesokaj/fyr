# Spec — <app>: <topic>   (YYYY-MM-DD)

Status: draft

<!-- Track 3: shared human + agent surface contract (machine-projectable). No YAML frontmatter.
     Status: draft (being written) | agreed (the user approved this text). Record status is current;
     retire superseded or obsolete specs (`fyr record edit <ID> --status retired`).
     One topic per record: an integration, storage format, CLI surface, or protocol.
     Required headings enforced by fyr check (spec-structure) and projected by fyr search --contracts:
     Purpose, Interfaces, Data, Failure modes (4-column table), Verification. -->

## Purpose

<!-- BLUF (1-3 active sentences): who calls this surface, what it does, and what it is not. -->

## Interfaces

<!-- Concrete shapes, signatures, flags, config keys, or environment variables so two implementers converge.
     Put each signature or wire shape in its own fenced block; use a Markdown table for flags/keys (fold > 12 rows in <details>). -->

```text
<signature, command, or wire shape>
```

## Compatibility

<!-- Who consumes this surface today, whether changes are additive, and the migration path for any breaking change. -->

## Data

<!-- Schemas, tables, files, and invariants as concise bullets (uniqueness, ordering, immutability, owning writer/migration). -->

- `<store or schema>`: <invariant and owning writer>

## Failure modes

<!-- What callers observe on each failure. Keep the 4-column header below so fyr check (spec-structure) and fyr search --contracts project failure contracts deterministically. -->

| Trigger | Observable result | Retry | Action |
|---|---|---|---|
| `<failure condition>` | `<HTTP status / exit code / error body / log line>` | `<yes / no / backoff>` | `<caller or operator recovery step>` |

## Non-goals

<!-- What this surface deliberately excludes so later readers do not add it by accident. -->

## Open questions

<!-- One line per open gap ending in the default taken until answered; delete once settled and move the answer to Interfaces, Data, or an ADR. -->

## Verification

<!-- Test files, test names, fixtures, and commands that pin every interface, data invariant, and failure mode.
     A section nothing can verify is design, not spec: move it to a plan or ADR. -->
