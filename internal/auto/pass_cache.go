package auto

import (
	"os"
	"path/filepath"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/metrics"
)

type convCacheKey struct {
	sinceNano, cutoffNano int64
	hasSince, hasCutoff   bool
}

func makeConvCacheKey(since, cutoff time.Time) convCacheKey {
	return convCacheKey{
		sinceNano:  since.UnixNano(),
		cutoffNano: cutoff.UnixNano(),
		hasSince:   !since.IsZero(),
		hasCutoff:  !cutoff.IsZero(),
	}
}

type projectPassCache struct {
	root          string
	cutoff        time.Time
	hasCutoff     bool
	convs         map[convCacheKey][]convSource
	metricsOpened bool
	metricsStore  *metrics.Store
	metricsTmp    string
	summaryLoaded bool
	summary       *metrics.Summary
	propsLoaded   bool
	props         []brain.Record
	propsErr      error
}

// beginProjectPass installs a per-project sweep cache so task selection and
// learning-input construction share one cutoff, conversation slice,
// metrics.Snapshot, and open-proposals query.
func (r *run) beginProjectPass(root string) func() {
	r.sweepCache = &projectPassCache{
		root:  root,
		convs: map[convCacheKey][]convSource{},
	}
	return func() {
		r.finishPassMetrics()
		r.sweepCache = nil
	}
}

// finishPassMetrics closes and deletes the cached metrics snapshot tempdir so
// nothing stays open across a 30-minute worker run.
func (r *run) finishPassMetrics() {
	if r.sweepCache == nil {
		return
	}
	if r.sweepCache.metricsStore != nil {
		_ = r.sweepCache.metricsStore.Close()
		r.sweepCache.metricsStore = nil
	}
	if r.sweepCache.metricsTmp != "" {
		_ = os.RemoveAll(r.sweepCache.metricsTmp)
		r.sweepCache.metricsTmp = ""
	}
}

func (r *run) sweepConversationCutoff() time.Time {
	if r.sweepCache == nil {
		return r.conversationCutoff()
	}
	if !r.sweepCache.hasCutoff {
		r.sweepCache.cutoff = r.conversationCutoff()
		r.sweepCache.hasCutoff = true
	}
	return r.sweepCache.cutoff
}

func (r *run) sweepOpenProposals() ([]brain.Record, error) {
	g := brain.Options{Home: r.o.Home, Global: true, Now: r.o.Now}
	if r.sweepCache == nil {
		return brain.List(g, brain.ListFilter{Kind: "proposal", Open: true})
	}
	if !r.sweepCache.propsLoaded {
		r.sweepCache.props, r.sweepCache.propsErr = brain.List(g, brain.ListFilter{Kind: "proposal", Open: true})
		r.sweepCache.propsLoaded = true
	}
	return r.sweepCache.props, r.sweepCache.propsErr
}

func (r *run) openMetricsSnapshot() (*metrics.Store, string, error) {
	mp := metrics.Path(r.o.Home)
	if _, err := os.Lstat(mp); err != nil {
		return nil, "", err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(mp), "auto-metrics-")
	if err != nil {
		return nil, "", err
	}
	s, err := metrics.Snapshot(mp, tmp)
	if err != nil {
		_ = os.RemoveAll(tmp)
		return nil, "", err
	}
	return s, tmp, nil
}

func (r *run) sweepMetricsSnapshot() (*metrics.Store, func()) {
	if r.sweepCache == nil {
		s, tmp, err := r.openMetricsSnapshot()
		if err != nil {
			return nil, func() {}
		}
		return s, func() {
			_ = s.Close()
			_ = os.RemoveAll(tmp)
		}
	}
	if !r.sweepCache.metricsOpened {
		r.sweepCache.metricsOpened = true
		if s, tmp, err := r.openMetricsSnapshot(); err == nil {
			r.sweepCache.metricsStore = s
			r.sweepCache.metricsTmp = tmp
		}
	}
	return r.sweepCache.metricsStore, func() {}
}
