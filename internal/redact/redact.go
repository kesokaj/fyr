// Package redact removes credentials from text before it leaves the machine:
// `fyr dream` passes every project input through it before the prompt
// goes to the model of the CLI it runs (R7, ADR-062). It is the one place
// the needle lists live.
package redact

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
)

const redactedValue = "[redacted by fyr]"

// Needles LOCATE a credential; they never define how much is removed. AKIA is
// a prefix and a PEM body carries no needle at all, so replacing the matched
// span would ship the key while reporting it redacted. What goes is always a
// whole line or a whole BEGIN-to-END block (D9, D13).
//
// prefixNeedles are vendor token shapes, matched case-sensitively: they are
// distinctive as written. Nothing shorter than a vendor's own prefix is
// listed — a bare `sk-` would match every "task-". Stored XORed with
// needleMask (0xA5, turning every ASCII byte into >= 0x80) and decoded lazily
// via sync.Once so compiled .rodata never holds printable vendor prefixes
// regardless of Go linker string packing (BUG-538).
const needleMask byte = 0xA5

var (
	needlesOnce   sync.Once
	prefixNeedles []string
	beginNeedle   string
	blockOpen     *regexp.Regexp
	blockClose    *regexp.Regexp
)

var maskedPrefixNeedles = []string{
	"\x88\x88\x88\x88\x88\xe7\xe0\xe2\xec\xeb", "\xf5\xf7\xec\xf3\xe4\xf1\xe0\x85\xee\xe0\xfc",
	"\xe4\xee\xec\xe4", "\xe4\xf6\xec\xe4", "\xe4\xec\xdf\xc4", "\xdc\xc4\x97\x9c\x8b", "\xe2\xea\xe6\xf6\xf5\xfd\x88",
	"\xc2\xcd\xd5\xfa", "\xc2\xcd\xca\xfa", "\xc2\xcd\xd6\xfa", "\xc2\xcd\xd0\xfa", "\xc2\xcd\xd7\xfa",
	"\xc2\xcc\xd1\xcd\xd0\xc7\xfa\xd5\xc4\xd1\xfa",
	"\xc2\xc9\xd5\xc4\xd1\x88", "\xc2\xc9\xc1\xd1\x88", "\xc2\xc9\xd7\xd1\x88", "\xc2\xc9\xd5\xd1\xd1\x88",
	"\xd6\xce\x88\xc4\xcb\xd1\x88", "\xd6\xce\x88\xd5\xd7\xca\xcf\x88",
	// Stripe webhook secrets and PyPI tokens (security review 2026-09-30, f3);
	// Stripe live/test keys stay in keywordNeedles (`sk_test_` sits in `task_test_`).
	"\xd2\xcd\xd6\xc0\xc6\xfa",
	"\xd5\xdc\xd5\xcc\x88\xe4\xc2\xe0\xec\xc6\xed\xc9\xd2\xc4\xf6\x90\xd3\xc6\xc8\xc6",
	"\xd5\xdc\xd5\xcc\x88\xe4\xc2\xe0\xeb\xc1\xe2\xf3\xdf\xc1\xe6\x90\xd2\xc0\xfd\xe7\xd5\xe9\xc8\x9c\xdc\xff\xd2",
	"\xd6\xce\x88\xd6\xd3\xc6\xc4\xc6\xc6\xd1\x88", "\xd6\xce\x88\xc4\xc1\xc8\xcc\xcb\x88", "\xd6\xce\x88\xeb\xca\xcb\xc0\x88",
	// An age key and DigitalOcean tokens (v1.14.0 gate, security f1).
	"\xe4\xe2\xe0\x88\xf6\xe0\xe6\xf7\xe0\xf1\x88\xee\xe0\xfc\x88",
	"\xc1\xca\xd5\xfa\xd3\x94\xfa", "\xc1\xca\xca\xfa\xd3\x94\xfa", "\xc1\xca\xd7\xfa\xd3\x94\xfa",
}

