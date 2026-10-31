# Real `agy` 1.4.12 output

Captured 2026-10-31 on dave, in a sandbox home outside every checkout, with
only the home's path rewritten. `TestRealAgyShapes` reads every file.

- `permissions.json`, `config.json`, `hooks.json`: `agy -p /<name> --output-format json`.
- `turn.jsonl`: `agy --input-format stream-json --output-format stream-json --mode plan -p ""`,
  the prompt on stdin, the dream's call, answering with no tool.
- `tool.jsonl`: the same call, in a turn that ran a tool.

After an `agy` upgrade, capture these again into a directory named for the
version and point `realAgy` at it: a test over the old output cannot see a
new one's shape.
