package dream

import (
	"strings"
	"testing"
)

func TestRenderSystemdUnits(t *testing.T) {
	service, timer := renderSystemd("/h/.local/bin/fyr", "/h", "/h/.local/bin:/usr/bin")
	for _, want := range []string{"Type=oneshot", "TimeoutStartSec=2h", `Environment="PATH=/h/.local/bin:/usr/bin"`, `ExecStart="/h/.local/bin/fyr" dream --all --home "/h"`} {
		if !strings.Contains(service, want) {
			t.Errorf("service lacks %q:\n%s", want, service)
		}
	}
	for _, want := range []string{"OnCalendar=*-*-* 03:00", "Persistent=true", "WantedBy=timers.target"} {
		if !strings.Contains(timer, want) {
			t.Errorf("timer lacks %q:\n%s", want, timer)
		}
	}
}

func TestRenderLaunchdPlist(t *testing.T) {
	plist := renderLaunchd("com.fyr.dream", "/Users/o & p/.local/bin/fyr", "/Users/o & p", "/usr/bin")
	for _, want := range []string{"<string>com.fyr.dream</string>", "<string>/Users/o &amp; p/.local/bin/fyr</string>", "<string>dream</string>", "<string>--all</string>", "<string>--home</string><string>/Users/o &amp; p</string>", "<key>Hour</key><integer>3</integer>", "<key>PATH</key><string>/usr/bin</string>"} {
		if !strings.Contains(plist, want) {
			t.Errorf("plist lacks %q:\n%s", want, plist)
		}
	}
}

// TestRenderSystemdEscapesDollar: systemd expands $VAR and ${VAR} in
// ExecStart, inside quotes too; $$ is a literal dollar sign.
func TestRenderSystemdEscapesDollar(t *testing.T) {
	service, _ := renderSystemd("/h/a$b/fyr", "/h/${X}", "/p")
	if !strings.Contains(service, `ExecStart="/h/a$$b/fyr" dream --all --home "/h/$${X}"`) {
		t.Errorf("a $ in ExecStart was not escaped:\n%s", service)
	}
}

// TestShellQuote: a path printed inside a command the operator may paste.
func TestShellQuote(t *testing.T) {
	for in, want := range map[string]string{
		"/home/u":      "'/home/u'",
		"/h/a b":       "'/h/a b'",
		"/h/$(id)":     "'/h/$(id)'",
		"/h/it's here": `'/h/it'\''s here'`,
	} {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %s, want %s", in, got, want)
		}
	}
}
