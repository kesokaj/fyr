---
owner: main
date: 2026-10-25
id: BUG-176
status: fixed
area: skills/bug (Step 5 dispatch wording)
---
# BUG-176: `skills/bug/SKILL.md:55` kept "a tool-call budget below the agent's `maxTurns`" past Phase 3b

## Symptom
Plan `2026-10-24-fyr-agy-only.md` D17 says every phrase "a budget below the agent's `maxTurns`" in the context and the skills becomes a tool-call budget the dispatch names, because Antigravity's `agent.md` has no `maxTurns` key. Phase 3b (`bece857`) changed the other sites and missed one: `skills/bug/SKILL.md:55` still said a dispatch to `verifier`, `reviewer` or `security` "names a tool-call budget below the agent's `maxTurns`". The phrase reached `bece857`, `c565b27` and every commit after them. The Phase 4a handoff named it as an agent note.

## Repro
`git grep -n 'maxTurns' -- context skills agents rules ':!skills/google-catalog/catalog'` at `c2810dd` prints `skills/bug/SKILL.md:55`.

## Root cause
Phase 3b's sweep matched "a budget below", while this line read "a tool-call budget below", so the pattern missed it. No check ran over the key's name, `maxTurns`, which is what every such sentence has in common.

## Fix
`skills/bug/SKILL.md:55` now says the dispatch "names a tool-call budget and repeats …", the wording `context/AGENTS.md:35` uses. The plan box asked for "a tool-call budget the dispatch names", but in this sentence the dispatch is already the subject, so that wording would repeat itself. Verified by the Repro grep, now empty.

## Prevention
A sweep that removes a concept greps for the concept's own name (the key, the flag, the verb) across the living tree, not for one phrasing of the sentence that used it; the sweep's verification line is that grep, empty.
