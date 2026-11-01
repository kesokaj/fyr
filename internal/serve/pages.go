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

// home is fyr status, the version, the project or all-projects overview,
// the dream's line and the action forms.
func (r *Run) home(w http.ResponseWriter, req *http.Request) {
	head := []block{
		{Line: "fyr " + r.o.Version},
		{Line: "dream: " + dream.StatusLine(r.o.Home)},
		r.overviewBlock(),
	}
	if r.root != "" {
		head = append(head, r.actionForm("/actions/index", "run fyr index"))
	}
	head = append(head, r.actionForm("/actions/metrics-ingest", "run fyr metrics ingest"))
	r.printed(w, req, "status", r.o.Status, head...)
}

func (r *Run) context(w http.ResponseWriter, req *http.Request) {
	r.printed(w, req, "context", r.o.Render)
}

// check is fyr check, one at a time: a run reads git history and can take
// a while on a large checkout, and a reload must not stack another (plan D49).
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

// memory is `fyr memory show` for the project, or active memories across
// every project scope when started outside a checkout (D5).
func (r *Run) memory(w http.ResponseWriter, req *http.Request) {
	var text string
	var err error
	if r.root != "" {
		text, err = brain.MemoryShow(brain.Options{Home: r.o.Home, Dir: r.o.Dir})
	} else {
		text, err = r.allMemoriesText()
	}
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
		v.Blocks = r.metricsBlocks()
	}
	v.Blocks = append(v.Blocks, r.actionForm("/actions/metrics-ingest", "run fyr metrics ingest"))
	r.page(w, req, http.StatusOK, v)
}

// metricsReport is the `fyr metrics report` document: scoped to the project
// inside a checkout, or across all projects with eval rows outside one,
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
	if err := renderMetrics(r.o.Home, r.root, &page); err != nil {
		r.fail(w, req, v, storeErr("metrics store", err))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(page.Bytes())
}

func renderMetrics(home, project string, w io.Writer) error {
	s, err := metrics.Open(metrics.Path(home))
	if err != nil {
		return err
	}
	defer s.Close()
	sum, err := s.Show(metrics.ShowOptions{Project: project})
	if err != nil {
		return err
	}
	var evals *metrics.EvalRows
	if project == "" {
		evals, err = s.Evals(metrics.EvalOptions{})
		if err != nil {
			return err
		}
	}
	return metrics.RenderReport(w, sum, evals, time.Now())
}

// evals is the per-row view of this home's trials, then the project's (or,
// outside a checkout, every scope's) testing records that `fyr eval` wrote.
func (r *Run) evals(w http.ResponseWriter, req *http.Request) {
	v := view{Title: "evals"}
	table, err := r.evalRows()
	if err != nil {
		r.fail(w, req, v, err)
		return
	}
	var recs []brain.Record
	if r.root != "" {
		recs, err = brain.List(brain.Options{Home: r.o.Home, Dir: r.o.Dir}, brain.ListFilter{Kind: "testing"})
	} else {
		recs, err = r.listAll(brain.ListFilter{Kind: "testing"})
	}
	if err != nil {
		r.fail(w, req, v, storeErr("brain store", err))
		return
	}
	var rows []recordRow
	for _, rec := range recs {
		if strings.HasPrefix(rec.Title, "Eval — ") {
			rows = append(rows, r.rowOf(rec))
		}
	}
	b := listBlock(rows, "no eval results: fyr eval writes one testing record per run")
	b.ShowScope = r.root == ""
	v.Blocks = []block{table, b}
	r.page(w, req, http.StatusOK, v)
}

// evalRows is `fyr metrics evals` over the last 30 days of this home's
// store, as its text in a <pre>, or the line that names a missing store:
// the trials live in the home fyr eval wrote to (SPEC-fyr-serve).
func (r *Run) evalRows() (block, error) {
	line, err := r.metricsLine()
	if err != nil || line != "" {
		return block{Line: line}, err
	}
	s, err := metrics.Open(metrics.Path(r.o.Home))
	if err != nil {
		return block{}, storeErr("metrics store", err)
	}
	defer s.Close()
	e, err := s.Evals(metrics.EvalOptions{})
	if err != nil {
		return block{}, storeErr("metrics store", err)
	}
	var text bytes.Buffer
	e.WriteText(&text)
	return block{Heading: "fyr metrics evals, the last 30 days", Pre: text.String()}, nil
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
