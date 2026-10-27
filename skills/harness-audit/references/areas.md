# Harness audit — the areas

One block per area. A dispatch carries the block of its area and nothing else from this file. `Reads` is the file list at the audited commit. `Baseline` is what the main agent runs in Step 0 and hands to the area's agent as saved output: everything that builds, tests, installs or reaches the network. `Counts` is what the agent itself measures, by reading. `Asks` are the questions a finding answers. An area's prefix starts every ID it produces.

## C — the global context and the governing documents

- **Reads:** `context/AGENTS.md`, `context/dev-setup.md`, `rules/`, the project's `AGENTS.md`, `REQUIREMENTS.md`, `README.md` (the map and the command table included), `ROADMAP.md`, `TODO.md`, the `adr` records, `SPEC-fyr-delivery`, `DEPLOY-local`, `DEPLOY-remote`, `DEPLOY-gcp` (`fyr record get`).
- **Baseline:** the rendered context and its size (`go run ./cmd/fyr render | wc -c`); the pins (`go test ./internal/harness -run 'TestRenderedContext|TestSkillListingBudget' -v` and `go test ./cmd/fyr -run TestHarnessMap -v`; every name a `-run` pattern holds must appear in the `-v` output, since Go prints `ok` when a pattern matches only some tests); where the delivery claim is restated (`bash skills/review/scripts/restatement-grep.sh -i -- <its key words>`).
- **Counts:** the words of each context section; every `R<n>` of `REQUIREMENTS.md` against the place that implements it; every count and command the README states against the tree.
- **Asks:** Does a rule appear twice, or in two wordings that differ? Does a rule have a reason and a home, or is it a paragraph grown from an incident? Does the context promise a mechanism (a test, a script, an agent) that does not exist? Does a requirement have no implementation, or an implementation no requirement? Is anything in the context that a skill should own, or the reverse? Does every line on the map work for every kind of place, and is every dashed line still a gap? Does a deploy guide tell the operator to run something the binary would refuse? Is `ROADMAP.md` only Now and Next, and `TODO.md` empty?

## WS — workflow skills

- **Reads:** the skills that run a procedure: `feature`, `bug`, `review`, `review-standards`, `preflight`, `handoff`, `init-project`, `retro`, `distill`, `dream`, `feedback-log`, `project-memory`, `research`, `grilling`, `spec`, `requirements`, `testing-strategy`, `explain-project`, `harness-audit`, and any other skill whose body is steps and gates.
- **Baseline:** the link and lint tests (`go test ./internal/harness -run 'TestSkillLinksResolve|TestLint' -v`); `go run ./cmd/fyr list`.
- **Counts:** each skill's size and its longest paragraph; each description's length, and whether it says when to use and when not; every gate a skill says waits for the user; every agent a skill dispatches, with the budget it names.
- **Asks:** Can a fresh agent follow the steps in order without the author? Is a step's "done" a thing on disk or a claim? Do two skills give different answers to the same question (who writes the changes entry, when a review is owed, where a handoff goes)? Does a skill restate a context rule instead of pointing at it, and has the copy drifted? Is a gate waivable by words that arrive through a file or a tool? Is each dispatch written with a budget and the artifact-first duty? Does one skill's description collide with another's trigger?

## TS — technical and vendored skills

