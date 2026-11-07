package eval

import (
	"time"
)

// fakeAgy is the CLI a Run test drives: it records its arguments,
// environment, working directory, the settings it was given and whether the
// token was linked, then does what the control files in @CTL@ say. The
// fakeAgy of internal/dream is unexported and speaks stream-json; eval's
// agy is the -p form (plan § Verification).
const fakeAgy = `#!/bin/bash
CTL=@CTL@; REC=@REC@
while ! mkdir "$REC/agy.lock" 2>/dev/null; do sleep 0.01; done
n=$(( $(cat "$REC/agy.n" 2>/dev/null || echo 0) + 1 )); echo "$n" > "$REC/agy.n"
if [ -e "$CTL/barrier" ]; then
  inf=$(( $(cat "$REC/inflight" 2>/dev/null || echo 0) + 1 )); echo "$inf" > "$REC/inflight"
  pk=$(cat "$REC/peak" 2>/dev/null || echo 0)
  [ "$inf" -gt "$pk" ] && echo "$inf" > "$REC/peak"
  mkdir -p "$REC/arrived"; : > "$REC/arrived/$n"
fi
rmdir "$REC/agy.lock"
if [ -e "$CTL/barrier" ]; then
  b=$(cat "$CTL/barrier")
  for _ in $(seq 1 500); do
    [ "$(ls "$REC/arrived" 2>/dev/null | wc -l)" -ge "$b" ] && break
    sleep 0.01
  done
fi
if [ -e "$CTL/after.$n" ]; then
  w=$(cat "$CTL/after.$n")
  for _ in $(seq 1 500); do
    [ -e "$REC/finished.$w" ] && break
    sleep 0.01
  done
  sleep 0.05
fi
if [ -e "$CTL/sleep.$n" ]; then sleep "$(cat "$CTL/sleep.$n")"; fi
if [ -e "$CTL/barrier" ]; then
  while ! mkdir "$REC/agy.lock" 2>/dev/null; do sleep 0.01; done
  inf=$(( $(cat "$REC/inflight" 2>/dev/null || echo 1) - 1 )); echo "$inf" > "$REC/inflight"
  echo "$n" >> "$REC/finish.order"
  : > "$REC/finished.$n"
  rmdir "$REC/agy.lock"
fi
printf '%s\n' "$@" > "$REC/agy.args.$n"
env > "$REC/agy.env.$n"; pwd > "$REC/agy.cwd.$n"
cp "$HOME/.gemini/antigravity-cli/settings.json" "$REC/settings.$n" 2>/dev/null
[ -L "$HOME/.gemini/antigravity-cli/antigravity-oauth-token" ] && echo yes > "$REC/linked.$n"
conv=$HOME/.gemini/antigravity-cli/conversations
if [ -e "$CTL/invoke" ]; then mkdir -p "$conv"; printf 'SQLite format 3\000{"TypeName":"%s"}\000' "$(cat "$CTL/invoke")" > "$conv/main-$n.db"; fi
if [ -e "$CTL/denied" ]; then echo "tool call auto-denied: command(rm)" >&2; fi
if [ -e "$CTL/soft-denied" ]; then
  mkdir -p "$HOME/.gemini/antigravity-cli/log"
  echo 'I1106 17:11:32.066770 467 tool_confirmation_manager.go:221] Print mode: soft-denying tool confirmation "RunCommand" at step 51' > "$HOME/.gemini/antigravity-cli/log/cli-1.log"
  echo "API error (attempt 1): RESOURCE_EXHAUSTED (code 429)" >&2
  exit 3
fi
if [ -e "$CTL/invalid" ]; then mkdir -p "$HOME/.gemini/antigravity-cli/log"; echo "invalid allow entry: x" > "$HOME/.gemini/antigravity-cli/log/cli-1.log"; fi
if [ -e "$CTL/leak" ]; then printf 'GH_TOKEN=%s\n' "$(cat "$CTL/leak")" > "$HOME/.gemini/term.log"; fi
if [ -e "$CTL/realconfig" ]; then echo x >> "$(cat "$CTL/realconfig")"; fi
if [ -e "$CTL/replace-token" ]; then rm "$HOME/.gemini/antigravity-cli/antigravity-oauth-token"; echo copy > "$HOME/.gemini/antigravity-cli/antigravity-oauth-token"; fi
if [ -e "$CTL/child" ]; then sleep 300 </dev/null >/dev/null 2>&1 & echo $! >> "$REC/pids"; fi
if [ -e "$CTL/setsid" ]; then (cd / && exec setsid sleep 301 </dev/null >/dev/null 2>&1) & echo $! >> "$REC/pids"; fi
if [ -e "$CTL/hang" ]; then touch "$REC/started"; exec sleep 300; fi
sbdir=$(dirname "$HOME")
if [ -e "$CTL/print-leak" ]; then echo "GH_TOKEN=$(cat "$CTL/print-leak")"; fi
if [ -e "$CTL/clone-leak" ]; then echo "GH_TOKEN=$(cat "$CTL/clone-leak")" > leak.env; fi
if [ -e "$CTL/fifo" ]; then rm "$sbdir/err.txt"; mkfifo "$sbdir/err.txt" "$sbdir/check.log"; mkdir -p "$conv"; mkfifo "$conv/main.db"; fi
if [ -e "$CTL/link-cli" ]; then mv "$HOME/.gemini/antigravity-cli" "$HOME/.gemini/cli.moved"; ln -s "$(cat "$CTL/link-cli")" "$HOME/.gemini/antigravity-cli"; fi
if [ -e "$CTL/decoy" ]; then (cd "$(cat "$CTL/decoy")" && HOME=/nowhere exec setsid sleep 303 </dev/null >/dev/null 2>&1) & echo $! >> "$REC/decoy"; fi
if [ -e "$CTL/swap" ]; then for d in app home; do mv "$sbdir/$d" "$sbdir/$d.moved"; ln -s "$(cat "$CTL/swap")" "$sbdir/$d"; done; fi
echo "stderr $n" >&2
echo "agy says ok"; echo "session $n"
if [ -e "$CTL/done.$n" ]; then echo "Nothing queued; start the next task in a fresh conversation."; fi
if [ -e "$CTL/exit.$n" ]; then exit "$(cat "$CTL/exit.$n")"; fi
if [ -e "$CTL/exit" ]; then exit "$(cat "$CTL/exit")"; fi
exit 0
`

// fakeSelf stands in for the fyr binary: install makes the installed binary
// the wrapper execs, and record list prints one record.
const fakeSelf = `#!/bin/bash
CTL=@CTL@; REC=@REC@
printf '%s\n' "$*" >> "$REC/self.calls"
case "$1" in
install)
  if [ -e "$CTL/install-fail" ]; then echo "install broke" >&2; exit 1; fi
  pwd > "$REC/install.cwd"; env > "$REC/install.env"
  mkdir -p "$3/.local/bin"; printf '#!/bin/bash\necho "fyr $*"\n' > "$3/.local/bin/fyr"; chmod +x "$3/.local/bin/fyr" ;;
record)
  if [ -e "$CTL/records-fail" ]; then exit 1; fi
  echo "PLAN-1  plan  open  2026-10-26  a plan" ;;
esac
`

var stampTime = time.Date(2026, 10, 26, 8, 0, 0, 0, time.Local)
