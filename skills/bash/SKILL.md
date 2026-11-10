---
name: bash
description: "Bash and shell scripting expert. Use when writing, reviewing, or debugging Bash/Zsh scripts, automating tasks with shell commands, or using CLI tools like jq, rg, fd, fzf, yq. Do NOT use for general application backend code (use golang) or frontend code (use frontend-ui-modern, typescript)."
---

# Bash & Shell Scripting

Expert guidance for writing robust, maintainable, and secure shell scripts following modern best practices.

## When to Use

- Writing or reviewing Bash/Zsh scripts
- Generating interactive human-in-the-loop setup, credential, or migration wizards (`./references/wizard.md`)
- Automating tasks with shell commands
- Argument parsing, error handling, or signal traps
- Using CLI tools: `jq`, `rg`, `fd`, `fzf`, `yq`, `bat`
- POSIX portability questions

## When NOT to Use

- Application logic that belongs in Go or TypeScript
- Terraform/IaC configuration (use the `terraform` skill)
- CI/CD pipeline design beyond shell steps (use platform-specific docs)

## When to Stop Using Shell

Shell is the right tool for glue scripts and automation. Switch to Go when:

- The script exceeds **~100 lines** of non-trivial control flow (conditionals, loops, data transformation)
- **Performance matters** — shell is slow for anything compute-heavy
- You need **data structures** beyond arrays and strings (maps, sets, structs)
- **Error handling** requires more granularity than exit codes
- **Multiple developers** will maintain the script — shell is harder to read at scale

If the script is mostly calling other utilities with little data manipulation, shell is fine regardless of length.

## Core Rules

1. Always use `#!/usr/bin/env bash` and `set -euo pipefail`
2. Quote all variables: `"$variable"`, never `$variable`
3. Use `[[ ]]` for conditionals, `(( ))` for arithmetic
4. Use `local` for function variables, `readonly` for constants
5. Clean up with `trap cleanup EXIT`
6. Validate all inputs before use
7. Use `$()` for command substitution, never backticks
8. Never use `eval` — build commands with arrays instead

## Escapes survive tools

A file tool can turn an escape into its character (`\u0041` written as `A`). After writing text meant to keep an escape, `grep -n` for the escape itself, and write it through a script when the tool keeps converting it; a check for an escape builds its needle from character codes and never quotes the escape itself. After a heredoc or generated write, `git diff --numstat <path>`: a `-` in both columns means the file is BINARY and invisible to every reviewer. (Moved from the global context 2026-10-01.)

## Installed commands

An installed COMMAND is verified by the shell that will run it — the bare name, after `hash -r` (bash) or `rehash` (zsh), then `type <name>` when it disagrees — never by the file that was moved into place: an earlier `PATH` entry or the shell's command hash shadows it with every file-level check green. Before claiming a script is done, apply `review-standards` § 8 (the code-shape rules). (Moved from the global context 2026-10-01.)

## Reference Library

For detailed patterns, examples, and templates, consult the reference files:

* **Script Foundations, Variables, Error Handling, Functions, Conditionals, Loops, Arrays**
  Read → `./references/bash-fundamentals.md`

* **Testing, ShellCheck, Linting, CI Validation**
  Read → `./references/testing-linting.md`

* **Makefile Patterns, Targets, Conventions**
  Read → `./references/makefile-patterns.md`

* **Interactive Human-in-the-Loop Setup, Secret & Migration Wizards (`set_gcp_secret`, `gh secret`, `.env`)**
  Read → `./references/wizard.md` · Template → `./assets/wizard-template.sh`

> [!TIP]
> When the user asks you to write a shell script, immediately read the reference file to apply correct patterns for error handling, argument parsing, and cleanup.

## Further Reading

- [Google Shell Style Guide](https://google.github.io/styleguide/shellguide.html) — Google's internal shell conventions (used as a reference for function documentation and style)
