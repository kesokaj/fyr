"""Shared library for fyr workflow scripts (cross-family routing, headless agy execution, and metrics recording)."""

import asyncio
from datetime import datetime, timezone
import json
import os
import re
import shutil
import subprocess
import time

MODEL_ID_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$")
PICK_TIMEOUT_SEC = 15
MAX_HEADLESS_PROMPT_BYTES = 120 * 1024
MAX_HEADLESS_OUTPUT_BYTES = 4 * 1024 * 1024


def safe_slug(raw, fallback):
    text = re.sub(r"[^A-Za-z0-9._-]+", "-", str(raw or "").strip())
    return re.sub(r"\.{2,}", "-", text).strip(".-") or fallback


def as_bool(raw, default=False):
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


def clamp_timeout(raw, default=300):
    try:
        return max(30, min(900, int(raw)))
    except (TypeError, ValueError):
        return default


def resolve_bin(name):
    found = shutil.which(name)
    if found:
        return found
    cand = os.path.expanduser(f"~/.local/bin/{name}")
    return cand if os.path.isfile(cand) and os.access(cand, os.X_OK) else None


def normalize_family(raw):
    f = str(raw or "").strip().lower()
    if f in ("gemini", "google"):
        return "gemini"
    if f in ("anthropic", "claude"):
        return "anthropic"
    if f in ("other", "openai", "gpt"):
        return "other"
    return ""


def model_family(model_id):
    m = str(model_id or "").strip().lower()
    if m.startswith("models/"):
        m = m[7:]
    if m.startswith("gemini") or "gemini" in m:
        return "gemini"
    if m.startswith("claude") or "claude" in m or "anthropic" in m:
        return "anthropic"
    return "other" if m else ""


def resolve_caller_family(args_dict):
    fam = normalize_family(args_dict.get("caller_family"))
    if fam:
        return fam
    raw_model = str(args_dict.get("caller_model") or os.environ.get("FYR_CALLER_MODEL") or "").strip()
    if raw_model:
        fam = model_family(raw_model)
        if fam:
            return fam
    return "gemini"


def is_cross_family_model(model_id, caller_family="gemini", explicit_family=""):
    m = str(model_id or "").strip()
    if not m or not MODEL_ID_RE.match(m):
        return False
    picked_fam = normalize_family(explicit_family) or model_family(m)
    caller_fam = normalize_family(caller_family) or "gemini"
    return bool(picked_fam and picked_fam != caller_fam)


def _runtime_flag_enabled(args_dict, key, conversation_id=""):
    raw = args_dict.get(key)
    if raw is None or (isinstance(raw, str) and not raw.strip()):
        return bool(str(conversation_id or "").strip()) and not os.environ.get("FYR_WORKFLOW_MOCKS")
    return as_bool(raw, False)


def exec_cli_enabled(args_dict, conversation_id=""):
    return _runtime_flag_enabled(args_dict, "exec_cli", conversation_id)


def cross_family_enabled(args_dict, conversation_id=""):
    return _runtime_flag_enabled(args_dict, "cross_family", conversation_id)


def headless_env():
    env = dict(os.environ)
    local_bin = os.path.expanduser("~/.local/bin")
    cur_path = env.get("PATH", "")
    parts = cur_path.split(os.pathsep) if cur_path else []
    if local_bin and local_bin not in parts:
        env["PATH"] = (cur_path + os.pathsep + local_bin) if cur_path else local_bin
    env["GIT_TERMINAL_PROMPT"] = "0"
    return env


def make_route(enabled, model=None, source="fallback", reason="", agy_bin=None):
    res = {"enabled": enabled, "model": model, "source": source, "reason": reason}
    if agy_bin:
        res["agy_bin"] = agy_bin
    return res


def _pick_judge_model(fyr_bin, caller_fam, args_dict):
    cmd = [fyr_bin, "models", "--pick", "judge", "--json", "--caller-family", caller_fam]
    fyr_home = str(args_dict.get("fyr_home", "")).strip()
    if fyr_home and not fyr_home.startswith("-"):
        cmd.append(f"--home={fyr_home}")
    try:
        proc = subprocess.run(cmd, capture_output=True, text=True, timeout=PICK_TIMEOUT_SEC, check=False, env=headless_env())
    except (OSError, subprocess.SubprocessError) as exc:
        return None, f"fyr models --pick judge failed: {exc}"
    if proc.returncode != 0:
        lines = (proc.stderr or proc.stdout or "").strip().splitlines()
        return None, f"fyr models --pick judge: {lines[-1] if lines else f'exit {proc.returncode}'}"
    try:
        picked = json.loads(proc.stdout)
    except ValueError as exc:
        return None, f"invalid pick JSON: {exc}"
    if not isinstance(picked, dict):
        return None, "pick JSON was not an object"
    return picked, None


