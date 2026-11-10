#!/usr/bin/env bash
#
# A wizard walks a human through a manual procedure, step by step.
# Adapted from mattpocock/skills skills/engineering/wizard/template.sh (4ae2317, MIT)
# for the fyr bash skill: defaults ephemeral copies to .agents/scratch/wizard-<slug>.sh
# and adds set_gcp_secret via `gcloud secrets versions add --data-file=-`.
#
# Mode 0644 in skills/bash/assets/ on purpose (go:embed carries no execute bit);
# invoke an authored copy as `bash .agents/scratch/wizard-<slug>.sh`.
#
# Everything above the "STAGES" marker is the wizard library: do not hand-edit
# it. Author the per-step stages below the marker.

# Library helpers may go unused in a given wizard while `exit` closes the file.
# shellcheck disable=SC2317
set -euo pipefail

# ──────────────────────────────────────────────────────────────────────────
# Wizard library: consistent interactive UX across every wizard.
# ──────────────────────────────────────────────────────────────────────────

if [[ -t 1 ]] && command -v tput >/dev/null 2>&1 && [[ "$(tput colors 2>/dev/null || echo 0)" -ge 8 ]]; then
  BOLD=$(tput bold); DIM=$(tput dim); RESET=$(tput sgr0)
  BLUE=$(tput setaf 4); GREEN=$(tput setaf 2); YELLOW=$(tput setaf 3)
else
  BOLD=""; DIM=""; RESET=""; BLUE=""; GREEN=""; YELLOW=""
fi

# Author sets this at the top of the stages section.
TOTAL_STAGES=0

_STAGE_INDEX=0
_WIZARD_TMP=""
ENV_FILE="${ENV_FILE:-.env}"
WRITTEN_ENV=()    # KEYs written to ENV_FILE this run
WRITTEN_SECRET=() # secret targets set this run (gh:<NAME>, gcp:<NAME>)
SKIPPED=()        # manual follow-ups when gh or gcloud is unavailable

_cleanup_wizard_tmp() {
  if [[ -n "${_WIZARD_TMP:-}" ]]; then
    rm -f "$_WIZARD_TMP"
  fi
}
trap _cleanup_wizard_tmp EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# _clear wipes the terminal so only the current step is on screen. No-op when
# output isn't a terminal, so piped logs stay readable.
_clear() {
  [[ -t 1 ]] || return 0
  if command -v tput >/dev/null 2>&1 && tput clear 2>/dev/null; then return 0; fi
  printf '\033[2J\033[3J\033[H'
}

# banner "Title" shows the opening frame: what this wizard does.
banner() {
  _clear
  printf '\n%s%s  %s%s\n' "$BOLD" "$BLUE" "$1" "$RESET"
  printf '%s  %s stages%s\n\n' "$DIM" "$TOTAL_STAGES" "$RESET"
  printf '%s  You drive the browser; this wizard tells you what to click and\n' "$DIM"
  printf '  captures the values you copy back. Stop any time with Ctrl-C and re-run\n'
  printf '  later, since it remembers values already saved in %s.%s\n' "$ENV_FILE" "$RESET"
  pause "Ready to start?"
}

# stage "Name" clears the screen, then announces a stage and shows progress.
stage() {
  _clear
  _STAGE_INDEX=$((_STAGE_INDEX + 1))
  printf '\n%s%s▸ Stage %s/%s · %s%s\n' \
    "$BOLD" "$BLUE" "$_STAGE_INDEX" "$TOTAL_STAGES" "$1" "$RESET"
}

say()  { printf '  %s\n' "$1"; }
step() { printf '  %s•%s %s\n' "$BLUE" "$RESET" "$1"; }
note() { printf '  %s%s%s\n' "$DIM" "$1" "$RESET"; }
warn() { printf '  %s⚠ %s%s\n' "$YELLOW" "$1" "$RESET"; }

# open_url URL opens it in the human's browser, cross-platform incl. WSL.
open_url() {
  local url="$1"
  printf '  %s↗ opening%s %s\n' "$GREEN" "$RESET" "$url"
  { if   command -v wslview      >/dev/null 2>&1; then wslview "$url"
    elif command -v explorer.exe >/dev/null 2>&1; then explorer.exe "$url"
    elif command -v xdg-open     >/dev/null 2>&1; then xdg-open "$url"
    elif command -v open         >/dev/null 2>&1; then open "$url"
    else false; fi
  } >/dev/null 2>&1 || warn "couldn't open a browser automatically; visit: $url"
}

