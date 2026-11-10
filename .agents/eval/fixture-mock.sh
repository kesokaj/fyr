# Held for fixture.sh: throwaway-directory guards for mock-* and demo scenarios (BUG-526).
# Sourced by .agents/eval/fixture.sh before any destructive step runs.

guard_demo_clone() {
  unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE GIT_COMMON_DIR GIT_OBJECT_DIRECTORY GIT_ALTERNATE_OBJECT_DIRECTORIES GIT_CONFIG_COUNT GIT_CONFIG_PARAMETERS
  local cwd sb_dir
  if ! cwd=$(pwd -P) || [ -z "$cwd" ] || [ "$(basename -- "$cwd")" != "app" ] ||
    [ -L .git ] || [ ! -d .git ] ||
    [ -z "${SB:-}" ] || [ ! -d "$SB" ] || ! sb_dir=$(cd -- "$SB" 2>/dev/null && pwd -P) ||
    [ "$cwd" = "$sb_dir" ] || [ "$(dirname -- "$cwd")" != "$(dirname -- "$sb_dir")" ] ||
    [ -e .agents/scratch/search.db ] || [ -L .agents/scratch/search.db ]; then
    echo "fixture.sh: refusing demo scenario outside trial app/ clone (pwd=${cwd:-?})" >&2
    return 2
  fi
}

guard_mock_dir() {
  unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE GIT_COMMON_DIR GIT_OBJECT_DIRECTORY GIT_ALTERNATE_OBJECT_DIRECTORIES GIT_CONFIG_COUNT GIT_CONFIG_PARAMETERS
  local cwd eval_dir checkout_root raw_tmp tmp_root sb_dir home_p=""
  if ! cwd=$(pwd -P) || [ -z "$cwd" ] || [ "$cwd" = "/" ]; then
    echo "fixture.sh: refusing mock scenario in unsafe working directory" >&2
    return 2
  fi
  if ! eval_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" 2>/dev/null && pwd -P) ||
    ! checkout_root=$(cd -- "$eval_dir/../.." 2>/dev/null && pwd -P); then
    echo "fixture.sh: refusing mock scenario (cannot resolve checkout root)" >&2
    return 2
  fi
  if [ -z "${SB:-}" ] || [ ! -d "$SB" ] || ! sb_dir=$(cd -- "$SB" 2>/dev/null && pwd -P); then
    echo "fixture.sh: refusing mock scenario (SB must be an existing directory)" >&2
    return 2
  fi
  raw_tmp="${TMPDIR:-/tmp}"
  if [ -z "$raw_tmp" ] || [ ! -d "$raw_tmp" ] || ! tmp_root=$(cd -- "$raw_tmp" 2>/dev/null && pwd -P); then
    echo "fixture.sh: refusing mock scenario (TMPDIR must be an existing directory)" >&2
    return 2
  fi
  if [ -n "${HOME:-}" ]; then
    if [ -d "$HOME" ]; then
      home_p=$(cd -- "$HOME" 2>/dev/null && pwd -P) || {
        echo "fixture.sh: refusing mock scenario (cannot resolve HOME)" >&2
        return 2
      }
    else
      home_p="$HOME"
    fi
  fi
  if [ "$tmp_root" = "/" ] || [ "$cwd" = "$tmp_root" ]; then
    echo "fixture.sh: refusing mock scenario (TMPDIR=/ or PWD equals TMPDIR)" >&2
    return 2
  fi
  if [ -n "$home_p" ]; then
    case "$home_p" in
      "$cwd" | "$cwd"/* | "$tmp_root")
        echo "fixture.sh: refusing mock scenario (TMPDIR or PWD overlaps HOME)" >&2
        return 2
        ;;
    esac
  fi
  case "$sb_dir" in
    "$cwd" | "$cwd"/*)
      echo "fixture.sh: refusing mock scenario (SB overlaps PWD)" >&2
      return 2
      ;;
  esac
  case "$cwd" in
    "$sb_dir"/*)
      echo "fixture.sh: refusing mock scenario (PWD is inside SB)" >&2
      return 2
      ;;
  esac
  case "$checkout_root" in
    "$cwd" | "$cwd"/*)
      echo "fixture.sh: refusing mock scenario at or above checkout $checkout_root" >&2
      return 2
      ;;
  esac
  case "$cwd" in
    "$checkout_root"/*)
      echo "fixture.sh: refusing mock scenario inside checkout $checkout_root" >&2
      return 2
      ;;
  esac
  if [ -L .git ] || { [ -e .git ] && [ ! -d .git ]; }; then
    echo "fixture.sh: refusing mock scenario (worktree or non-directory .git in $cwd)" >&2
    return 2
  fi
  if [ ! -d .git ] && git rev-parse --git-dir >/dev/null 2>&1; then
    echo "fixture.sh: refusing mock scenario inside enclosing git repository ($cwd)" >&2
    return 2
  fi
  if [ -e .agents/scratch/search.db ] || [ -L .agents/scratch/search.db ] ||
    [ -e .agents/eval/fixture.sh ] || [ -L .agents/eval/fixture.sh ]; then
    echo "fixture.sh: refusing mock scenario (checkout marker present in $cwd)" >&2
    return 2
  fi
  case "$cwd" in
    "$tmp_root"/*) return 0 ;;
  esac
  echo "fixture.sh: refusing mock scenario outside throwaway root $tmp_root (pwd=$cwd)" >&2
  return 2
}