def resolve_cross_family_route(args_dict, conversation_id=""):
    raw_cf = args_dict.get("cross_family")
    if not cross_family_enabled(args_dict, conversation_id):
        omitted = raw_cf is None or (isinstance(raw_cf, str) and not raw_cf.strip())
        return make_route(False, source="disabled", reason="default disabled (no CONVERSATION_ID)" if omitted else "cross_family=false")
    caller_fam = resolve_caller_family(args_dict)
    agy_bin = resolve_bin("agy")
    explicit = str(args_dict.get("judge_model") or args_dict.get("model_override") or "").strip()
    if explicit:
        if is_cross_family_model(explicit, caller_fam) and agy_bin:
            return make_route(True, explicit, "explicit", f"explicit model {explicit}", agy_bin)
        return make_route(False, explicit, "fallback", f"explicit model {explicit!r} uses native pro tier")
    fyr_bin = resolve_bin("fyr")
    if not fyr_bin:
        return make_route(False, reason="fyr binary not found on PATH")
    picked, err = _pick_judge_model(fyr_bin, caller_fam, args_dict)
    if err:
        return make_route(False, reason=err)
    model_id = str(picked.get("model", "")).strip()
    reason = str(picked.get("reason", "")).strip()
    fb_reason = str(picked.get("fallback_reason", "")).strip()
    picked_fam = str(picked.get("family", "")).strip()
    if not is_cross_family_model(model_id, caller_fam, picked_fam):
        return make_route(False, model_id or None, f"fallback-{caller_fam}", fb_reason or reason or f"non-cross-family pick {model_id!r}")
    if not agy_bin:
        return make_route(False, model_id, "fallback", "agy binary not found on PATH")
    return make_route(True, model_id, "fyr-models-pick", reason or f"picked {model_id}", agy_bin)


def strip_json_fences(text):
    s = str(text or "").strip()
    if s.startswith("```"):
        s = re.sub(r"^```[A-Za-z0-9_-]*\s*", "", s)
        s = re.sub(r"\s*```\s*$", "", s)
    return s.strip()


def unwrap_agy_json(raw_stdout):
    trimmed = str(raw_stdout or "").strip()
    if not trimmed:
        return None, "empty headless output"
    try:
        parsed = json.loads(trimmed)
    except ValueError:
        try:
            parsed = json.loads(strip_json_fences(trimmed))
        except ValueError as exc:
            return None, f"invalid JSON output: {exc}"
    if not isinstance(parsed, dict):
        return None, "headless JSON root is not an object"
    if not any(k in parsed for k in ("conversation_id", "response", "error", "structured_output", "denied_actions")):
        return parsed, None
    if str(parsed.get("error") or "").strip():
        return None, f"agy error: {str(parsed.get('error')).strip()}"
    status = str(parsed.get("status") or "").strip()
    if status and status.upper() != "SUCCESS":
        return None, f"agy status {status}"
    denied = parsed.get("denied_actions")
    if isinstance(denied, list) and denied:
        return None, f"headless agy had {len(denied)} denied tool action(s)"
    if isinstance(parsed.get("structured_output"), dict):
        return parsed["structured_output"], None
    resp = strip_json_fences(parsed.get("response") or "")
    if resp:
        try:
            inner = json.loads(resp)
        except ValueError as exc:
            return None, f"invalid JSON in response: {exc}"
        if isinstance(inner, dict):
            return inner, None
    return None, "missing structured_output in agy envelope"


def safe_artifact_rel(candidate, default_rel):
    cand = str(candidate or "").strip().replace("\\", "/")
    norm = os.path.normpath(cand) if cand else ""
    if os.path.dirname(norm) == ".agents/scratch/agents" and os.path.basename(norm) not in ("", ".", ".."):
        return norm
    return default_rel


def ensure_artifact_file(artifact_rel, body):
    base = os.path.realpath(os.getcwd())
    for rel_dir in (".agents", os.path.join(".agents", "scratch"), os.path.join(".agents", "scratch", "agents")):
        if os.path.islink(os.path.join(base, rel_dir)):
            return
    agents_dir = os.path.join(base, ".agents", "scratch", "agents")
    full_path = os.path.normpath(os.path.join(base, artifact_rel))
    if not full_path.startswith(agents_dir + os.sep) or os.path.islink(full_path):
        return
    if os.path.isfile(full_path) and os.path.getsize(full_path) > 0:
        return
    os.makedirs(agents_dir, exist_ok=True)
    if os.path.islink(agents_dir):
        return
    with open(full_path, "w", encoding="utf-8") as fh:
        fh.write(body)