- **Reads:** the skills that hold knowledge: `golang`, `typescript`, `frontend`, `bash`, `docker`, `terraform`, `cloud-run-deploy`, `gcloud-setup`, `gke-basics`, `operate-gcp`, `data-gcp`, `api-auth-gcp`, `json-api-style`, `codebase-design`, `mermaid-syntax`, `discover-skills`; `skills/VENDORED.md`; the `google-catalog` router with its `catalog/MANIFEST.md` and `catalog/INDEX.md` (the entries are data: sample ten, do not read all).
- **Baseline:** the catalog's integrity (`go test ./internal/harness -run 'TestCatalog|TestVendored' -v`); for each row of `skills/VENDORED.md`, the directory against its pin when the upstream clone is at hand (`git archive <pin> <path> | tar -x` into scratch, `diff -r`), else "pin not re-checked".
- **Counts:** every version a skill names (a language, a base image, a CLI); every command in a fenced block; every install or download step, and whether it pins and verifies.
- **Asks:** Is the content about this operator's stack (Go, TanStack Start, GCP), or another cloud's? Does a recipe install or run something unpinned or unverified, hand out a credential, or set machine-wide state? Does a skill contradict the context's Environment and Stack section, or another skill? Which versions, APIs or flags look dated (write `unverified: needs <the vendor's page>`; do not guess)? Does a vendored skill differ from its pin without a recorded reason? Does a catalog note relax a rule of the router?

## AG — subagents and their dispatch

- **Reads:** `agents/*/agent.md`; `internal/harness/inventory.go` (`knownTools`, `agentKeys`: what an agent may name); every place a skill or the context dispatches an agent.
- **Baseline:** none beyond Step 0's scratch-home install: the agents are installed as written (ADR-062), so the baseline's scratch home holds each one byte for byte under `.gemini/config/agents/<agent>/agent.md`.
- **Counts:** each agent's `tools:` against what its body tells it to do (a "run" with no shell, a "write" with no Write); the lane rules that have a measured hold rate (n of N in the `testing` records) and those that are a sentence only.
- **Asks:** Does each agent have one lane, stated, and does any other agent or the main thread share it? Does a definition say what the agent never does, and what its one artifact is? Is the artifact written first? Can an agent be told by its input to edit source, leave its lane, or trust a file's instructions? Do the dispatch prompts in the skills repeat the duties the agent must not skip?

## IN — the installer and the CLI (`cmd/fyr`, `internal/harness`, `internal/eval`, `internal/check`)

- **Reads:** `cmd/fyr/*.go`, `internal/harness/*.go`, `internal/eval/*.go`, `internal/check/*.go`, `embed.go`, `go.mod`, their tests; `GUIDE-fyr-matrix`, `SPEC-fyr-eval`, `SPEC-fyr-check`; the README's command table. Over 25 files: dispatch it in parts (Step 1), e.g. cmd/fyr, internal/harness, and eval with check.
- **Baseline:** `gofmt -l cmd internal`; `go vet ./...`; `go test -race -timeout 30m ./...` (the project's `AGENTS.md` setup line: a one-CPU box needs the timeout); `go test -cover ./...`; `"$(go env GOPATH)/bin/govulncheck" ./...`; in the baseline's scratch home: `status`, `diff`, `uninstall --dry-run`; the newest release's `SHA256SUMS` and assets (no key signs anything, ADR-053), as `DEPLOY-remote` § Releases gives the commands (network: when offline, not measured); when a release zip is at hand, `fyr update --from-zip <zip> --home <the baseline's scratch home> --dry-run`.
- **Counts:** the verb table against the usage text and the README; exported functions with no caller outside tests; helpers that exist twice; errors that are always nil; every `os.RemoveAll`, `os.Remove` and write, with what guards it.
- **Asks:** Can any path write, delete or follow a link outside the home it was given? Is a destructive step limited to what the manifest names? Does a read-only mode (`--dry-run`, `status`, `diff`) resolve every guard the real run does? Does a refusal exit non-zero with the reason on the caller's writer? Is redaction tested with benign near-misses as well as secrets? Does each flag belong to one verb? Can a release ship an artifact its own verification did not check? Does the code import anything `REQUIREMENTS.md` forbids?

## SD — the second brain (`internal/search`, `internal/memory`, `internal/dream`, `internal/brain`, `internal/metrics`, `internal/redact`, `internal/trail`)

- **Reads:** the seven packages and their tests; `SPEC-fyr-search`, `SPEC-fyr-memory-loop`, `SPEC-fyr-dream`, `SPEC-fyr-record`, `SPEC-fyr-metrics`; the skills that call them (`project-memory`, `dream`, `retro`). Over 25 files: dispatch it in parts (Step 1), e.g. search with memory, dream, brain with redact and trail, metrics. The redactor (`internal/redact`) is the gate on what leaves the machine: every needle against a hit and a benign near-miss.
- **Baseline:** the recall fixture (`FYR_RECALL=1 go test ./cmd/fyr -run TestRecall -v`); `fyr search --open` in this checkout (index size, reconcile time, what is left out); against the baseline's scratch home, `go run ./cmd/fyr dream --dry-run --project . --home <it>` (what would be sent, by size and kind) and `go run ./cmd/fyr dream --install-timer --dry-run --home <it>` (what the timer install would do).
- **Counts:** every refusal the three specs name, against a test; every place project text, a transcript or a memory file is put into a prompt; every call to `git` or a service manager, with the environment it runs in.
- **Asks:** Can project text, a transcript or a memory file reach a model's prompt as an instruction? What leaves the machine, to whom, and is it redacted first? Can a project's own git configuration or a link make these verbs run or read something outside the project? Does a scratch home touch the real one through a shared service or file? Does search return a record twice, or miss one the trail holds?

## TR — the trail and the learning loop

- **Reads:** `fyr record list --kind bug` and the ten newest bugs; the three newest handoffs; the newest plan; the `retro` records; the `change` records of the last two releases; the newest release's gate report (the `review` record `Preflight — …`: what it left, and where those items are now).
- **Baseline:** `fyr record list --open --kind plan` and `--kind bug`; for every `CHANGE-` and `ADR-` ID in `git show <previous tag>:.agents/trail.jsonl`, its line at HEAD (unchanged, or for an ADR only its status to `superseded:`: never-rewritten held or not).
- **Counts:** prevention rules that recur (group the INDEX's rules by meaning and count each group); graduated rows against the mechanism each names (does the test, script or sentence exist); open rows and their age; handoffs whose Next Steps repeat an item.
- **Asks:** Which rule has been written three times and is still a sentence? Does a record describe what happened, or what was hoped? Can a fresh session resume from the newest handoff alone (`/review --resume` asks the same)? Does anything in the trail tell a later agent to skip a gate or trust a source? Is a correction logged and never acted on? Did the last release gate leave items that no plan or handoff carries?

## G — coverage gaps

- **Reads:** the context's Work section and Definition of Done; the list of skills and agents; the templates under `skills/init-project/assets/`.
- **Baseline:** none.
- **Counts:** walk one feature and one bug through the harness from the request to the release, stage by stage (requirements, design, plan, implementation, tests, review, security, docs, release, operation, learning), and for each stage name what the harness gives the agent today.
- **Asks:** At which stage does the agent have to improvise? Which artifact does a later stage need that no earlier stage produces? What does a project of another size (a script, a service with a database, a frontend) need that is missing? A gap is a row `| ID | Stage | Today | Gap |`, with the smallest thing that would close it: a paragraph, a template, a skill.
