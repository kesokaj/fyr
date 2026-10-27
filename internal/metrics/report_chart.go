package metrics

import (
	"fmt"
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
			fmt.Fprintf(b, `<text class="tick" x="%.1f" y="%.1f" text-anchor="middle">%s</text>`, p.x(i), chartH-8, esc(d.Date[5:]))
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
