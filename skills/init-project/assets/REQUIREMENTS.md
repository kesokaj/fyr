# Requirements — <project>

<!-- Read before every significant change. Immutable without the user's explicit approval of the exact text:
     propose the edit → approval → ADR → REQUIREMENTS.md and ROADMAP.md in one commit (global context § Hard rules).
     One sentence per item, each with a provenance line: who asked, when, and where the requirement is enforced
     (a test, a rule, a guide). The `requirements` skill elicits and traces these; it never writes "approved" itself. -->

## Must — non-negotiable

<!-- The project is wrong without these. Numbered R1…; a later edit keeps the numbers (a struck item stays as "R3 — struck <date>: <why>"). -->

- **R1** — <one sentence>.
  <!-- provenance: <who>, <date>; enforced by: <path[:N] | TestName | record ID>, … -->
- **R2** — <one sentence>.
  <!-- provenance: …; enforced by: … -->

## Should — expected unless a decision says otherwise

<!-- S1…: a deviation needs a D-row in the active plan, not an ADR. -->

- **S1** — <one sentence>.
  <!-- provenance: …; enforced by: … -->

## Could — welcome when cheap

<!-- C1…: nobody blocks on these; a plan may take one as a Follow-up. -->

- **C1** — <one sentence>.

## Non-goals

<!-- What this project deliberately does not do, one line each, so nobody adds it by accident. -->

- <one line>.

## Known limits

<!-- Facts about the environment or the platform the project lives with (a size cap, a rate limit, a CLI quirk), each with its source. -->

- <one line> — <source>.

<!-- Targets a service must meet (concurrency, p99 latency, data volume) are Must or Should items like any other,
     one sentence each with the number in it and the test, or the path or record of the dashboard, that measures it as "enforced by";
     a table is added only when there are five or more. A CLI, a library or a tool has none. -->
