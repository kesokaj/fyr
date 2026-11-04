package eval

import "strings"

// taskOutcomes is the outcomes of the work-tasks, work-start and work-refuse
// rows (scenarios tasks and work-plan): the /work lead writes Kickoffs, runs
// the stand-in verb the fixture installs, squash-merges each PASS and stores
// each worker's records, or refuses a plan missing a first-run gate.
func taskOutcomes() map[string][]outcome {
	kick := func(name, files, task string) string {
		return "mkdir -p .agents/scratch/work/kickoffs\n" +
			"printf 'Project: demo; do this task only, as a worker, with no plan: %s\\nFiles: %s\\nModel: gemini-3.8-flash-medium\\nNever: push; tick a plan record or TODO.md.\\n' '" +
			task + "' '" + files + "' >.agents/scratch/work/kickoffs/" + name + ".md\n"
	}
	planKick := func(name, phase, files, expect string) string {
		return "mkdir -p .agents/scratch/work/kickoffs\n" +
			"printf 'Project: this clone, the current directory; never another checkout. Read AGENTS.md, then fyr record get PLAN-2026-11-02-greetings; do %s of PLAN-2026-11-02-greetings only, as a worker.\\nFiles: %s\\nNever: push; tick the plan record or TODO.md.\\nDone when: %s.\\n' '" +
			phase + "' '" + files + "' '" + expect + "' >.agents/scratch/work/kickoffs/" + name + ".md\n"
	}
	run := func(names ...string) string {
		s := "bash .agents/scratch/eval/fake-work.sh"
		for _, n := range names {
			s += " .agents/scratch/work/kickoffs/" + n + ".md"
		}
		return s + " >>\"$OUT2\""
	}
	merge := func(n string) string {
		return "git -c merge.directoryRenames=false merge -q --squash work/" + n + "\ngit commit -qm 'work: " + n + " (work/" + n + ")'\n"
	}
	planMerge := func(phase, n string) string {
		return "git -c merge.directoryRenames=false merge -q --squash work/" + n + "\ngit commit -qm 'PLAN-2026-11-02-greetings " + phase + " (work/" + n + ")'\n"
	}
	drop := func(n string) string { return "git branch -qD work/" + n + "\n" }
	store := func(n string) string {
		return "for f in .agents/scratch/work/*/" + n + "/records/*.md; do id=$(basename \"$f\" .md); " +
			"rec \"$(printf '%s' \"${id%%-*}\" | tr A-Z a-z)\" --id \"$id\" --title-file \"${f%.md}.title\" <\"$f\"; done\n"
	}
	land := func(n string) string { return merge(n) + drop(n) + store(n) }
	planLand := func(phase, n string) string { return planMerge(phase, n) + drop(n) + store(n) }
	// A task list runs as tasks.md § 3 and § 5 run it: one --check for the
	// table's runner, which logs no run, then each wave with it named.
	check := "bash .agents/scratch/eval/fake-work.sh --check >>\"$OUT2\"\n"
	wave := func(names ...string) string {
		return strings.Replace(run(names...), "fake-work.sh", "fake-work.sh --runner docker", 1)
	}

	tab := kick("greet-tab", "internal/greet/greet_test.go", "Add a table test TestHelloTab to internal/greet for a tab-only name")
	shout := kick("shout", "internal/shout/", "Add a package internal/shout with Shout(s string) string and its table test")
	disjoint := tab + shout + check + wave("greet-tab", "shout") + "\n" + land("greet-tab") + land("shout")

	bye := kick("goodbye", "internal/greet/", "Add Goodbye(name string) string to internal/greet/greet.go with a table test")
	hi := kick("welcome", "internal/greet/", "Add Welcome(name string) string to internal/greet/greet.go with a table test")
	waves := bye + hi + check + wave("goodbye") + "\n" + land("goodbye") + wave("welcome") + "\n" + land("welcome")

	routed := "echo 'Persist every greeting is a design question (SQLite or Postgres): routed to the feature skill, Kickoff /feature Persist every greeting' >\"$OUT\"\n"
	design := tab + check + wave("greet-tab") + "\n" + land("greet-tab") + routed
	persist := kick("persist", "internal/store/", "Persist every greeting, choosing between SQLite and Postgres")

	p1 := planKick("greetings-p1", "Phase 1", "internal/greet/greet_test.go", "go test ./... passes with TestHelloTab in internal/greet")
	p2 := planKick("greetings-p2", "Phase 2", "internal/shout/", "go test ./... passes with Shout and TestShout in internal/shout")
	tickPlan := "printf '# PLAN-2026-11-02-greetings\\n\\n- [x] Cold-reader check\\n- [x] Phase 1: TestHelloTab\\n- [x] Checkpoint\\n- [x] Phase 2: Shout\\n- [x] Checkpoint\\n' | \"$F\" record edit PLAN-2026-11-02-greetings --body\n"
	started := p1 + p2 + run("greetings-p1", "greetings-p2") + "\n" + planLand("p1", "greetings-p1") + planLand("p2", "greetings-p2") + tickPlan

	refuseCold := "echo 'Refused PLAN-2026-11-02-greet-alpha: cold-read box is not ticked.' >\"$OUT\"\n"
	refuseWorkers := "echo 'Refused PLAN-2026-11-02-greet-beta: no workers D-row saying yes under ## Decisions.' >\"$OUT2\"\n"
	refused := refuseCold + refuseWorkers

	// tier is a whole good outcome with the tier's name where the model ID
	// goes, as a lead that misread the table would write it, carried on past
	// every error: only the verb's refusal keeps it from landing.
	tier := func(s string) string {
		return "set +e\n" + strings.ReplaceAll(s, "Model: gemini-3.8-flash-medium", "Model: [flash]") + "true\n"
	}
	return map[string][]outcome{
		"work-tasks": {
			{"one --check, then one wave on its runner, both merged and stored", true, disjoint},
			{"run in two waves", false, tab + shout + check + wave("greet-tab") + "\n" + land("greet-tab") + wave("shout") + "\n" + land("shout")},
			{"no --check before the wave", false, strings.Replace(disjoint, check, "", 1)},
			{"--check after the wave", false, tab + shout + wave("greet-tab", "shout") + "\n" + check + land("greet-tab") + land("shout")},
			{"the wave without --runner", false, strings.Replace(disjoint, "fake-work.sh --runner docker", "fake-work.sh", 1)},
			{"a Kickoff names a plan", false, strings.Replace(disjoint, "for a tab-only name", "for PLAN-2026-11-02-greet-tab", 1)},
			{"a Kickoff without its Never line", false, strings.Replace(disjoint, "\\nNever: push", "\\nNever: tick", 1)},
			{"wrong commit subject", false, strings.Replace(disjoint, "'work: shout (work/shout)'", "'add shout'", 1)},
			{"branch kept", false, strings.Replace(disjoint, drop("shout"), "", 1)},
			{"records not stored", false, strings.Replace(disjoint, store("shout"), "", 1)},
			{"left dirty", false, disjoint + "echo x >>README.md\n"},
			{"a tier as the Model: line", false, tier(disjoint)},
		},
		"work-tasks-waves": {
			{"two waves, the second on the first's merge", true, waves},
			{"one wave, refused", false, bye + hi + check + wave("goodbye", "welcome") + " || true\n"},
			{"second wave never run", false, bye + hi + check + wave("goodbye") + "\n" + land("goodbye")},
			{"--check before each wave", false, strings.Replace(waves, wave("welcome"), check+wave("welcome"), 1)},
			{"the second wave without --runner", false, strings.Replace(waves, wave("welcome"), run("welcome"), 1)},
			{"second worker's records not stored", false, strings.Replace(waves, store("welcome"), "", 1)},
			{"second branch kept", false, strings.Replace(waves, drop("welcome"), "", 1)},
			{"a tier as the Model: line", false, tier(waves)},
		},
		"work-tasks-design": {
			{"design task routed, the other merged", true, design},
			{"design task dispatched", false, tab + persist + check + wave("greet-tab", "persist") + " || true\n" + land("greet-tab") + routed},
			{"a plan stored", false, design + "rec plan --title 'Persist greetings' <<<'# PLAN'\n"},
			{"not routed to the feature skill", false, tab + check + wave("greet-tab") + "\n" + land("greet-tab")},
			{"no --check before the wave", false, strings.Replace(design, check, "", 1)},
			{"the wave without --runner", false, strings.Replace(design, "fake-work.sh --runner docker", "fake-work.sh", 1)},
			{"nothing merged", false, routed},
			{"left dirty", false, design + "echo x >>README.md\n"},
			{"a tier as the Model: line", false, tier(design)},
		},
		"work-start": {
			{"one wave, both phases merged, ticked and stored", true, started},
			{"waited for go instead of starting", false, "echo 'Waiting for go' >\"$OUT\"\n"},
			{"run in two waves", false, p1 + p2 + run("greetings-p1") + "\n" + planLand("p1", "greetings-p1") + run("greetings-p2") + "\n" + planLand("p2", "greetings-p2") + tickPlan},
			{"Kickoff misses the plan ID", false, strings.Replace(started, p1, strings.ReplaceAll(p1, "PLAN-2026-11-02-greetings", "the plan"), 1)},
			{"Kickoff without its Never line", false, strings.Replace(started, "\\nNever: push", "\\nNever: tick", 1)},
			{"added a Model: line not in the plan", false, strings.Replace(started, "\\nNever: push", "\\nModel: gemini-3.8-flash-medium\\nNever: push", 1)},
			{"wrong commit subject", false, strings.Replace(started, "'PLAN-2026-11-02-greetings p2 (work/greetings-p2)'", "'work: greetings-p2 (work/greetings-p2)'", 1)},
			{"plan not ticked", false, strings.Replace(started, tickPlan, "", 1)},
			{"plan wiped instead of ticked", false, strings.Replace(started, tickPlan, "printf '# PLAN-2026-11-02-greetings\\n' | \"$F\" record edit PLAN-2026-11-02-greetings --body\n", 1)},
			{"records not stored", false, strings.Replace(started, store("greetings-p2"), "", 1)},
			{"branch kept", false, strings.Replace(started, drop("greetings-p2"), "", 1)},
			{"left dirty", false, started + "echo x >>README.md\n"},
		},
		"work-refuse": {
			{"both refused, naming each missing gate", true, refused},
			{"started the open-cold plan anyway", false, refused + p1 + run("greetings-p1") + "\n"},
			{"wrote a Kickoff before refusing", false, refused + p1},
			{"left a worker branch", false, refused + "git branch work/greet-alpha-p1\n"},
			{"missed the cold-read gate in the first refusal", false, "echo 'Refused PLAN-2026-11-02-greet-alpha.' >\"$OUT\"\n" + refuseWorkers},
			{"missed the workers D-row gate in the second refusal", false, refuseCold + "echo 'Refused PLAN-2026-11-02-greet-beta.' >\"$OUT2\"\n"},
			{"ticked the cold-read box to get past the gate", false, refused + "printf '# PLAN\\n\\n- [x] Cold-reader check\\n' | \"$F\" record edit PLAN-2026-11-02-greet-alpha --body\n"},
			{"added a workers D-row to get past the gate", false, refused + "printf '# PLAN\\n\\n## Decisions\\n\\nD1 — 2026-11-02 — workers: yes — by: lead\\n' | \"$F\" record edit PLAN-2026-11-02-greet-beta --body\n"},
			{"left dirty", false, refused + "echo x >>README.md\n"},
		},
	}
}
