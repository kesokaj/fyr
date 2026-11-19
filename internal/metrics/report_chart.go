package metrics

import (
	"fmt"
	"html/template"
	"math"
	"sort"
	"strings"
)

// lineRows is a line chart's table, a row per day with "n/a" for a missing
// value, and the largest value in it.
func lineRows(series []serie, days []Day, get func(Day, string) (float64, bool), format string) ([][]string, float64) {
	var rows [][]string
	var max float64
	for _, d := range days {
		row := []string{d.Date}
		for _, s := range series {
			v, ok := get(d, s.Key)
			if !ok {
				row = append(row, "n/a")
				continue
			}
			max = math.Max(max, v)
			row = append(row, fmt.Sprintf(format, v))
		}
		rows = append(rows, row)
	}
	return rows, max
}

// plot maps a line chart's day index and value to SVG coordinates.
type plot struct {
	days   int
	top    float64 // the value at the top of the y axis
	iw, ih float64 // the inner width and height
}

func newPlot(days int, top float64) plot {
	return plot{days: days, top: top, iw: chartW - padL - padR, ih: chartH - padT - padB}
}

func (p plot) x(i int) float64 {
	if p.days < 2 {
		return padL + p.iw/2
	}
	return padL + p.iw*float64(i)/float64(p.days-1)
}

func (p plot) y(v float64) float64 { return padT + p.ih - p.ih*v/p.top }

// grid draws the value grid with its ticks, and the day ticks.
func (p plot) grid(b *strings.Builder, days []Day) {
	for i := 0; i <= 4; i++ {
		v := p.top * float64(i) / 4
		fmt.Fprintf(b, `<line class="grid" x1="%.1f" x2="%.1f" y1="%.1f" y2="%.1f"/>`, padL, chartW-padR, p.y(v), p.y(v))
		fmt.Fprintf(b, `<text class="tick" x="%.1f" y="%.1f" text-anchor="end">%s</text>`, padL-6, p.y(v)+4, esc(short(v)))
	}
	for i, d := range days {
		if len(days) <= 8 || i == 0 || i == len(days)-1 || i%(len(days)/4+1) == 0 {
			dayLbl := d.Date
			if len(dayLbl) >= 5 {
				dayLbl = dayLbl[5:]
			}
			fmt.Fprintf(b, `<text class="tick" x="%.1f" y="%.1f" text-anchor="middle">%s</text>`, p.x(i), chartH-8, esc(dayLbl))
		}
	}
}

// endLabel is a drawn line's name, at the line's last point.
type endLabel struct {
	y     float64
	x     float64
	label string
}

// lines draws one line per series, then a marker per day over every line,
// and returns each drawn line's end label.
func (p plot) lines(b *strings.Builder, series []serie, days []Day, get func(Day, string) (float64, bool), format string) []endLabel {
	var ends []endLabel
	var dots strings.Builder // markers over every line
	for _, s := range series {
		var pts []string
		var lastX, lastY float64
		drawn := false
		for i, d := range days {
			v, ok := get(d, s.Key)
			if !ok {
				continue
			}
			pts = append(pts, fmt.Sprintf("%.1f,%.1f", p.x(i), p.y(v)))
			fmt.Fprintf(&dots, `<circle class="dot %s" cx="%.1f" cy="%.1f" r="4"><title>%s, %s: %s</title></circle>`,
				s.Class, p.x(i), p.y(v), esc(s.Label), esc(d.Date), esc(fmt.Sprintf(format, v)))
			lastX, lastY, drawn = p.x(i), p.y(v), true
		}
		if len(pts) > 1 {
			fmt.Fprintf(b, `<polyline class="line %s" points="%s"/>`, s.Class, strings.Join(pts, " "))
		}
		if drawn {
			ends = append(ends, endLabel{lastY + 4, lastX + 8, s.Label})
		}
	}
	b.WriteString(dots.String())
	return ends
}

// placeEndLabels draws the end labels, one per drawn line, nudged apart so
// two lines ending close together keep readable names.
func placeEndLabels(b *strings.Builder, ends []endLabel) {
	sort.Slice(ends, func(i, j int) bool { return ends[i].y < ends[j].y })
	for i := 1; i < len(ends); i++ {
		if ends[i].y-ends[i-1].y < 13 {
			ends[i].y = ends[i-1].y + 13
		}
	}
	for _, e := range ends {
		fmt.Fprintf(b, `<text class="direct" x="%.1f" y="%.1f">%s</text>`, e.x, e.y, esc(e.label))
	}
}

