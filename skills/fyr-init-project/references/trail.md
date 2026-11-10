# The trail: records, and an agent's draft

The one statement of where a project's trail lives and how an agent's artifact reaches it. The nine agents point here (`~/.gemini/config/skills/fyr-init-project/references/trail.md` as installed) instead of carrying their own copy.

## Records

A project's trail is records in `fyr`'s brain store (`~/.config/fyr/brain.db`): plans, reviews, test logs (`testing`), changes, handoffs, bugs, ADRs, retros, learnings, specs, deploy docs and guides. They are written and read with `fyr record add|get|edit|list`, searched with `fyr search`, exported on demand to `.agents/trail.jsonl` with `fyr record export`, and loaded on another machine with `fyr record sync`. A project whose trail is still a `docs/` or `<project>/.agents/docs/` tree is migrated with `fyr record import` first.

Reading: `fyr record list [--kind K] [--open]` (newest first), `fyr record get <ID>`, `fyr search <words>`, `fyr search --refs <ID|path>`.

## An agent's artifact: a draft in scratch

An agent never runs `fyr record add`. It writes its artifact as a draft file and the main thread stores it (brain plan D12):

1. From the project root (`git rev-parse --show-toplevel`; without a shell, the directory the project's `AGENTS.md` sits in), `mkdir -p .agents/scratch/agents`.
2. Write the draft EARLY to `.agents/scratch/agents/<agent>-<stamp>.md`, `<stamp>` from `date +%F-%H%M` (never memory), with `Status: IN PROGRESS`; update it as you go. A draft cut off at a turn limit survives there.
3. The draft's first line is its title as a `# ` heading; no frontmatter (a record carries its own kind, status and date).
4. Reply with the draft's path, named only after the `write_to_file` call succeeded.

The main thread then stores it: `fyr record add <kind> --title "<title>" --body-file .agents/scratch/agents/<agent>-<stamp>.md`, and cites the ID it prints.

| Agent | Kind it drafts |
|---|---|
| reviewer, security | `review` |
| verifier | `testing` (and, when it writes or fills the testing guide, a second draft for `GUIDE-testing`) |
| explorer, triage | `plan` (a recon or triage brief: no checkboxes outside fenced blocks, so it is stored closed) |
| bugs | `bug` |
| changelog | `change` |
| dreamer | `retro` |
| documentation | edits files; a spec, deploy doc or guide that is a record gets a draft of its new body, stored with `fyr record edit <ID> --body-file <path>` |