const (
	maskedBlockOpen  = "\x8d\x9a\xcc\x8c\x88\x88\x88\x88\x88\xe7\xe0\xe2\xec\xeb\xd9\xe7\xe0\xe2\xec\xeb\xfe\xe4\x88\xff\x85\xf8\x8f\xf5\xf7\xec\xf3\xe4\xf1\xe0\x85\xee\xe0\xfc"
	maskedBlockClose = "\x8d\x9a\xcc\x8c\x88\x88\x88\x88\x88\xe0\xeb\xe1\xfe\xfb\x88\xf8\x8f\x8d\x88\x88\x88\x88\x88\x8c\x9a\xd9\xe0\xeb\xe1\xfe\xe4\x88\xff\x85\xf8\x8f\xf5\xf7\xec\xf3\xe4\xf1\xe0\x85\xee\xe0\xfc"
)

func unmaskNeedle(masked string) string {
	b := make([]byte, len(masked))
	for i := range len(masked) {
		b[i] = masked[i] ^ needleMask
	}
	return string(b)
}

func ensureNeedles() {
	needlesOnce.Do(initNeedles)
}

func initNeedles() {
	prefixNeedles = make([]string, len(maskedPrefixNeedles))
	for i, m := range maskedPrefixNeedles {
		prefixNeedles[i] = unmaskNeedle(m)
	}
	beginNeedle = prefixNeedles[0]
	blockOpen = regexp.MustCompile(unmaskNeedle(maskedBlockOpen))
	blockClose = regexp.MustCompile(unmaskNeedle(maskedBlockClose))
}

// keywordNeedles are assignment and header shapes, matched without regard to
// case, because the common forms are GITHUB_TOKEN=, DB_PASSWORD: and
// Authorization: Bearer (security review 2026-09-17, finding 1). Pattern is
// the name a redaction records, never the matched text.
// secretRe is the "secret assignment" needle, named so its accept function
// can read its groups by name.
var secretRe = regexp.MustCompile(`(?i)(?P<flag>--)?(?P<key>(?:pass(?:word|wd|phrase)?|pwd|secret|token|api[_-]?key)[a-z0-9_-]*)["']?\s*(?P<sep>[:=]|\s)\s*(?:"(?P<dq>[^"]*)"|'(?P<sq>[^']*)'|(?P<bare>\S*))`)

