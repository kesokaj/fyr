package auto

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestUsageCapture(t *testing.T) {
	data, err := os.ReadFile("testdata/usage.txt")
	if err != nil {
		t.Fatal(err)
	}
	pools, err := parseUsage(string(data))
	if err != nil {
		t.Fatal(err)
	}
	want := []Pool{{"Gemini Models", 88, 71}, {"Claude and GPT models", disabled, 0}}
	if len(pools) != 2 || pools[0] != want[0] || pools[1] != want[1] {
		t.Errorf("%+v", pools)
	}
}

func usageText(week, five string) string {
	return "P\tWeekly Limit Remaining\t" + week + "\t2026-11-07T08:56:00Z\nP\tFive Hour Limit Remaining\t" + five + "\t2026-11-01T04:56:00Z\n"
}

func TestUsageRefusals(t *testing.T) {
	cases := map[string]string{
		"empty":                      "",
		"prose":                      "You are not logged in.\n",
		"three fields":               "P\tWeekly Limit Remaining\t71%\n",
		"five fields":                "P\tWeekly Limit Remaining\t71%\tx\ty\n",
		"no pool name":               "\tWeekly Limit Remaining\t71%\tx\n",
		"unknown window":             "P\tDaily Limit Remaining\t71%\tx\n",
		"a window lacks":             "P\tWeekly Limit Remaining\t71%\tx\n",
		"over 100":                   usageText("101%", "50%"),
		"a fraction":                 usageText("0.71", "50%"),
		"negative":                   usageText("-1%", "50%"),
		"no percent sign":            usageText("71", "50%"),
		"a line with five fields":    strings.Replace(usageText("71%", "50%"), "\n", "\textra\n", 1),
		"a line with three fields":   strings.Replace(usageText("71%", "50%"), "\t2026-11-07T08:56:00Z", "", 1),
		"no pool name, both windows": strings.ReplaceAll(usageText("71%", "50%"), "P\t", "\t"),
		"a line twice":               usageText("71%", "50%") + "P\tWeekly Limit Remaining\t90%\tx\n",
		"a trailing line":            usageText("71%", "50%") + "warning: update available\n",
	}
	for name, text := range cases {
		t.Run(name, func(t *testing.T) {
			if p, err := parseUsage(text); err == nil {
				t.Errorf("parsed: %+v", p)
			}
		})
	}
}

func TestUsageQuotesAreBounded(t *testing.T) {
	long := strings.Repeat("x", 2<<20)
	for name, text := range map[string]string{
		"a line":   long + "\n",
		"a pool":   strings.ReplaceAll(usageText("90%", "90%"), "P\t", long+"\t"),
		"a value":  usageText(long, "90%"),
		"a window": "P\t" + long + "\t90%\tx\n",
	} {
		t.Run(name, func(t *testing.T) {
			line, stop := gateBudget(0, text, nil)
			if !stop || len(line) > 2*lineLimit {
				t.Errorf("stop %v, %d bytes", stop, len(line))
			}
		})
	}
}

func TestUsagePoolCountIsBounded(t *testing.T) {
	var b strings.Builder
	for i := range poolsLimit + 1 {
		b.WriteString(strings.ReplaceAll(usageText("90%", "90%"), "P\t", "P"+strconv.Itoa(i)+"\t"))
	}
	if _, err := parseUsage(b.String()); err == nil {
		t.Error("parsed")
	}
}

func TestUsageFloors(t *testing.T) {
	cases := []struct {
		name, week, five string
		runs             int
		err              error
		stop             bool
		want             string
	}{
		{name: "both above", week: "30%", five: "50%", want: "budget: P 50% of 5h, 30% of week remaining; floors 50/30"},
		{name: "week under", week: "29%", five: "50%", stop: true, want: "G9 budget:"},
		{name: "five under", week: "30%", five: "49%", stop: true, want: "G9 budget:"},
		{name: "five disabled", week: "90%", five: "disabled", stop: true, want: "disabled of 5h"},
		{name: "week disabled", week: "disabled", five: "90%", stop: true, want: "disabled of week"},
		{name: "day's runs spent", week: "90%", five: "90%", runs: 4, stop: true, want: "4 of 4 model runs"},
		{name: "one left", week: "90%", five: "90%", runs: 3, want: "budget:"},
		{name: "unreadable, good text beside it", week: "90%", five: "90%", err: errFake, stop: true, want: "unreadable"},
		{name: "unparsed", week: "x", five: "90%", stop: true, want: "unreadable"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			line, stop := gateBudget(c.runs, usageText(c.week, c.five), c.err)
			if stop != c.stop || !strings.Contains(line, c.want) {
				t.Errorf("%q, stop %v", line, stop)
			}
		})
	}
}

func TestUsageAnyPoolStops(t *testing.T) {
	text := usageText("90%", "90%") + strings.ReplaceAll(usageText("10%", "90%"), "P\t", "Q\t")
	if line, stop := gateBudget(0, text, nil); !stop || !strings.Contains(line, "Q 90% of 5h, 10% of week") {
		t.Errorf("%q", line)
	}
}
