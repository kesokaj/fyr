"""Run /fyr-preflight release gate checks in parallel via run_workflow."""

import json
import re


def _safe_slug(raw, fallback):
    text = re.sub(r"[^A-Za-z0-9._-]+", "-", str(raw or "").strip())
    text = re.sub(r"\.{2,}", "-", text).strip(".-")
    return text or fallback


def _as_bool(raw, default=False):
    if raw is None:
        return default
    if isinstance(raw, bool):
        return raw
    if isinstance(raw, str):
        norm = raw.strip().lower()
        if norm in ("false", "0", "no", "off", ""):
            return False
        if norm in ("true", "1", "yes", "on"):
            return True
    return bool(raw)


def _norm_token(raw):
    return re.sub(r"[\s-]+", "_", str(raw or "").strip().upper())


STAGE_SCHEMA = {
    "type": "object",
    "properties": {
        "stage": {"type": "string", "enum": ["reviewer", "security", "verifier"]},
        "agent": {"type": "string"},
        "status": {
            "type": "string",
            "enum": ["COMPLETE", "PASS", "FAIL", "BLOCKED", "INCOMPLETE"],
        },
        "verdict": {
            "type": "string",
            "enum": [
                "APPROVE",
                "APPROVE_WITH_NITS",
                "REQUEST_CHANGES",
                "CLEAR",
                "FINDINGS",
                "PASS",
                "FAIL",
                "BLOCKED",
            ],
        },
        "blocking": {"type": "boolean"},
        "artifact_path": {"type": "string"},
        "not_examined": {"type": "string"},
        "summary": {"type": "string"},
    },
    "required": [
        "stage",
        "agent",
        "status",
        "verdict",
        "blocking",
        "artifact_path",
        "not_examined",
        "summary",
    ],
}


def _reviewer_prompt(scope_text, version, date):
    artifact = f".agents/scratch/agents/reviewer-{date}-preflight-{version}.md"
    return (
        f"Preflight release reviewer (`--code --docs`) for {version} on {date}.\n"
        f"Range reviewed: {scope_text}\n\n"
        "Instructions:\n"
        "- Review the release range against `review-standards` (correctness, dead code, "
        "simplicity, decoupling, rule adherence) AND include a `## Docs lens` section "
        "comparing changed behavior/interfaces against README.md, SDD.md, TDD.md, "
        "SPEC/GUIDE/DEPLOY records, and `fyr check` output.\n"
        "- Set `blocking: true` in your `finish` result ONLY if verdict is REQUEST_CHANGES, "
        "there is any high-severity code/correctness finding, or status is not COMPLETE "
        "(docs gaps are advisory and non-blocking).\n\n"
        "Constraints:\n"
        "- Report only: do NOT edit source, tests, docs, or config.\n"
        "- Budget: at most 30 tool calls. Write `.agents/scratch/agents/...` early with "
        f"`Status: IN PROGRESS` at `{artifact}`, update as you go, finish with "
        "`## Status: COMPLETE` and `## Not examined`, then MUST call `finish` with "
        "`{stage: 'reviewer', agent: 'reviewer', status, verdict, blocking, artifact_path, "
        "not_examined, summary}`."
    )


def _security_prompt(scope_text, version, date):
    artifact = f".agents/scratch/agents/security-{date}-preflight-{version}.md"
    return (
        f"Preflight release security audit for {version} on {date}.\n"
        f"Range reviewed: {scope_text}\n\n"
        "Instructions:\n"
        "- Read EVERY changed path in the release diff end to end (trail markdown included "
        "for secrets, credentials, private data, and prompt-injection text) plus its blast "
        "radius (auth, crypto, secrets, IAM, input parsing, path safety, dependencies).\n"
        "- List anything skipped under `## Not examined`. Any release-diff path left "
        "unexamined or any high-severity security finding MUST set `blocking: true`.\n\n"
        "Constraints:\n"
        "- Defensive review only: never build exploits and never edit source.\n"
        "- Budget: at most 30 tool calls. Write early with `Status: IN PROGRESS` at "
        f"`{artifact}`, finish with `## Status: COMPLETE` and `## Not examined`, then MUST "
        "call `finish` with `{stage: 'security', agent: 'security', status, verdict, "
        "blocking, artifact_path, not_examined, summary}`."
    )


