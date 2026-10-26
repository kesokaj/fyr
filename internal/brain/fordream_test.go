package brain

import (
	"testing"
	"time"
)

// Gate review of v3.0.0, finding 2: one record dated after now must not
// hide the project's real last update (the dream's "in use" test reads
// Last), and is never news to the dream.
func TestForDreamIgnoresARecordFromTheFuture(t *testing.T) {
	dir, home := repo(t), t.TempDir()
	at := func(d time.Duration) Options {
		o := opts(dir, home)
		o.Now = func() time.Time { return fixed.Add(d) }
		return o
	}
	add(t, at(-time.Hour), "change", "an hour ago", "body")
	add(t, at(48*time.Hour), "change", "from the future", "body")
	in, err := ForDream(home, dir, fixed.Add(-24*time.Hour), fixed)
	if err != nil {
		t.Fatal(err)
	}
	if want := fixed.Add(-time.Hour); !in.Last.Equal(want) {
		t.Errorf("Last %v, want the record an hour old, %v", in.Last, want)
	}
	if len(in.Trail) != 1 || in.Trail[0].Title != "an hour ago" {
		t.Errorf("the future record is news: %+v", in.Trail)
	}
}
