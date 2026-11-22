package auto

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/kesokaj/fyr/internal/dream"
	"github.com/kesokaj/fyr/internal/search"
	"github.com/kesokaj/fyr/internal/trail"
)

// sweep checks G3, discovers all targets, evaluates each target, and runs the
// machine's health once when a target reached it.
func (r *run) sweep() {
	if stop := r.machineStop(); stop != "" {
		r.stopMachine(stop)
		return
	}
	targets, ok := r.discoverSweepTargets()
	if !ok {
		return
	}
	reached, seen := r.sweepTargets(targets)
	if r.halted {
		return
	}
	if len(targets) == 0 {
		r.say("no workspaces discovered")
	}
	r.pruneSweepProjects(seen)
	if !r.dry {
		r.consolidateProposals()
		r.syncSIMFromLatestDigest()
	}
	if reached > 0 && !r.dry {
		if !r.again("the machine's checks") {
			return
		}
		r.machineHealth()
		if r.halted {
			return
		}
	}
	r.verdict = fmt.Sprintf("%d of %d projects checked", reached, len(targets))
	r.say(r.verdict)
}

func (r *run) discoverSweepTargets() ([]dream.Target, bool) {
	env, home := r.env, r.o.Home
	raw, err := limited(r.context(), r.o.limit(projectLimit), func() ([]dream.Target, error) { return env.Targets(home) })
	if err != nil {
		r.stopMachine("targets: " + oneLine(err.Error()))
		r.failed = true
		return nil, false
	}
	targets, skipped := filterSweepTargets(raw, r.o.Home, r.o.RealHome)
	if skipped > 0 {
		r.say(fmt.Sprintf("skipped %d ephemeral workspace(s)", skipped))
	}
	return targets, true
}

func (r *run) sweepTargets(targets []dream.Target) (int, map[string]bool) {
	reached := 0
	seen := map[string]bool{}
	for i, t := range targets {
		if i > 0 && !r.again(t.Root) {
			return reached, seen
		}
		seen[t.Root] = true
		if r.project(t.Root, t.Skip) {
			reached++
			r.projectsChecked++
		}
		if r.halted {
			return reached, seen
		}
	}
	return reached, seen
}

// project is one target's checks and health; it says whether health ran.
func (r *run) project(root, skip string) bool {
	r.projectWorkerRan = false
	p := r.st.Projects[root]
	read := projectRead{stop: "skipped: " + skip}
	if skip == "" {
		read = r.readProject(root)
	}
	verdict := short(read.stop)
	reached := read.stop == ""
	if reached {
		verdict = short(r.health(root, &p, read))
	}
	r.say(short(root) + ": " + verdict)
	now := r.o.now()
	p.LastRun, p.Verdict = now, verdict
	if skip == "gone" {
		if p.GoneSince.IsZero() {
			p.GoneSince = now
		}
	} else {
		p.GoneSince = time.Time{}
	}
	if r.st.Projects == nil {
		r.st.Projects = map[string]Project{}
	}
	r.st.Projects[root] = p
	return reached
}

const (
	goneKeepAge = 7 * 24 * time.Hour
	maxGoneKeep = 20
)

func (r *run) pruneSweepProjects(seen map[string]bool) {
	for root := range r.st.Projects {
		if !seen[root] {
			delete(r.st.Projects, root)
		}
	}
	pruneGoneProjects(r.st.Projects, r.o.now())
}

func pruneGoneProjects(projects map[string]Project, now time.Time) {
	type goneEntry struct {
		root string
		at   time.Time
	}
	var kept []goneEntry
	for root, p := range projects {
		if p.Verdict != "skipped: gone" {
			continue
		}
		if IsEphemeralPath(root) {
			delete(projects, root)
			continue
		}
		goneAt := p.GoneSince
		if goneAt.IsZero() {
			goneAt = p.LastRun
		}
		if goneAt.IsZero() || now.Sub(goneAt) > goneKeepAge {
			delete(projects, root)
			continue
		}
		kept = append(kept, goneEntry{root: root, at: goneAt})
	}
	if len(kept) <= maxGoneKeep {
		return
	}
	sort.Slice(kept, func(i, j int) bool {
		if !kept[i].at.Equal(kept[j].at) {
			return kept[i].at.After(kept[j].at)
		}
		return kept[i].root < kept[j].root
	})
	for _, g := range kept[maxGoneKeep:] {
		delete(projects, g.root)
	}
}

// readProject checks git readability and collects free findings only on
// readable git checkouts (non-git workspaces and GlobalWorkspace pass safely
// without G7/G8 blockers).
func (r *run) readProject(root string) projectRead {
	if root == search.GlobalWorkspace {
		return projectRead{}
	}
	o, dry, readFn := r.o, r.dry, readable
	got, err := limited(r.context(), o.limit(projectLimit), func() ([]projectRead, error) {
		if _, _, rerr := trail.Roots(root); errors.Is(rerr, trail.ErrNotCheckout) {
			return []projectRead{{}}, nil
		} else if rerr != nil {
			return []projectRead{{stop: "skipped: " + oneLine(rerr.Error())}}, nil
		}
		if !readFn(root) {
			return []projectRead{{stop: "skipped: a partial clone this git cannot read safely"}}, nil
		}
		var read projectRead
		if !dry {
			read.findings, read.failed = o.projectFindings(root)
		}
		return []projectRead{read}, nil
	})
	if err != nil {
		r.failed = true
		return projectRead{stop: "read project: " + err.Error()}
	}
	return got[0]
}

// projectRead is readProject's answer: stop is the gate that held, if one did.
type projectRead struct {
	stop     string
	findings []Finding
	failed   bool
}

var readable = trail.Readable

const (
	projectLimit = 10 * time.Minute
	installLimit = 2 * time.Minute
	metricsLimit = 30 * time.Minute
)

// limited runs read and gives up on it after d or when ctx cancels. Its
// goroutine may outlive the run, so read must touch nothing the run still uses.
func limited[T any](ctx context.Context, d time.Duration, read func() ([]T, error)) ([]T, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	tctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	type answer struct {
		got []T
		err error
	}
	done := make(chan answer, 1)
	go func() {
		got, err := read()
		done <- answer{got, err}
	}()
	select {
	case a := <-done:
		return a.got, a.err
	case <-tctx.Done():
		if errors.Is(tctx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
			return nil, fmt.Errorf("a read took over %s", d)
		}
		return nil, tctx.Err()
	}
}