# pause "msg" waits for the human to confirm they've done the manual part.
pause() {
  printf '  %s%s%s ' "$DIM" "${1:-Press Enter to continue}" "$RESET"
  read -r _ || true
}

# confirm "question" is a y/N gate; returns success on yes.
confirm() {
  local reply=""
  printf '  %s? %s [y/N]%s ' "$YELLOW" "$1" "$RESET"
  read -r reply || true
  [[ "$reply" =~ ^[Yy] ]]
}

# _existing KEY: current value of KEY in ENV_FILE, if any, with write_env's
# single quotes or a plain dotenv double-quoted value undone.
_existing() {
  [[ -f "$ENV_FILE" && ! -L "$ENV_FILE" ]] || return 1
  local line="" value sq="'\\''"
  while IFS= read -r candidate; do
    if [[ "$candidate" == "${1}="* ]]; then
      line="$candidate"
    fi
  done < "$ENV_FILE"
  [[ -n "$line" ]] || return 1
  value="${line#*=}"
  if [[ "$value" == \'*\' ]]; then
    value="${value:1:${#value}-2}"
    value="${value//"$sq"/\'}"
  elif [[ "$value" == \"*\" ]]; then
    value="${value:1:${#value}-2}"
    [[ "$value" == *[\\\$\"]* ]] && return 1
  fi
  printf '%s' "$value"
}

# ask KEY "Prompt" reads a visible value into $KEY, offering the existing .env
# value on re-runs (Enter keeps it).
ask() {
  local key="$1" prompt="$2" current input="" s=$'\001' e=$'\002' p rc=0
  current=$(_existing "$key" || true)
  p="  ${s}${BOLD}${e}${prompt}${s}${RESET}${e} "
  [[ -n "$current" ]] && p+="${s}${DIM}${e}[Enter keeps current]${s}${RESET}${e} "
  read -e -r -p "$p" input || rc=1
  if (( rc )) && [[ -z "$input" ]]; then return 1; fi
  [[ -z "$input" && -n "$current" ]] && input="$current"
  printf -v "$key" '%s' "$input"
}

# ask_secret KEY "Prompt" reads a hidden secret value into $KEY.
ask_secret() {
  local key="$1" prompt="$2" current input="" rc=0
  current=$(_existing "$key" || true)
  if [[ -n "$current" ]]; then
    printf '  %s%s%s %s[Enter keeps current]%s ' "$BOLD" "$prompt" "$RESET" "$DIM" "$RESET"
  else
    printf '  %s%s%s ' "$BOLD" "$prompt" "$RESET"
  fi
  read -rs input || rc=1
  printf '\n'
  if (( rc )) && [[ -z "$input" ]]; then return 1; fi
  [[ -z "$input" && -n "$current" ]] && input="$current"
  printf -v "$key" '%s' "$input"
}

# write_env KEY VALUE upserts KEY='VALUE' into ENV_FILE at mode 0600 using a
# sibling scratch file (never system temp) and sets $KEY. Idempotent.
write_env() {
  local key="$1" value="$2" dir tmp sq="'\\''" line
  if [[ -L "$ENV_FILE" ]]; then
    printf '  %s✗ refusing symlinked %s%s\n' "$YELLOW" "$ENV_FILE" "$RESET" >&2
    return 1
  fi
  dir=$(dirname "$ENV_FILE")
  mkdir -p "$dir"
  tmp=$(umask 077 && mktemp "${ENV_FILE}.tmp.XXXXXX")
  _WIZARD_TMP="$tmp"
  chmod 0600 "$tmp"
  if [[ -f "$ENV_FILE" ]]; then
    while IFS= read -r line || [[ -n "$line" ]]; do
      if [[ "$line" != "${key}="* ]]; then
        printf '%s\n' "$line" >> "$tmp"
      fi
    done < "$ENV_FILE"
  fi
  printf "%s='%s'\n" "$key" "${value//\'/$sq}" >> "$tmp"
  mv -f "$tmp" "$ENV_FILE"
  _WIZARD_TMP=""
  printf -v "$key" '%s' "$value"
  WRITTEN_ENV+=("$key")
  printf '  %s✓ wrote%s %s → %s\n' "$GREEN" "$RESET" "$key" "$ENV_FILE"
}

# set_gcp_secret NAME VALUE [PROJECT_ID] adds a secret version in GCP Secret
# Manager via stdin (--data-file=-, never argv). Creates the secret with
# automatic replication first when missing.
set_gcp_secret() {
  local name="$1" value="$2" project="${3:-${GCP_PROJECT_ID:-}}"
  local -a proj_args=()
  [[ -n "$project" ]] && proj_args=(--project "$project")
  if command -v gcloud >/dev/null 2>&1; then
    if ! gcloud secrets describe "$name" "${proj_args[@]}" >/dev/null 2>&1; then
      gcloud secrets create "$name" --replication-policy="automatic" "${proj_args[@]}" >/dev/null 2>&1 || true
    fi
    if printf '%s' "$value" | gcloud secrets versions add "$name" --data-file=- "${proj_args[@]}" >/dev/null 2>&1; then
      WRITTEN_SECRET+=("gcp:$name")
      printf '  %s✓ set%s GCP Secret Manager %s\n' "$GREEN" "$RESET" "$name"
      return
    fi
  fi
  SKIPPED+=("GCP secret $name (run: printf '%s' '<value>' | gcloud secrets versions add $name --data-file=-)")
  warn "skipped GCP secret $name: gcloud not ready or project unset; set it later"
}

# set_secret NAME VALUE sets a GitHub repo secret via gh stdin.
set_secret() {
  local name="$1" value="$2"
  if command -v gh >/dev/null 2>&1 && gh auth status >/dev/null 2>&1; then
    if printf '%s' "$value" | gh secret set "$name" >/dev/null 2>&1; then
      WRITTEN_SECRET+=("gh:$name")
      printf '  %s✓ set%s GitHub secret %s\n' "$GREEN" "$RESET" "$name"
      return
    fi
  fi
  SKIPPED+=("GitHub secret $name (set manually: gh secret set $name)")
  warn "skipped GitHub secret $name: gh not ready; set it later"
}

# set_var NAME VALUE sets a GitHub repo variable (non-secret).
set_var() {
  local name="$1" value="$2"
  if command -v gh >/dev/null 2>&1 && gh auth status >/dev/null 2>&1; then
    if gh variable set "$name" --body "$value" >/dev/null 2>&1; then
      printf '  %s✓ set%s GitHub variable %s\n' "$GREEN" "$RESET" "$name"
      return
    fi
  fi
  SKIPPED+=("GitHub variable $name")
  warn "skipped GitHub variable $name: gh not ready; set it later"
}

# finish clears, then shows a closing summary of everything configured.
finish() {
  _clear
  printf '\n%s%s  ✓ Setup complete%s\n' "$BOLD" "$GREEN" "$RESET"
  (( ${#WRITTEN_ENV[@]} ))    && note "wrote ${#WRITTEN_ENV[@]} value(s) to $ENV_FILE: ${WRITTEN_ENV[*]}"
  (( ${#WRITTEN_SECRET[@]} )) && note "stored ${#WRITTEN_SECRET[@]} secret(s): ${WRITTEN_SECRET[*]}"
  if (( ${#SKIPPED[@]} )); then
    printf '\n'; warn "still to do by hand:"
    for s in "${SKIPPED[@]}"; do note "  - $s"; done
  fi
  printf '\n'
}

# ──────────────────────────────────────────────────────────────────────────
# STAGES: author this section. One stage() per step the human takes.
# Replace the example below. Set TOTAL_STAGES to match the stages you write.
# Keep every stage inside run_wizard and the last line as is: bash parses
# the whole file before the first prompt, so an edit mid-run cannot break it.
# ──────────────────────────────────────────────────────────────────────────

run_wizard() {

TOTAL_STAGES=1

banner "Service credentials setup"

# ── Example stage: replace with your real steps ───────────────────────────
stage "OAuth client & Cloud Run secret"
say "We will capture your OAuth credentials for local .env and GCP Secret Manager."
ask GCP_PROJECT_ID "GCP project ID:"
open_url "https://console.cloud.google.com/apis/credentials"
step "Select Create Credentials → OAuth client ID, then copy the Client ID."
ask OAUTH_CLIENT_ID "Paste OAuth client ID:"
step "Copy the Client Secret shown in the modal."
ask_secret OAUTH_CLIENT_SECRET "Paste OAuth client secret:"
write_env GCP_PROJECT_ID "$GCP_PROJECT_ID"
write_env OAUTH_CLIENT_ID "$OAUTH_CLIENT_ID"
write_env OAUTH_CLIENT_SECRET "$OAUTH_CLIENT_SECRET"
set_gcp_secret "oauth-client-secret" "$OAUTH_CLIENT_SECRET" "$GCP_PROJECT_ID"
set_secret OAUTH_CLIENT_SECRET "$OAUTH_CLIENT_SECRET"
# ──────────────────────────────────────────────────────────────────────────

finish
}

run_wizard "$@"; exit
