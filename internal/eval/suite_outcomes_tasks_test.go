package eval

import "strings"

// taskOutcomes is the outcomes of the work-tasks rows (scenario tasks): the
// /work lead writes plan-less Kickoffs, runs the stand-in verb the fixture
// installs, squash-merges each PASS and stores each worker's records.
func taskOutcomes() map[string][]outcome {
	kick := func(name, files, task string) string {
		return "mkdir -p .agents/scratch/work/kickoffs\n" +
			"printf 'Project: demo; do this task only, as a worker, with no plan: %s\\nFiles: %s\\nModel: flash\\nNever: push; tick a plan record or TODO.md.\\n' '" +
			task + "' '" + files + "' >.agents/scratch/work/kickoffs/" + name + ".md\n"
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
	drop := func(n string) string { return "git branch -qD work/" + n + "\n" }
	store := func(n string) string {
		return "for f in .agents/scratch/work/*/" + n + "/records/*.md; do id=$(basename \"$f\" .md); " +
			"rec \"$(printf '%s' \"${id%%-*}\" | tr A-Z a-z)\" --id \"$id\" --title-file \"${f%.md}.title\" <\"$f\"; done\n"
	}
	land := func(n string) string { return merge(n) + drop(n) + store(n) }

	tab := kick("greet-tab", "internal/greet/greet_test.go", "Add a table test TestHelloTab to internal/greet for a tab-only name")
	shout := kick("shout", "internal/shout/", "Add a package internal/shout with Shout(s string) string and its table test")
	disjoint := tab + shout + run("greet-tab", "shout") + "\n" + land("greet-tab") + land("shout")

	bye := kick("goodbye", "internal/greet/", "Add Goodbye(name string) string to internal/greet/greet.go with a table test")
	hi := kick("welcome", "internal/greet/", "Add Welcome(name string) string to internal/greet/greet.go with a table test")
	waves := bye + hi + run("goodbye") + "\n" + land("goodbye") + run("welcome") + "\n" + land("welcome")

	routed := "echo 'Persist every greeting is a design question (SQLite or Postgres): routed to the feature skill, Kickoff /feature Persist every greeting' >\"$OUT\"\n"
	design := tab + run("greet-tab") + "\n" + land("greet-tab") + routed
	persist := kick("persist", "internal/store/", "Persist every greeting, choosing between SQLite and Postgres")

	return map[string][]outcome{
		"work-tasks": {
			{"one wave, both merged and stored", true, disjoint},
			{"run in two waves", false, tab + shout + run("greet-tab") + "\n" + land("greet-tab") + run("shout") + "\n" + land("shout")},
			{"a Kickoff names a plan", false, strings.Replace(disjoint, "for a tab-only name", "for PLAN-2026-11-02-greet-tab", 1)},
			{"a Kickoff without its Never line", false, strings.Replace(disjoint, "\\nNever: push", "\\nNever: tick", 1)},
			{"wrong commit subject", false, strings.Replace(disjoint, "'work: shout (work/shout)'", "'add shout'", 1)},
			{"branch kept", false, strings.Replace(disjoint, drop("shout"), "", 1)},
			{"records not stored", false, strings.Replace(disjoint, store("shout"), "", 1)},
			{"left dirty", false, disjoint + "echo x >>README.md\n"},
		},
		"work-tasks-waves": {
			{"two waves, the second on the first's merge", true, waves},
			{"one wave, refused", false, bye + hi + run("goodbye", "welcome") + " || true\n"},
			{"second wave never run", false, bye + hi + run("goodbye") + "\n" + land("goodbye")},
			{"second worker's records not stored", false, strings.Replace(waves, store("welcome"), "", 1)},
			{"second branch kept", false, strings.Replace(waves, drop("welcome"), "", 1)},
		},
		"work-tasks-design": {
			{"design task routed, the other merged", true, design},
			{"design task dispatched", false, tab + persist + run("greet-tab", "persist") + " || true\n" + land("greet-tab") + routed},
			{"a plan stored", false, design + "rec plan --title 'Persist greetings' <<<'# PLAN'\n"},
			{"not routed to the feature skill", false, tab + run("greet-tab") + "\n" + land("greet-tab")},
			{"nothing merged", false, routed},
			{"left dirty", false, design + "echo x >>README.md\n"},
		},
	}
}
