---
owner: main
date: 2026-10-24
id: BUG-174
status: fixed
area: context/AGENTS.md (post-dispatch check) on Antigravity print mode
---
# BUG-174: on agy 1.4.6 the main agent chains the context's post-dispatch check into one command, the grant store denies it, and the run fails or idles to its print timeout

## Symptom
In fyr Phase 3a's behavioural rows (`testing/2026-10-24-verify-fyr-agents.md`), 4 of 12 attempts failed on the same auto-deny: `dispatch-bugs`, `dispatch-changelog`, `dispatch-documentation` attempt 1 and `agent-verifier` attempt 2. In each, the main agent ran `git status --short --untracked-files=all && git log @{u}..HEAD && git tag` (or the same joined with `;`, or with `|| git log -n 3`) as one command after the subagent returned, and agy's grant store, which allows `command(git)` for a simple git command only, denied it. Two rows then idled to the 20-minute print timeout with an empty reply. The agent under test had already written its artifact every time. Two related failures were a subagent chaining its own commands (`agent-reviewer` attempt 1; `agent-verifier` attempt 1, against the verifier's step 4).

## Repro
`bash .agents/scratch/fyr/kit/run.sh dispatch-documentation` with `INSTALL_FROM` set to a `git archive a2e6a5a` tree (the context before Phase 3a): 0 of 2, both denied on the chained check. On the Phase 3a tree: 1 of 2.

## Root cause
The context says: "After any dispatch that can write, `git status --short --untracked-files=all`, `git log @{u}..HEAD` and `git tag`". It names three commands but does not say "each its own command", and the model on agy 1.4.6 joins them. The 2026-10-07 rows on agy 1.2.14 passed with the same sentence (`testing/2026-10-07-verify-agy-release-rows.md`). Not determined: whether the CLI or its model changed. A `command(<name>)` grant does not cover a compound command line (BUG-141's store; guide § 4).

## Fix
Chosen 2026-10-25: (a), by the operator ("Go all approved"; plan D25), landing in fyr Phase 3b; status stays open until its rows pass. The options were: (a) the context line says the three are run as three separate commands (a `context/` change; it re-runs the canary and every dispatch row); (b) the kits grant `command(*)` on dispatch rows, which hides the defect a real place would hit; (c) leave it, so an Antigravity print-mode run that dispatches keeps failing about half the time.

## Prevention
An instruction that names several shell commands says that each is its own call, because the grant store allows simple commands only. A kit's auto-deny count is read per attempt and diagnosed from the conversation database before any grant is widened (BUG-141).

## Addendum 2026-10-25 — the root cause above was misread; fixed in fyr Phase 3b
With option (a) in place (the three commands run separately), agy 1.4.7 still auto-denied `git log @{u}..HEAD` run alone, in 6 of 6 dispatch rows, while chained lines of granted commands ran (`git log -n 5 --stat && git status --short --untracked-files=all`, dispatch-documentation steps 5–6). A probe row ran `git log --branches --not --remotes --oneline`, `git status -sb` and `git log origin/HEAD..HEAD --oneline`, each as its own call, and was denied only `git log @{u}..HEAD`. The store refuses the `@{u}` form under `command(git)`; chaining was never the cause, and every Phase 3a chained line held `@{u}`. A chain is denied when one of its commands has no grant (the verifier's `… && uname -sr`, `uname` ungranted), not because it is a chain.

Fix (plan D26, the operator's choice of 2026-10-25): `context/AGENTS.md` names `git log --branches --not --remotes --oneline`, which also works where no upstream is set, and keeps the "three separate commands" wording. Verified: the canary and all eight dispatch rows PASS on the Phase 3b tree, each row's store holding the new command and no `@{u}` (`testing/2026-10-25-verify-fyr-content.md`).

Prevention, replacing the one above: a command the context or a skill tells the agent to run is run once under the kit's real grants before it ships, because the grant store judges each command's form (`@{u}`, a `$( )` substitution) as well as its name; a denial is diagnosed from the conversation database's `error_details` (`permission check failed for unsandboxed "<command>"`) before a cause is written down.
