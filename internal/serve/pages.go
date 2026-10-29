package serve

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/dream"
	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/metrics"
)

// printed runs one of cmd/fyr's printers into a page: its text in a <pre>,
// or on an error its output so far and the error, status 500.
func (r *Run) printed(w http.ResponseWriter, req *http.Request, title string, print func(io.Writer) error, head ...block) {
	var buf bytes.Buffer
	err := print(&buf)
	v := view{Title: title, Blocks: append(head, block{Pre: printableText(buf.String())})}
	if err != nil {
		r.fail(w, req, v, err)
		return
	}
	r.page(w, req, http.StatusOK, v)
}

// home is fyr status, the version, the project and the dream's line.
func (r *Run) home(w http.ResponseWriter, req *http.Request) {
	r.printed(w, req, "status", r.o.Status,
		block{Line: "fyr " + r.o.Version},
		block{Line: "dream: " + dream.StatusLine(r.o.Home)})
}

func (r *Run) context(w http.ResponseWriter, req *http.Request) {
	r.printed(w, req, "context", r.o.Render)
}

// check is fyr check, one at a time: each run takes minutes on a large
// checkout, and a reload must not stack another (plan D49).
func (r *Run) check(w http.ResponseWriter, req *http.Request) {
	if r.noProject(w, req, "check") {
		return
	}
	if !r.checks.TryLock() {
		r.page(w, req, http.StatusConflict, view{Title: "check", Blocks: []block{{Line: "fyr check is already running; reload when it finishes"}}})
		return
	}
	defer r.checks.Unlock()
	r.printed(w, req, "check", r.o.Check)
}

// memory is `fyr memory show` for the project.
func (r *Run) memory(w http.ResponseWriter, req *http.Request) {
	if r.noProject(w, req, "memory") {
		return
	}
	text, err := brain.MemoryShow(brain.Options{Home: r.o.Home, Dir: r.o.Dir})
	if err != nil {
		r.fail(w, req, view{Title: "memory"}, storeErr("brain store", err))
		return
	}
	r.page(w, req, http.StatusOK, view{Title: "memory", Blocks: []block{{Pre: printableText(text)}}})
}

// metricsLine is what /metrics and /metrics/report say with no store; Lstat
// first, because metrics.Open would create one.
func (r *Run) metricsLine() (string, error) {
	path := metrics.Path(r.o.Home)
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return "no metrics store at " + path + "; run fyr metrics ingest", nil
	} else if err != nil {
		return "", storeErr("metrics store", err)
	}
	return "", nil
}

func (r *Run) metrics(w http.ResponseWriter, req *http.Request) {
	v := view{Title: "metrics"}
	line, err := r.metricsLine()
	switch {
	case err != nil:
		r.fail(w, req, v, err)
		return
	case line != "":
		v.Blocks = []block{{Line: line}}
	default:
		v.Blocks = []block{{Link: &link{Href: r.base + "/metrics/report", Text: "the metrics report (Antigravity sessions, the last 30 days)"}}}
	}
	r.page(w, req, http.StatusOK, v)
}

// metricsReport is the `fyr metrics report` document for the default view,
// served whole: no layout.
func (r *Run) metricsReport(w http.ResponseWriter, req *http.Request) {
	v := view{Title: "metrics report"}
	line, err := r.metricsLine()
	if err != nil {
		r.fail(w, req, v, err)
		return
	}
	if line != "" {
		v.Blocks = []block{{Line: line}}
		r.page(w, req, http.StatusNotFound, v)
		return
	}
	var page bytes.Buffer
	if err := renderMetrics(r.o.Home, &page); err != nil {
		r.fail(w, req, v, storeErr("metrics store", err))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(page.Bytes())
}

func renderMetrics(home string, w io.Writer) error {
	s, err := metrics.Open(metrics.Path(home))
	if err != nil {
		return err
	}
	defer s.Close()
	sum, err := s.Show(metrics.ShowOptions{})
	if err != nil {
		return err
	}
	return metrics.RenderReport(w, sum, time.Now())
}

// evals is the project's testing records that `fyr eval` wrote.
func (r *Run) evals(w http.ResponseWriter, req *http.Request) {
	if r.noProject(w, req, "evals") {
		return
	}
	v := view{Title: "evals"}
	recs, err := brain.List(brain.Options{Home: r.o.Home, Dir: r.o.Dir}, brain.ListFilter{Kind: "testing"})
	if err != nil {
		r.fail(w, req, v, storeErr("brain store", err))
		return
	}
	var rows []recordRow
	for _, rec := range recs {
		if strings.HasPrefix(rec.Title, "Eval — ") {
			rows = append(rows, r.rowOf(rec, false))
		}
	}
	v.Blocks = []block{listBlock(rows, "no eval results: fyr eval writes one testing record per run")}
	r.page(w, req, http.StatusOK, v)
}

// dream is the dream's status line and the tail of its log.
func (r *Run) dream(w http.ResponseWriter, req *http.Request) {
	tail := readDreamLog(filepath.Join(r.o.Home, harness.StateDir, "dream.log"))
	r.page(w, req, http.StatusOK, view{Title: "dream", Blocks: []block{
		{Line: dream.StatusLine(r.o.Home)},
		{Heading: "dream.log, the last 200 lines", Pre: tail},
	}})
}

// storeErr names the store an error came from.
func storeErr(store string, err error) error {
	return errors.New(store + ": " + err.Error())
}