def _verifier_prompt(scope_text, version, date, verify_tiers):
    artifact = f".agents/scratch/agents/verifier-{date}-preflight-{version}.md"
    tiers_line = f"\nTiers / commands owed:\n{verify_tiers}\n" if verify_tiers else "\n"
    return (
        f"Preflight live e2e verification for {version} on {date}.\n"
        f"Range reviewed: {scope_text}\n"
        f"{tiers_line}\n"
        "Instructions:\n"
        "- Run the live e2e/smoke/container tiers owed on the live tree (`fyr verify` "
        "covers gofmt, go vet, go test -race, and fyr check); write `# Verification — "
        f"preflight-{version}` and `## Status: PASS|FAIL|BLOCKED` at `{artifact}` after "
        "your first command and update after each command.\n"
        "- Set `blocking: false` only when `status == 'PASS'` and all required tiers ran.\n\n"
        "Constraints:\n"
        "- Report only: never modify tests or source.\n"
        "- Budget: at most 30 tool calls. Include `## Not examined` and MUST call `finish` "
        "with `{stage: 'verifier', agent: 'verifier', status, verdict, blocking, "
        "artifact_path, not_examined, summary}`."
    )


base_ref = str(args.get("base_ref", "")).strip()
head_ref = str(args.get("head_ref", "HEAD")).strip() or "HEAD"
scope_text = str(args.get("scope_text", "")).strip()
if not scope_text:
    if not base_ref:
        raise ValueError("args.scope_text or args.base_ref is required")
    scope_text = (
        f"Commits {base_ref}..{head_ref} (git log --oneline {base_ref}..{head_ref}); "
        f"diff is git diff {base_ref}..{head_ref}."
    )

version = _safe_slug(args.get("version"), "candidate")
date = _safe_slug(args.get("date"), "today")
run_reviewer = _as_bool(args.get("run_reviewer"), True)
run_security = _as_bool(args.get("run_security"), True)
run_verifier = _as_bool(args.get("run_verifier"), False)
verify_tiers = str(args.get("verify_tiers", "")).strip()

parallel_specs = []
if run_reviewer:
    parallel_specs.append({
        "stage": "reviewer",
        "type_name": "reviewer",
        "role": f"Preflight reviewer ({version})",
        "prompt": _reviewer_prompt(scope_text, version, date),
    })
if run_security:
    parallel_specs.append({
        "stage": "security",
        "type_name": "security",
        "role": f"Preflight security ({version})",
        "prompt": _security_prompt(scope_text, version, date),
    })

stages = []
if parallel_specs:
    phase("Parallel review and security")
    stages = await parallel(
        parallel_specs,
        lambda s: agent(
            s["prompt"],
            schema=STAGE_SCHEMA,
            role=s["role"],
            type_name=s["type_name"],
            workspace="inherit",
            model="pro",
        ),
    )

def _stage_blocked(r):
    stage = _norm_token(r.get("stage")).lower()
    status = _norm_token(r.get("status"))
    verdict = _norm_token(r.get("verdict"))
    if stage not in ("reviewer", "security", "verifier"):
        return True
    if bool(r.get("blocking")):
        return True
    if status in ("FAIL", "BLOCKED", "INCOMPLETE"):
        return True
    if verdict in ("FINDINGS", "REQUEST_CHANGES", "FAIL", "BLOCKED"):
        return True
    if verdict not in ("APPROVE", "APPROVE_WITH_NITS", "CLEAR", "PASS"):
        return True
    if stage in ("reviewer", "security") and status != "COMPLETE":
        return True
    if stage == "verifier" and status != "PASS":
        return True
    return False


prior_blocked = any(_stage_blocked(r) for r in stages)

if run_verifier and not prior_blocked:
    phase("Live e2e verification")
    v_res = await agent(
        _verifier_prompt(scope_text, version, date, verify_tiers),
        schema=STAGE_SCHEMA,
        role=f"Preflight verifier ({version})",
        type_name="verifier",
        workspace="inherit",
        model="pro",
    )
    stages.append(v_res)

overall_blocked = any(_stage_blocked(r) for r in stages)
gate_verdict = "NO-SHIP" if (not stages or overall_blocked) else "SHIP"

for res in stages:
    log(
        f"[{res.get('stage')}] status={res.get('status')} verdict={res.get('verdict')} "
        f"blocking={res.get('blocking')} -> {res.get('artifact_path')}"
    )

print(
    json.dumps(
        {
            "version": version,
            "date": date,
            "gate_verdict": gate_verdict,
            "stages": stages,
        },
        indent=2,
    )
)
