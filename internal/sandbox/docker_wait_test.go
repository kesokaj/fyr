package sandbox

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

const fakeDockerWaitTransition = `#!/bin/bash
CTL=@CTL@; REC=@REC@
printf '%s\n' "$*" >> "$REC/docker.calls"
case "$1" in
run)
  shift; envs=(); cid=; wd=
  while [ $# -gt 0 ]; do
    case "$1" in
      -e) envs+=("$2"); shift 2 ;;
      --cidfile) cid=$2; shift 2 ;;
      -w) wd=$2; shift 2 ;;
      --read-only|--init) shift ;;
      -*) shift 2 ;;
      *) break ;;
    esac
  done
  shift 2
  echo cid123 > "$cid"
  cd "$wd" && exec env -i "${envs[@]}" "$(cat "$CTL/agy")" "$@" ;;
wait)
  if [ "$2" = "fyr-work-stamp1-w1" ]; then
    echo waited > "$REC/waited"
  fi ;;
ps)
  n=$(( $(cat "$REC/ps.n" 2>/dev/null || echo 0) + 1 )); echo "$n" > "$REC/ps.n"
  case "$*" in *"--filter label=fyr.worker=w1"*) ;; *) exit 0 ;; esac
  if [ "$n" -eq 1 ]; then
    if [ -e "$CTL/stay-running" ] || [ ! -e "$REC/waited" ]; then
      echo "cid123 running"
    else
      echo "cid123 exited"
    fi
  fi ;;
rm) ;;
esac
`

func TestDockerSessionWaitsForDaemonExitTransition(t *testing.T) {
	t.Run("waits-until-exited", func(t *testing.T) {
		d := newDockerRig(t)
		script := strings.NewReplacer("@CTL@", ShellQuote(d.ctl), "@REC@", ShellQuote(d.rec)).Replace(fakeDockerWaitTransition)
		put(t, filepath.Join(d.bin, "docker"), script, 0o755)

		b := d.prepared(d.config("w1"))
		if rc := b.Session(context.Background(), "agy-unused", filepath.Join(d.real, TokenRel), "hello", "out.txt"); rc != 0 || !b.Started() {
			t.Fatalf("rc %d started %v\n%s", rc, b.Started(), d.out.String())
		}
		b.After()
		b.ExitChecks()
		if len(b.Reasons()) > 0 {
			t.Fatalf("reasons %v\n%s\ncalls:\n%s", b.Reasons(), d.out.String(), d.read("docker.calls"))
		}
	})

	t.Run("still-running-fails-closed", func(t *testing.T) {
		d := newDockerRig(t)
		script := strings.NewReplacer("@CTL@", ShellQuote(d.ctl), "@REC@", ShellQuote(d.rec)).Replace(fakeDockerWaitTransition)
		put(t, filepath.Join(d.bin, "docker"), script, 0o755)
		d.set("stay-running", "")

		b := d.prepared(d.config("w1"))
		if rc := b.Session(context.Background(), "agy-unused", filepath.Join(d.real, TokenRel), "hello", "out.txt"); rc != 0 {
			t.Fatalf("rc %d", rc)
		}
		if !b.Has(ReasonProcs) {
			t.Fatalf("expected ReasonProcs when container stays running after wait, got %v", b.Reasons())
		}
	})
}
