package metrics

import (
	"reflect"
	"strings"
	"testing"
)

// lineChart's drawing, pinned after the Phase 3 review (its finding 3): a
// one-day chart centres its marker and draws no line; a series with no
// value is "n/a" and gets no end label; end labels sort by height and the
// lower of two close ones is nudged 13px down.
func TestLineChartDrawing(t *testing.T) {
	series := []serie{{Key: "a", Label: "A", Class: "s1"}, {Key: "b", Label: "B", Class: "s2"}}
	get := func(vals map[string]map[string]float64) func(Day, string) (float64, bool) {
		return func(d Day, k string) (float64, bool) {
			v, ok := vals[d.Date][k]
			return v, ok
		}
	}

	one := lineChart("t", "", series, []Day{{Date: "2026-10-08"}},
		get(map[string]map[string]float64{"2026-10-08": {"a": 10}}), "%.0f")
	if want := [][]string{{"2026-10-08", "10", "n/a"}}; !reflect.DeepEqual(one.Table.Rows, want) {
		t.Errorf("rows %q, want %q", one.Table.Rows, want)
	}
	svg := string(one.SVG)
	for _, want := range []string{`<circle class="dot s1" cx="300.0"`, `<text class="direct" x="308.0" y="20.0">A</text>`} {
		if !strings.Contains(svg, want) {
			t.Errorf("one-day chart lacks %q:\n%s", want, svg)
		}
	}
	for _, unwanted := range []string{"<polyline", ">B</text>"} {
		if strings.Contains(svg, unwanted) {
			t.Errorf("one-day chart holds %q:\n%s", unwanted, svg)
		}
	}

	two := lineChart("t", "", series, []Day{{Date: "2026-10-08"}, {Date: "2026-10-09"}},
		get(map[string]map[string]float64{"2026-10-08": {"a": 1, "b": 1}, "2026-10-09": {"a": 10, "b": 9.5}}), "%.1f")
	svg = string(two.SVG)
	for _, want := range []string{
		`<polyline class="line s1" points="56.0,174.4 544.0,16.0"/>`,
		`<text class="direct" x="552.0" y="20.0">A</text>`,
		`<text class="direct" x="552.0" y="33.0">B</text>`,
	} {
		if !strings.Contains(svg, want) {
			t.Errorf("two-day chart lacks %q:\n%s", want, svg)
		}
	}
}