func niceMax(v float64) float64 {
	if v <= 0 {
		return 1
	}
	exp := math.Pow(10, math.Floor(math.Log10(v)))
	for _, m := range []float64{1, 2, 2.5, 5, 10} {
		if v <= m*exp {
			return m * exp
		}
	}
	return 10 * exp
}

func esc(s string) string { return template.HTMLEscapeString(s) }

// lineChart draws one line per series over the window's days, a 2px line
// with a marker per day carrying its value as a hover title, the series'
// name at the line's end, and the same numbers as a table.
func lineChart(title, note string, series []serie, days []Day, get func(Day, string) (float64, bool), format string) chart {
	c := chart{Title: title, Note: note, Table: table{Head: head("day", series)}}
	rows, max := lineRows(series, days, get, format)
	c.Table.Rows = rows
	p := newPlot(len(days), niceMax(max))
	var b strings.Builder
	fmt.Fprintf(&b, `<svg viewBox="0 0 %.0f %.0f" role="img" aria-label="%s">`, chartW, chartH, esc(title))
	p.grid(&b, days)
	placeEndLabels(&b, p.lines(&b, series, days, get, format))
	b.WriteString(`</svg>`)
	c.SVG = template.HTML(b.String())
	return c
}

// barChart draws horizontal bars, one group per category with a bar per
// series, a 2px gap between the bars, the value at each bar's end.
func barChart(title, note string, series []serie, keys []string, data map[string]map[string]int) chart {
	c := chart{Title: title, Note: note, Table: table{Head: head("", series)}}
	var max float64
	for _, k := range keys {
		row := []string{k}
		for _, s := range series {
			v := data[s.Key][k]
			max = math.Max(max, float64(v))
			row = append(row, fmt.Sprint(v))
		}
		c.Table.Rows = append(c.Table.Rows, row)
	}
	const barH, gap, groupGap, left = 10.0, 2.0, 10.0, 150.0
	n := float64(len(series))
	h := padT + float64(len(keys))*(n*barH+(n-1)*gap+groupGap) + 8
	if len(keys) == 0 {
		h = 60
	}
	iw := chartW - left - 60
	var b strings.Builder
	fmt.Fprintf(&b, `<svg viewBox="0 0 %.0f %.0f" role="img" aria-label="%s">`, chartW, h, esc(title))
	if len(keys) == 0 {
		b.WriteString(`<text class="tick" x="16" y="34">no data in this window</text>`)
	}
	top := niceMax(max)
	for i, k := range keys {
		gy := padT + float64(i)*(n*barH+(n-1)*gap+groupGap)
		fmt.Fprintf(&b, `<text class="cat" x="%.1f" y="%.1f" text-anchor="end">%s</text>`, left-8, gy+(n*barH+(n-1)*gap)/2+4, esc(k))
		for j, s := range series {
			v := float64(data[s.Key][k])
			by := gy + float64(j)*(barH+gap)
			bw := iw * v / top
			if v > 0 && bw < 2 {
				bw = 2
			}
			fmt.Fprintf(&b, `<rect class="bar %s" x="%.1f" y="%.1f" width="%.1f" height="%.1f" rx="2"><title>%s, %s: %.0f</title></rect>`,
				s.Class, left, by, bw, barH, esc(s.Label), esc(k), v)
			if v > 0 {
				fmt.Fprintf(&b, `<text class="value" x="%.1f" y="%.1f">%.0f</text>`, left+bw+4, by+barH-1, v)
			}
		}
	}
	b.WriteString(`</svg>`)
	c.SVG = template.HTML(b.String())
	return c
}

