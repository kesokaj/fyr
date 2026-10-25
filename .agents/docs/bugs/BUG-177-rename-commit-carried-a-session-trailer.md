---
owner: main
date: 2026-10-25
id: BUG-177
status: fixed
area: process (commit message, Claude Code session)
---
# BUG-177: the Phase 4a rename commit was first written with a `Claude-Session:` trailer

## Symptom
In fyr Phase 4a the main agent (Claude Code) wrote the rename commit with a `Claude-Session: https://claude.ai/code/session_…` trailer. The global git rule forbids it: the author comes from the local git config only, with no session links and not the trailer a CLI's own reminder asks for. The commit was amended before the push, so the pushed `c565b27` has no trailer. It still counts under the bug skill's Step 0, because it reached a commit (`changes/2026-10-25-1039-fyr-phase4a-rename-in-the-tree.md` § Deviations; the Phase 4a handoff's agent notes).

## Repro
A Claude Code session whose system reminder asks for a `Claude-Session:` line at the end of each commit message. The agent writes the message from that reminder and not from the global context's § Git.

## Root cause
Two instructions disagreed about the same lines. The CLI's reminder came later in the conversation and was more specific, and the agent followed it over the context rule, which says that reminder defers to it. Same class as BUG-039, the `Co-Authored-By` trailer that headless Claude Code commits carried. ADR-062 removed the harness's Claude Code setting for that one, so no setting covers this trailer.

## Fix
`c565b27` was amended before the push: `git log -1 --format=%B c565b27` holds no trailer. No code changed. The rule was already in `context/AGENTS.md` § Git and was not followed.

## Prevention
Before every push, `git log --format=%B --branches --not --remotes` is read for `Claude-Session:` and `Co-Authored-By:` lines, and a hit is amended before the push. A commit message is written from the context's § Git, never from a CLI's reminder.
