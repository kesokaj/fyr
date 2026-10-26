package dream

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A fresh copy belongs to a live dry run, which takes no lock: only copies
// older than an hour are a killed run's leftovers.
func TestRemoveCopiesKeepsFreshOnes(t *testing.T) {
	conf := t.TempDir()
	write(t, filepath.Join(conf, "dream-projects-1", "search.db"), "db", time.Now())
	write(t, filepath.Join(conf, "dream-projects-2", "search.db"), "db", time.Now().Add(-2*time.Hour))
	os.Chtimes(filepath.Join(conf, "dream-projects-1"), time.Now(), time.Now())
	os.Chtimes(filepath.Join(conf, "dream-projects-2"), time.Now().Add(-2*time.Hour), time.Now().Add(-2*time.Hour))
	removeCopies(conf, time.Now())
	if _, err := os.Stat(filepath.Join(conf, "dream-projects-1")); err != nil {
		t.Error("a fresh copy was removed")
	}
	if _, err := os.Stat(filepath.Join(conf, "dream-projects-2")); !os.IsNotExist(err) {
		t.Error("an old copy was kept")
	}
}

// TestRemoveCopiesUsesInjectedClock: the age check used the wall clock, so the
// one-hour rule had no deterministic test (audit of 2026-09-30, SD11).
func TestRemoveCopiesUsesInjectedClock(t *testing.T) {
	conf := t.TempDir()
	mt := time.Now().Add(-2 * time.Hour)
	write(t, filepath.Join(conf, "dream-projects-3", "search.db"), "db", mt)
	os.Chtimes(filepath.Join(conf, "dream-projects-3"), mt, mt)
	removeCopies(conf, mt.Add(10*time.Minute))
	if _, err := os.Stat(filepath.Join(conf, "dream-projects-3")); err != nil {
		t.Error("a copy ten minutes old by the injected clock was removed")
	}
	removeCopies(conf, mt.Add(2*time.Hour))
	if _, err := os.Stat(filepath.Join(conf, "dream-projects-3")); !os.IsNotExist(err) {
		t.Error("a copy two hours old by the injected clock was kept")
	}
}

// The metrics copies (full Antigravity conversations in metrics-agy-*, a
// copy of the store in dream-metrics-*) are swept like dream's own: a killed
// run must not leave them for good (security review 2026-10-08).
func TestRemoveCopiesSweepsMetricsCopies(t *testing.T) {
	conf := t.TempDir()
	mt := time.Now().Add(-2 * time.Hour)
	for _, d := range []string{"metrics-agy-1", "dream-metrics-2", "unrelated-3"} {
		write(t, filepath.Join(conf, d, "x.db"), "db", mt)
		os.Chtimes(filepath.Join(conf, d), mt, mt)
	}
	removeCopies(conf, time.Now())
	for _, d := range []string{"metrics-agy-1", "dream-metrics-2"} {
		if _, err := os.Stat(filepath.Join(conf, d)); !os.IsNotExist(err) {
			t.Errorf("%s was kept", d)
		}
	}
	if _, err := os.Stat(filepath.Join(conf, "unrelated-3")); err != nil {
		t.Error("a directory that is not a copy was removed")
	}
}