var keywordNeedles = []struct {
	pattern string
	re      *regexp.Regexp
	// accept, when set, judges a match by its submatches; a needle whose
	// shape alone over-reaches (an assignment whose "value" is prose) gets
	// one (audit 2026-09-30, IN1).
	accept func(m []string) bool
}{
	// One rule per kind (audit 2026-09-30, IN1): a key holding a word of the pass family (password, passwd,
	// passphrase, pwd), secret, token or api key, followed by `:` or `=`,
	// or by a space when the key is a `--flag`; the value is the rest of
	// that token or the whole quoted string, on the same line. A quoted
	// value is a secret whatever it holds. An unquoted one is a secret from
	// four characters when the WHOLE key (not its first word) holds pass,
	// pwd or secret (`password=correct`, `TOKEN_PASSWORD=letmein`,
	// `--password letmein`), and from eight, or six with a digit, for a
	// token or API key (`token=abc123`), since tokens are long by nature. A
	// heading (`## Secrets: how`), a field name (`token: field`), a word
	// followed by a space (`passwords are hard`) or a short word is prose.
	// `token=abcdefg` and `secret = my passphrase` are the accepted
	// under-redaction, pinned in the test.
	{"secret assignment", secretRe, func(m []string) bool {
		g := func(name string) string { return m[secretRe.SubexpIndex(name)] }
		if strings.TrimSpace(g("sep")) == "" && g("flag") == "" {
			return false // a word, not an assignment or a flag
		}
		if g("dq") != "" || g("sq") != "" {
			return true
		}
		v := strings.TrimRight(g("bare"), `,;)`)
		key := strings.ToLower(g("key"))
		if strings.Contains(key, "pass") || strings.Contains(key, "pwd") || strings.Contains(key, "secret") {
			return len(v) >= 4
		}
		return len(v) >= 8 || len(v) >= 6 && strings.ContainsAny(v, "0123456789")
	}},
	{"bearer", regexp.MustCompile(`(?i)bearer\s+\S`), nil},
	{"authorization", regexp.MustCompile(`(?i)authorization["']?\s*:\s*\S`), nil},
	// The user may be empty: redis://:pass@host.
	{"url credentials", regexp.MustCompile(`://[^/\s:@]*:[^/\s@]+@`), nil},
	// Token shapes whose prefix alone is too short to list above (a bare
	// `sk-` or `hf_` sits inside ordinary words), pinned by the length and
	// alphabet the vendor uses instead (security review 2026-09-30, finding
	// 3). No word boundary in front: a token after a literal `\n` or an
	// underscore, as a line that failed to decode carries it, would sit
	// inside a word and be missed (security check of the fix, finding 2);
	// a word that happens to end in `sk-` before 32 alphanumerics is the
	// accepted over-redaction.
	{"jwt", regexp.MustCompile(`eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.`), nil},
	// Stripe live and test keys: the prefix plus sixteen alphanumerics, so a
	// name such as `task_test_helper` stays.
	{"stripe key", regexp.MustCompile(`[sr]k_(live|test)_[A-Za-z0-9]{16,}`), nil},
	{"openai key", regexp.MustCompile(`sk-[A-Za-z0-9]{32,}`), nil},
	{"npm token", regexp.MustCompile(`npm_[A-Za-z0-9]{36}`), nil},
	{"hugging face token", regexp.MustCompile(`hf_[A-Za-z0-9]{30,}`), nil},
	// A token as the whole userinfo, with an empty or no password: sixteen
	// characters or more of anything up to the `@`, so a short username
	// (`ssh://git@`) stays while an escaped or padded token goes. A long
	// username goes too, and a pathless URL followed by an unrelated `@` on
	// the same line takes the line with it: over-redaction in the safe
	// direction, accepted like `ASIAN` and pinned in the test. One rule per
	// kind, not one per instance: an instance list is always one shape short
	// (BUG-110).
	{"url token", regexp.MustCompile(`://[^/\s:@]{16,}:?@`), nil},
	// The families the v1.14.0 gate named as not covered, one rule per kind
	// (a JWT's payload may be as short as `e30`, an empty object).
	{"jwt short", regexp.MustCompile(`eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]*\.[A-Za-z0-9_-]{8,}`), nil},
	{"google refresh token", regexp.MustCompile(`1//0[A-Za-z0-9_-]{20,}`), nil},
	{"discord webhook", regexp.MustCompile(`(?i)discord(app)?\.com[:.]?[0-9]*/api(/v[0-9]+)?/webhooks/`), nil},
	{"teams webhook", regexp.MustCompile(`(?i)webhook\.office\.com/|/webhookb2/|[?&]sig=[A-Za-z0-9%_-]{16,}`), nil},
	{"sendgrid key", regexp.MustCompile(`SG\.[A-Za-z0-9_-]{16,}\.[A-Za-z0-9_-]{16,}`), nil},
	{"azure account key", regexp.MustCompile(`(?i)(accountkey|sharedaccesskey|storage_key|access_key)=\S`), nil},
	// The -b/--cookie flag needs a name=value after it: `git checkout -b x`
	// and `-bench . -b 3` are not cookies (audit 2026-09-30, IN1).
	{"cookie header", regexp.MustCompile(`(?i)(set-)?cookie["']?\s*:\s*\S|(^|\s)(-b|--cookie)[ =]["']?\S+=\S|document\.cookie\s*=`), nil},
	{"vault token", regexp.MustCompile(`hv[sbr]\.[A-Za-z0-9_-]{20,}`), nil},
	// Two kinds the 2026-10-27 audit named (SD41), each shape from the
	// vendor's own documentation, the lengths from its shortest example:
	// - Docker personal and organization access tokens, `dckr_pat_` and
	//   `dckr_oat_` (the Docker Hub API spec, docs.docker.com/reference/api/
	//   hub/latest.yaml, gives `dckr_pat_124509ugsdjga93`);
	// - HCP Terraform and Terraform Enterprise API tokens, an ID, `.atlasv1.`
	//   and the secret (the CLI configuration page, developer.hashicorp.com/
	//   terraform/cli/config/config-file, gives `xxxxxx.atlasv1.zzzzzzzzzzzzz`).
	{"docker access token", regexp.MustCompile(`dckr_(?:pat|oat)_[A-Za-z0-9_-]{8,}`), nil},
	{"terraform api token", regexp.MustCompile(`[A-Za-z0-9]{6,}\.atlasv1\.[A-Za-z0-9_=-]{13,}`), nil},
	// Three kinds the project review of 2026-10-09 named (security row 9,
	// each approved by the operator), one rule per kind and nothing anchored
	// in front (a quote, a backtick, a path or a literal `\n` may precede
	// the command; the fix's security review):
	// - a credential on a command line: `user:pass` (or smbclient's
	//   `user%pass`) after -u/--user/--username, the flag after any
	//   character but a letter, a digit, `_` or `-` (so `-u` inside
	//   `run-usage` or `web-ui` is not one), and whitespace, `=`, an
	//   argv-array `", "` or an escaped quote before the value: the rule is
	//   the kind, after two rounds that added a character each (the gate
	//   reviews of v2.6.2..66f3412 and its fixes; the operator's choice,
	//   2026-10-12), so a shape it misses is a known limit, not a patch (the
	//   release gate then took three more by the operator's choice: an
	//   escaped `\n`, `\r` or `\t` written out before the flag, which the
	//   AGENTS.md needle rule names, and a quote around the user or the
	//   password); the user empty, numeric or attached
	//   (`-uadmin:x`, `-u :token`) but letter-led before a `%` (so
	//   `date -u +%FT%TZ` stays), so `docker run -u 1000:1000` and
	//   `sudo -u postgres:x` go too, pinned; a password flag (`-p<pw>`,
	//   `-p pw`, `-a pw`) after
	//   a client that takes one (sshpass, the MySQL clients, redis-cli,
	//   docker/podman login), so `mysql -P3306` goes too, pinned, while
	//   `mysql -p` alone, `mkdir -p` and `cp -pr` stay; `htpasswd -b`, whose
	//   password is positional; and, by the operator's choice at the release
	//   gate of v2.6.2..13c5bde, mongosh's and sqlcmd's `-p`/`-P`, an LDAP
	//   tool's lower-case `-w` (`-W` prompts), OpenSSL's `pass:`, quoted or not, after
	//   `-passin`/`-passout`/`-password` (`env:` and `file:` stay), keytool's
	//   `-storepass`/`-keypass` with a value (`:env` stays) and lftp's
	//   `-u user,password` (`net use … /user:` is a known limit);
	// - an auth key holding base64: Docker's `"auth":` in any quoting or
	//   YAML, `.npmrc`'s `_auth=`, Kubernetes' `.dockerconfigjson:`, from
	//   sixteen base64 characters so `auth: required` stays, and the strict
	//   double-quoted JSON form from eight (`admin:admin` is fifteen plus its
	//   padding; the fix's review, round 2);
	// - a `credential(s)` key whose value is sixteen or more token characters
	//   and does not start with `/`, `~` or `.`, so `credentials: none`,
	//   `Credentials: see gcloud-setup` and
	//   `GOOGLE_APPLICATION_CREDENTIALS=/path` stay.
	{"command-line credential", regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9_-]|\\[nrt])(?:-u|--user|--username)(?:\s+|=|["']\s*,\s*["'])?\\?["']?(?:[A-Za-z0-9_.][^\s:%"']*)?["']?[:%]["']?[^\s"']+|(?:sshpass|mysql\S*|mariadb\S*|redis-cli|docker\s+login|podman\s+login|mongo(?:sh)?|sqlcmd)\b[^\n]*?\s-[pa]\s*\S+|htpasswd\s+-[a-z]*b|ldap\w+\b[^\n]*?\s(?-i:-w)\s*\S+|-pass(?:in|out|word)?\s+["']?pass:\S+|-(?:src|dest)?(?:store|key)pass\s+\S+|lftp\b[^\n]*?\s-u\s*[^\s,]+,\S+`), nil},
	{"auth key", regexp.MustCompile(`(?i)"auth"\s*:\s*"[A-Za-z0-9+/=]{8,}"|(?:["']?_?auth["']?|\.?dockerconfigjson["']?)\s*[:=]\s*["']?[A-Za-z0-9+/]{16,}={0,2}`), nil},
	{"credentials key", regexp.MustCompile(`(?i)credentials?["']?\s*[:=]\s*["']?[A-Za-z0-9+=_-][A-Za-z0-9+/=_.-]{15,}`), nil},
	// A shape past the rules above is a design question for the operator,
	// never another batch of instances: the list is a safety net (delivery.md,
	// BUG-110).
}

func needleIn(s string) (string, bool) {
	ensureNeedles()
	for _, n := range prefixNeedles {
		if strings.Contains(s, n) {
			return n, true
		}
	}
	for _, k := range keywordNeedles {
		if k.accept == nil {
			if k.re.MatchString(s) {
				return k.pattern, true
			}
			continue
		}
		for _, m := range k.re.FindAllStringSubmatch(s, -1) {
			if k.accept(m) {
				return k.pattern, true
			}
		}
	}
	return "", false
}

// redaction records where content was removed and which needle fired — never
// the text. Line is 1-based.
type redaction struct {
	Line         int
	Pattern      string
	Lines        int
	Unterminated bool
}

// marker is what replaces removed content. In a JSONL file it is a JSON
// object, so every line of the redacted file still parses on its own.
func marker(text string, jsonl bool) string {
	if !jsonl {
		return text
	}
	b, _ := json.Marshal(map[string]string{"redacted": text})
	return string(b)
}

// redactLines redacts line-oriented content: a key block goes whole, and any
// other line carrying a needle goes whole. Nothing on a matching line
// survives, so no structure inside it — JSON values, keys, arrays — can carry
// a secret past the rule (D13, which retired the per-value JSON walk).
func redactLines(data []byte, jsonl bool) ([]byte, []redaction) {
	ensureNeedles()
	lines := strings.SplitAfter(string(data), "\n")
	var out strings.Builder
	var recs []redaction
	for i := 0; i < len(lines); i++ {
		raw := lines[i]
		if raw == "" {
			continue
		}
		body := strings.TrimSuffix(raw, "\n")
		var dec string
		if jsonl && !blockOpen.MatchString(body) {
			dec = decoded(body)
		}
		if blockOpen.MatchString(body) || (jsonl && blockOpen.MatchString(dec)) {
			end, closed := blockEnd(lines, i, jsonl)
			rec := redaction{Line: i + 1, Pattern: beginNeedle, Lines: end - i + 1, Unterminated: !closed}
			last := lines[end]
			out.WriteString(marker(fmt.Sprintf("[redacted by fyr: %d lines]", rec.Lines), jsonl))
			out.WriteString(last[len(strings.TrimSuffix(last, "\n")):])
			recs = append(recs, rec)
			i = end
			continue
		}
		n, ok := needleIn(body)
		if !ok && jsonl {
			n, ok = needleIn(dec)
		}
		if ok {
			out.WriteString(marker(redactedValue, jsonl) + raw[len(body):])
			recs = append(recs, redaction{Line: i + 1, Pattern: n})
			continue
		}
		out.WriteString(raw)
	}
	return []byte(out.String()), recs
}

// Redact removes from text every line that carries a needle and every key
// block, for `fyr dream`, which sends project text to a CLI; clean text
// comes back byte-identical.
func Redact(text string) string {
	out, _ := redactLines([]byte(text), false)
	return string(out)
}

// RedactJSONL is Redact for JSON Lines: a needle hidden behind a JSON escape
// (`\u003d`, `\"`) is matched on the decoded line, as `fyr dream`
// reads a feedback record and a transcript (security review of the v2.0.0 gate).
func RedactJSONL(text string) string {
	out, _ := redactLines([]byte(text), true)
	return string(out)
}
