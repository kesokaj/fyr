# Security Policy

## Supported Versions

Only the latest tagged `vX.Y.Z` release on `main` receives security updates.

| Version | Supported |
|---|---|
| Latest release (`v8.x`) | Yes |
| `< v8.0.0` | No |

## Reporting a Vulnerability

Do **not** open a public GitHub issue for security vulnerabilities.

Instead, report suspected vulnerabilities privately through [GitHub Private Vulnerability Reporting](https://github.com/kesokaj/fyr/security/advisories/new) on `github.com/kesokaj/fyr`. Include:

1. Affected component or package (`cmd/fyr`, `internal/harness`, `internal/search`, `internal/sandbox`, `internal/work`, `internal/auto`, `internal/redact`, or a skill/subagent definition).
2. Minimal reproduction steps or proof-of-concept input.
3. Impact assessment (for example: path traversal on `--from-zip`, symlink traversal during `fyr install`, prompt-injection fence escape in `internal/search/fence.go`, sandbox escape in `internal/sandbox` / `internal/work`, or credential redaction bypass in `internal/redact`).

## Security Model & Boundaries

- **Local-first by design (`PRD.md` R3, R7, R8):** `fyr` runs no background server, daemon, or telemetry service. Trail records (`~/.config/fyr/brain.db`), session metrics (`~/.config/fyr/metrics.db`), and AST/FTS5 indexes (`.agents/scratch/search.db`, `~/.config/fyr/search.db`) remain on the local machine (`0700` state directory).
- **Release integrity (`PRD.md` R9, `ADR-053`, `ADR-515`):** Release binaries (`fyr-linux-amd64`, `fyr-darwin-arm64`, `fyr-linux-arm64`) and source archives (`fyr-vX.Y.Z.zip`) are published on GitHub Releases with a version-stamped `SHA256SUMS` file. `fyr update` verifies both the `# fyr vX.Y.Z` header and the SHA-256 digest over HTTPS before replacing `~/.local/bin/fyr` or unpacking `--from-zip`.
- **Installer hardening (`internal/harness`):** `fyr install` refuses symlinked destinations (`O_NOFOLLOW` / `Lstat`), secret-shaped filenames (`.env*`, `*.pem`, `*.key`, credentials), unknown subagent tools, and unauthorized frontmatter keys (`disallowedTools` and permission overrides are rejected).
- **Untrusted data fencing (`internal/search/fence.go`):** Pre-invocation lifecycle hooks wrap retrieved records, memories, skill digests, and AST slices inside a per-turn cryptographic boundary (`=====<18-hex>=====`) marked as reference data, never instructions.
- **Structural secret redaction (`internal/redact`):** Before `fyr dream` or `fyr auto` learning tasks pass local transcripts, memories, or records to the configured CLI model, `internal/redact` removes the entire line, JSONL record, or `-----BEGIN`..`-----END` PEM block whenever a credential needle matches.
