# The docs root

The one statement of how an agent finds a project's trail. The seven agents that write under it point here (`~/.gemini/config/skills/init-project/references/docs-root.md` as installed) instead of carrying their own copy; this skill scaffolds the tree it names.

Resolve it from the project root (an agent with a shell: `git rev-parse --show-toplevel`; one without: the directory the project's `AGENTS.md` sits in), before the first write:

1. `.agents/docs/` if that tree exists;
2. else a bare `docs/` at the project root if it exists (some repos still use one);
3. else create `.agents/docs/`.

Never create both trees. Create any missing directory under the resolved root (`bugs/`, `changes/`, `reviews/`, `testing/`, `plans/`) on first use. Every path written as `<docs-root>/…` in an agent definition means this root.