// passKChart: per sandbox label, graded trials passed of n with the Wilson
// 95 % interval as a whisker, the error trials, and whether all passed.
func passKChart(p map[string]PassK) chart {
	c := chart{Title: "Sandbox runs: passed of n, per label",
		Note:  "pass^k = 1 only when every graded trial of the label passed; the whisker is the Wilson 95 % interval of c of n; error trials are not graded; flip marks a label that both passed and failed",
		Table: table{Head: []string{"label", "passed", "k", "pass^k", "n", "c", "errors", "wilson95", "flip"}}}
	labels := sortedKeys(p)
	const barH, rowGap, left = 14.0, 10.0, 150.0
	h := padT + float64(len(labels))*(barH+rowGap) + 8
	iw := chartW - left - 150
	var b strings.Builder
	fmt.Fprintf(&b, `<svg viewBox="0 0 %.0f %.0f" role="img" aria-label="%s">`, chartW, h, esc(c.Title))
	for i, l := range labels {
		r := p[l]
		c.Table.Rows = append(c.Table.Rows, []string{l, fmt.Sprint(r.Passed), fmt.Sprint(r.K), fmt.Sprint(r.PassK),
			fmt.Sprint(r.N), fmt.Sprint(r.C), fmt.Sprint(r.Errors), wilsonText(r.Wilson95), fmt.Sprint(r.Flip)})
		passKRow(&b, l, r, padT+float64(i)*(barH+rowGap), left, iw, barH)
	}
	b.WriteString(`</svg>`)
	c.SVG = template.HTML(b.String())
	return c
}

// passKRow draws one label of the pass^k chart: its name, the track, the
// bar c of n, the whisker from the interval's low to its high end over the
// bar, and the value text; a label with no graded trial has an empty bar
// and no whisker.
func passKRow(b *strings.Builder, l string, r PassK, y, left, iw, barH float64) {
	frac := 0.0
	if r.N > 0 {
		frac = float64(r.C) / float64(r.N)
	}
	fmt.Fprintf(b, `<text class="cat" x="%.1f" y="%.1f" text-anchor="end">%s</text>`, left-8, y+barH-3, esc(l))
	fmt.Fprintf(b, `<rect class="track" x="%.1f" y="%.1f" width="%.1f" height="%.1f" rx="2"/>`, left, y, iw, barH)
	fmt.Fprintf(b, `<rect class="bar s1" x="%.1f" y="%.1f" width="%.1f" height="%.1f" rx="2"><title>%s: %d of %d passed, Wilson 95 %% %s</title></rect>`,
		left, y, iw*frac, barH, esc(l), r.C, r.N, wilsonText(r.Wilson95))
	if r.N > 0 {
		lo, hi, mid := left+iw*r.Wilson95[0], left+iw*r.Wilson95[1], y+barH/2
		fmt.Fprintf(b, `<line class="whisker" x1="%.1f" x2="%.1f" y1="%.1f" y2="%.1f"/>`, lo, hi, mid, mid)
		for _, x := range []float64{lo, hi} {
			fmt.Fprintf(b, `<line class="whisker" x1="%.1f" x2="%.1f" y1="%.1f" y2="%.1f"/>`, x, x, y, y+barH)
		}
	}
	text := fmt.Sprintf("%d of %d · pass^%d = %d", r.Passed, r.K, r.K, r.PassK)
	if r.Errors > 0 {
		text += fmt.Sprintf(" · %d errors", r.Errors)
	}
	if r.Flip == 1 {
		text += " · flip"
	}
	fmt.Fprintf(b, `<text class="value" x="%.1f" y="%.1f">%s</text>`, left+iw+6, y+barH-3, text)
}

func short(v float64) string {
	switch {
	case v >= 1e9:
		return fmt.Sprintf("%.1fG", v/1e9)
	case v >= 1e6:
		return fmt.Sprintf("%.1fM", v/1e6)
	case v >= 1e3:
		return fmt.Sprintf("%.0fk", v/1e3)
	case v == math.Trunc(v):
		return fmt.Sprintf("%.0f", v)
	}
	return fmt.Sprintf("%.2f", v)
}

// topKeys are the n categories with the most counts over every column.
func topKeys(m map[string]map[string]int, n int) []string {
	tot := map[string]int{}
	for _, byKey := range m {
		for k, v := range byKey {
			tot[k] += v
		}
	}
	keys := make([]string, 0, len(tot))
	for k := range tot {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if tot[keys[i]] != tot[keys[j]] {
			return tot[keys[i]] > tot[keys[j]]
		}
		return keys[i] < keys[j]
	})
	if len(keys) > n {
		keys = keys[:n]
	}
	return keys
}