async def _exec_agy_headless(prompt_text, schema, route, timeout_sec):
    if len(prompt_text.encode("utf-8")) >= MAX_HEADLESS_PROMPT_BYTES:
        return None, "headless prompt exceeds 120 KiB safety cap"
    cmd = [
        route["agy_bin"], "-p", prompt_text, "--model", route["model"],
        "--disable-slash-commands", "--output-format", "json",
        "--json-schema", json.dumps(schema), "--print-timeout", f"{timeout_sec}s",
    ]
    try:
        proc = await asyncio.create_subprocess_exec(
            *cmd, stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.PIPE, env=headless_env(),
        )
    except OSError as exc:
        return None, f"spawn agy failed: {exc}"
    try:
        stdout_b, stderr_b = await asyncio.wait_for(proc.communicate(), timeout=timeout_sec + 15)
    except asyncio.TimeoutError:
        try:
            proc.kill()
            await proc.communicate()
        except OSError:
            pass
        return None, f"headless agy timed out after {timeout_sec}s"
    if len(stdout_b) > MAX_HEADLESS_OUTPUT_BYTES:
        return None, "headless output exceeded 4 MiB cap"
    stdout_s = stdout_b.decode("utf-8", errors="replace")
    stderr_s = stderr_b.decode("utf-8", errors="replace").strip()
    if proc.returncode != 0:
        tail = stderr_s.splitlines()[-1] if stderr_s else f"exit {proc.returncode}"
        return None, f"headless agy failed ({tail})"
    return stdout_s, None


async def run_headless_cross_family(prompt_text, schema, route, timeout_sec, validate_fn, render_artifact_fn):
    stdout_s, err = await _exec_agy_headless(prompt_text, schema, route, timeout_sec)
    if err:
        return None, err
    unwrapped, err = unwrap_agy_json(stdout_s)
    if err:
        return None, err
    validated, err = validate_fn(unwrapped)
    if err:
        return None, err
    ensure_artifact_file(validated["artifact_path"], render_artifact_fn(validated))
    return validated, None


async def record_orchestration_run(args_dict, payload, conversation_id=""):
    if not exec_cli_enabled(args_dict, conversation_id) or as_bool(args_dict.get("dry_run"), False):
        return
    fyr_bin = resolve_bin("fyr")
    if not fyr_bin:
        return
    cmd = [fyr_bin, "metrics", "record-run", "--json-in"]
    fyr_home = str(args_dict.get("fyr_home", "")).strip()
    if fyr_home and not fyr_home.startswith("-"):
        cmd.append(f"--home={fyr_home}")
    try:
        proc = await asyncio.create_subprocess_exec(
            *cmd, stdin=asyncio.subprocess.PIPE, stdout=asyncio.subprocess.DEVNULL, stderr=asyncio.subprocess.DEVNULL, env=headless_env(),
        )
        try:
            await asyncio.wait_for(proc.communicate(input=json.dumps(payload).encode("utf-8")), timeout=PICK_TIMEOUT_SEC)
        except asyncio.TimeoutError:
            try:
                proc.kill()
                await proc.communicate()
            except OSError:
                pass
    except Exception:
        pass


def make_run_row(run_id, kind, status, units, failed, run_start, args_dict, route, verdict):
    row = {
        "id": run_id,
        "ts": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
        "kind": kind,
        "project_root": os.getcwd(),
        "status": status,
        "units": units,
        "defect_rounds": 0,
        "failed": failed,
        "duration_ms": max(0, int((time.monotonic() - run_start) * 1000)),
        "worker_model": str(args_dict.get("caller_model") or "").strip(),
        "judge_model": str(route.get("model") or "").strip() if route.get("enabled") else "",
        "verdict": verdict,
    }
    row["passed"] = units - failed
    return row


def routing_summary(route):
    summary = {k: route.get(k) for k in ("enabled", "model", "source", "reason")}
    summary["enabled"] = bool(summary["enabled"])
    return summary


def routing_log_line(route):
    if route["enabled"]:
        return f"Cross-family routing enabled: model={route['model']} ({route['source']})"
    return f"Cross-family routing fallback to native pro: {route['reason']}"
