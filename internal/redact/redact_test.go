package redact

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

const pem = "-----BEGIN RSA PRIVATE KEY-----\nMIIEpAIBAAKCAQEAsecretbody\nmoresecretbody\n-----END RSA PRIVATE KEY-----"

func TestRedactJSONL(t *testing.T) {
	pemJSON, _ := json.Marshal("here is my key " + pem)
	quote := `{"n":12345678901234567890,"user_quote":` + string(pemJSON) + `}`
	akia := `{"project":"/p","user_quote":"use AKIAIOSFODNN7EXAMPLE for the demo","tags":["ok"]}`
	malformed := `{"user_quote": "token=abc123 broken`
	clean := `{"user_quote":"<b>no secrets</b> & more","n":1.50}`
	in := strings.Join([]string{quote, akia, malformed, clean, "not json at all", ""}, "\n")

	out, recs := redactLines([]byte(in), true)
	lines := strings.Split(string(out), "\n")
	if len(lines) != 6 || lines[5] != "" {
		t.Fatalf("line structure changed: %q", out)
	}
	for _, leak := range []string{"secretbody", "IOSFODNN7EXAMPLE", "abc123"} {
		if strings.Contains(string(out), leak) {
			t.Errorf("output still carries %q:\n%s", leak, out)
		}
	}
	// A matching line goes whole, and in a JSONL file its marker is itself a
	// JSON object, so every redacted line parses — the malformed one included.
	for i, want := range []string{marker("[redacted by fyr: 1 lines]", true), marker(redactedValue, true), marker(redactedValue, true)} {
		if lines[i] != want || !json.Valid([]byte(lines[i])) {
			t.Errorf("line %d = %q, want %q", i+1, lines[i], want)
		}
	}
	if lines[3] != clean || lines[4] != "not json at all" {
		t.Errorf("clean lines not byte-identical: %q %q", lines[3], lines[4])
	}
	want := []redaction{
		{Line: 1, Pattern: "-----BEGIN", Lines: 1},
		{Line: 2, Pattern: "AKIA"},
		{Line: 3, Pattern: "secret assignment"},
	}
	if len(recs) != len(want) {
		t.Fatalf("redactions = %+v, want %+v", recs, want)
	}
	for i := range want {
		if recs[i] != want[i] {
			t.Errorf("redaction %d = %+v, want %+v", i, recs[i], want[i])
		}
	}
}

// TestRedactShapes: the common credential forms, in any case, are
// caught inside a JSONL value and on a text line; ordinary prose that only
// resembles them is not (security review 2026-09-17, finding 1).
// shapeSecrets is every secret shape the redactor removes, one per rule; `Redact`
// is held to the same list.
var shapeSecrets = []string{
	"export GITHUB_TOKEN=FAKEVALUE", "DB_PASSWORD=FAKEVALUE", "password: FAKEVALUE",
	`config was {"password": "FAKEVALUE"}`, "Authorization: Bearer FAKEVALUE",
	"github_pat_FAKEVALUE", "ghp_FAKEVALUE", "sk-ant-FAKEVALUE", "aws_secret_access_key = FAKEVALUE",
	"ASIAFAKEVALUE", "postgres://app:FAKEVALUE@db/x", "token = FAKEVALUE", "xoxb-FAKEVALUE",
	"GOCSPX-FAKEVALUE", "api_key=FAKEVALUE", "API-KEY: FAKEVALUE",
	// Round 2, finding 8: an empty URL user, and a non-Bearer scheme.
	"redis://:FAKEVALUE@localhost:6379/0", "Authorization: Basic FAKEVALUE",
	// Round 3, finding 3: the header name quoted, as code and JSON write it.
	`headers: {"Authorization": "Basic FAKEVALUE"}`, `requests.get(u, headers={'Authorization': 'Basic FAKEVALUE'})`,
	// Project review 2026-09-30, security finding 3: token shapes with no
	// keyword on the line — Stripe, PyPI (every token opens with the same
	// macaroon location), a Slack webhook, a JWT, an OpenAI key, an npm
	// token, a Hugging Face token, and a URL whose userinfo is a bare
	// token with no password.
	"sk_live_FAKEVALUEFAKEVALUE", "rk_live_FAKEVALUEFAKEVALUE", "whsec_FAKEVALUE",
	"pypi-AgEIcHlwaS5vcmcFAKEVALUE",
	"https://hooks.slack.com/services/T0/B0/FAKEVALUE",
	"eyJhbGciOiJIUzI1NiJ9.eyJFAKEVALUEx.FAKEVALUE",
	"sk-FAKEVALUEFAKEVALUEFAKEVALUEFAKEVALUE",
	"npm_FAKEVALUEFAKEVALUEFAKEVALUEFAKEVALUE",
	"hf_FAKEVALUEFAKEVALUEFAKEVALUEFAKEVALUE",
	"https://FAKEVALUEFAKEVALUEFAKEVALUE@github.com/o/r.git",
	// Security check of the fix, finding 2: the same tokens after a
	// literal `\n` (a JSONL line that failed to decode keeps its
	// escapes) and after an underscore, and a test.pypi.org token.
	`log:\neyJhbGciOiJIUzI1NiJ9.eyJFAKEVALUEx.FAKEVALUE`, `key_sk-FAKEVALUEFAKEVALUEFAKEVALUEFAKEVALUE`,
	`x\tnpm_FAKEVALUEFAKEVALUEFAKEVALUEFAKEVALUE`, `_hf_FAKEVALUEFAKEVALUEFAKEVALUEFAKEVALUE`,
	"pypi-AgENdGVzdC5weXBpLm9yZwFAKEVALUE",
	// Round 2 of the check: test-mode keys, service and admin keys, a
	// workflow webhook, a percent-escaped userinfo token.
	"sk_test_FAKEVALUEFAKEVALUE", "rk_test_FAKEVALUEFAKEVALUE", "sk-svcacct-FAKEVALUE", "sk-admin-FAKEVALUE",
	// v1.13.1 gate: the GitHub refresh token and GitLab deploy, runner
	// and pipeline-trigger tokens the guide's vendor list implied.
	"ghr_FAKEVALUE", "gldt-FAKEVALUE", "glrt-FAKEVALUE", "glptt-FAKEVALUE",
	// The families the v1.14.0 gate's security review named as not
	// covered, one rule per kind: a Google refresh token, Discord and
	// Teams webhooks, SendGrid, an Azure storage connection string, a
	// cookie header, Vault tokens, an age key, DigitalOcean tokens, a
	// JWT with a short payload.
	"1//0FAKEVALUEFAKEVALUE-FAKEVALUE", "https://discord.com/api/webhooks/1/FAKEVALUE",
	// Project review 2026-10-09, security row 9: a credential on a command
	// line, Docker's auth key, and a credentials key.
	"curl -u admin:FAKEVALUE https://h/", "curl --user admin:FAKEVALUE https://h/", "mysql -u root -pFAKEVALUE db",
	"sshpass -p FAKEVALUE ssh h", `"auth": "FAKEVALUEFAKEVALUE=="`, "CREDENTIALS=FAKEVALUEFAKEVALUE", "credentials: FAKEVALUE0123456789",
	// The fix's security review: the command after a quote, a backtick or a
	// path; the user attached, empty or numeric; smbclient, redis-cli,
	// htpasswd, docker login; single-quoted, YAML, .npmrc and Kubernetes
	// auth keys; a credentials value with a slash.
	`{"text":"sshpass -p FAKEVALUE ssh h"}`, "run `mysql -pFAKEVALUE`", "/usr/bin/mysql -uroot -pFAKEVALUE",
	"curl -uadmin:FAKEVALUE h", "curl -u :FAKEVALUE h", "curl -u 12345:FAKEVALUE h",
	"smbclient //h/s -U user%FAKEVALUE", "redis-cli -a FAKEVALUE ping", "htpasswd -b .htpasswd user FAKEVALUE",
	"docker login -u user -p FAKEVALUE reg", "podman login -u user -p FAKEVALUE reg",
	`'auth': 'FAKEVALUEFAKEVALUE'`, "auth: FAKEVALUEFAKEVALUE", "_auth=FAKEVALUEFAKEVALUE", ".dockerconfigjson: FAKEVALUEFAKEVALUEFAKE",
	"credentials: abc/defFAKEVALUE0123456789",
	// Round 2: a short Docker auth value (`admin:admin` is sixteen base64
	// characters with its padding), which the first double-quoted form caught.
	`"auth": "RkFLRVZBTFVF="`, `{"auths":{"r":{"auth":"RkFLRVZBTFVF="}}}`,
	"https://discordapp.com/api/webhooks/1/FAKEVALUE", "https://x.webhook.office.com/webhookb2/FAKEVALUE",
	"SG.FAKEVALUEFAKEVALUE.FAKEVALUEFAKEVALUE", "AccountKey=FAKEVALUE", "Cookie: session=FAKEVALUE",
	"Set-Cookie: sid=FAKEVALUE", "hvs.FAKEVALUEFAKEVALUEFAKE", "hvb.FAKEVALUEFAKEVALUEFAKE",
	"AGE-SECRET-KEY-1FAKEVALUE", "dop_v1_FAKEVALUE", "doo_v1_FAKEVALUE", "dor_v1_FAKEVALUE",
	"eyJhbGciOiJIUzI1NiJ9.e30.FAKEVALUE",
	// The seventh pass: a versioned Discord path, a Workflows signature,
	// the other Azure key names, cookies on a curl line and in a script,
	// a JWE with an empty key segment, a post-quantum age key.
	"https://discord.com/api/v10/webhooks/1/FAKEVALUE", "https://x.logic.azure.com/w?api-version=1&sig=FAKEVALUEFAKEVALUE",
	"SharedAccessKey=FAKEVALUE", "AZURE_STORAGE_KEY=FAKEVALUE", "curl -b sid=FAKEVALUE h", "curl --cookie sid=FAKEVALUE h",
	"document.cookie = 'sid=FAKEVALUE'", "eyJhbGciOiJIUzI1NiJ9..FAKEVALUEFAKEVALUE", "AGE-SECRET-KEY-PQ-1FAKEVALUE",
	"https://hooks.slack.com/workflows/T0/A0/FAKEVALUE", "https://FAKEVALUE%2FFAKEVALUE%3DFAKE@host/r",
	// Round 3, one rule per kind: any Slack webhook path in any case, a
	// userinfo token of sixteen characters, a padded one, an sk-None key.
	"https://HOOKS.SLACK.COM/services/T0/B0/FAKEVALUE", "https://hooks.slack.com/triggers/T0/FAKEVALUE",
	"https://FAKEVALUE1234567@host/r", "https://FAKEVALUEFAKEVAL==@host/r", "sk-None-FAKEVALUE",
	// The security check of that rule: a webhook host with a port, a
	// trailing dot or a gov domain, an encoded path, an empty password.
	"https://hooks.slack.com:443/services/T0/B0/FAKEVALUE", "https://hooks.slack.com./services/FAKEVALUE",
	"https://hooks.slack-gov.com/services/FAKEVALUE", "https://hooks.slack.com%2Fservices%2FFAKEVALUE",
	"https://FAKEVALUEFAKEVALUE:@host/r",
	// The gate security review of v2.6.2..66f3412, finding 2: the flag after
	// a quote or a backtick, not only after whitespace.
	`curl "-uadmin:FAKEVALUE" h`, "curl '-uadmin:FAKEVALUE' h", "run `-uadmin:FAKEVALUE` there", `{"text":"-uadmin:FAKEVALUE"}`,
	// Its round 2, the rule made a class (the operator's choice, 2026-10-12):
	// the flag after any character that is not a letter, digit, `_` or `-`,
	// and any whitespace, `=` or argv-array separator before the value.
	"ARGS=-uadmin:FAKEVALUE", "(-uadmin:FAKEVALUE)", "[-uadmin:FAKEVALUE]", "curl,-uadmin:FAKEVALUE", "x;-uadmin:FAKEVALUE", "x|-uadmin:FAKEVALUE",
	`exec(["curl", "-u", "admin:FAKEVALUE"])`, `["curl","-u","admin:FAKEVALUE"]`, "curl -u\tadmin:FAKEVALUE h", "curl -u  admin:FAKEVALUE h",
	`curl --user \"admin:FAKEVALUE\" h`,
	// The release gate of v2.6.2..13c5bde, each chosen by the operator: the
	// flag after an escaped line break or tab written out as text, a password
	// quoted after the colon or `%`, and six more clients' password flags,
	// one rule per kind.
	`curl https://x \n-u admin:FAKEVALUE`, `x\t-uadmin:FAKEVALUE`,
	`curl -u admin:"FAKEVALUE"`, `curl -u "admin":"FAKEVALUE"`, "curl -u admin:'FAKEVALUE'", "curl --user=admin:'FAKEVALUE'", "smbclient -U admin%'FAKEVALUE'",
	"mongosh -u admin -p FAKEVALUE", "sqlcmd -U sa -P FAKEVALUE", "ldapsearch -x -D cn=admin -w FAKEVALUE",
	"openssl pkcs12 -in k.p12 -passin pass:FAKEVALUE", "openssl rsa -passout pass:FAKEVALUE", "keytool -list -storepass FAKEVALUE",
	"keytool -importkeystore -srcstorepass FAKEVALUE", "lftp -u user,FAKEVALUE sftp://h",
	// Its round 2: OpenSSL's pass: inside quotes.
	`openssl pkcs12 -passin "pass:FAKEVALUE"`, "openssl rsa -passin 'pass:FAKEVALUE'",
}

func TestRedactShapes(t *testing.T) {
	for _, sec := range shapeSecrets {
		quoted, _ := json.Marshal(map[string]string{"user_quote": "they said " + sec})
		for name, in := range map[string]struct {
			data  string
			jsonl bool
		}{"jsonl": {string(quoted) + "\n", true}, "text": {"note: " + sec + "\n", false}} {
			out, recs := redactLines([]byte(in.data), in.jsonl)
			if len(recs) != 1 || strings.Contains(string(out), "FAKEVALUE") {
				t.Errorf("%s %q: out=%q recs=%+v", name, sec, out, recs)
			}
		}
	}
	// Accepted over-redaction of the url-token rule, pinned so a change to
	// it is deliberate: a username of sixteen or more characters, and a
	// pathless URL whose line holds an unrelated `@` later on.
	// The teams-webhook rule's `?sig=` also matches an Azure SAS signature,
	// a real secret kind, so a docs URL carrying one goes too: pinned.
	for _, over := range []string{"ssh://first.lastname.long@bitbucket.org/x", `["https://app.example.com","ops@example.com"]`, "https://example.com/report?sig=abcdefghijklmnopqrst",
		// Short secrets the value rule keeps redacting (security review of
		// the fix, 2026-09-30): a quoted value whatever it holds, a password
		// of four characters or more, and the prose-shaped `passwords: rotate`.
		`DB_PASSWORD="correct horse battery staple"`, "password: letmein", `api_key: "AbCdEf"`, "password=correct", "passwords: rotate them yearly",
		// The whole key decides the threshold, not its first word.
		"TOKEN_PASSWORD=letmein", "api_key_password: letmein", "API_KEY_SECRET: letmein",
		// A backticked token that starts with -u and holds a colon reads as
		// an attached user:password (the gate's finding 2 put the backtick
		// in front of the flag): review records quoting near-misses go.
		"see `-ui:8080` above",
		// The release gate's rules take these harmless lines too (its round 2):
		// a mongo container's port flag, sqlcmd's packet size, a JSON key
		// named -u, keytool's flag named in prose, and a later -a on a line
		// that names mongo.
		"docker run --name mongo -p 27017:27017 -d mongo", "sqlcmd -S srv -a 4096", `{"-u":"value"}`, "the -storepass flag", "mongo is down; ls -a /tmp",
		// The pass family as one kind, and the flag form (security review round 3).
		"DB_PASS=letmein", "MYSQL_PWD=letmein", "PASSPHRASE: letmein", "run it with --password letmein now",
		// The bearer rule takes prose too (project review 2026-10-09, security row 8).
		"the bearer of the token",
		// The command-line rule takes a numeric or named uid:gid, a MySQL
		// port flag and a `-p` followed by a word (the fix's reviews).
		"docker run -u 1000:1000 img", "docker run -u node:node img", "mysql -P3306 -h db", "mysql -p prompts for the password",
		"docker exec -u root:root c sh", "chown -u www-data:www-data", "sudo -u postgres:x",
		// The strict quoted auth form takes an eight-letter word (the first
		// version's behaviour, kept so `admin:admin` is caught).
		`"auth": "required"`,
		"credentials: application-default"} {
		if _, recs := redactLines([]byte(over+"\n"), false); len(recs) != 1 {
			t.Errorf("%q: over-redaction is the pinned behaviour, got %+v", over, recs)
		}
	}
	for _, benign := range []string{
		"the task-list grew", "the token expired", "passwords are hard", "max_tokens 5", "ASIAN food",
		// The shapes above, too short or too plain to be a token.
		"npm_config_registry=https://r.example", "ssh://git@github.com/o/r.git", "sk-1234 is a short id",
		"the eyJ prefix marks base64 JSON", "hf_hub is the client library", "a whsec is a webhook secret",
		"https://deploy-bot@host/r", "see slack.com/apps for hooks",
		"task_test_helper ran", "network_test_case 3 passed",
		"the SG. prefix marks SendGrid keys", "see discord.com/channels/1/2", "the age tool encrypts files",
		"cookies are set per host", "1/2 of the users", "hvs is the Vault token prefix",
		"see webhook.office.com for the docs", "the AccountKey field is required",
		"a signature is 7 chars", "the access key is rotated", "discord.com/api/v10/users/1",
		// Ordinary prose the audit of 2026-09-30 (IN1) found redacted: a
		// branch flag, a heading, a field name, a short value.
		"git checkout -b feature/x", "-b is the branch flag", "go test -bench . -b 3",
		"## Secrets: how they reach Cloud Run", "see the token: field in the schema",
		"token: short", "- [ ] tokens: budget the context", "pwd prints the directory", "a passphrase protects the key",
		// Accepted under-redaction of the value rule, pinned so a change to
		// it is deliberate: a seven-letter token with no digit, and a
		// two-letter unquoted word after `secret =`.
		"token=abcdefg", "secret = my passphrase",
		// One benign near-miss per needle that had none (project review
		// 2026-10-09, security row 8): the header rules, the URL rule, and
		// the vendor prefixes one character off (case, a dash for an
		// underscore, the trailing mark missing).
		"token type: bearer", "authorization is required for the admin routes", "redis://host:6379/0 has no password",
		"akia keys start with AKI", "the ghp- prefix", "a glpat_ token", "xoxb tokens", "ya29 tokens carry no dot",
		"GOCSPX keys", "github_pat tokens", "a dop_v2_ shape", "AGE-SECRET keys", "sk-ant keys", "the AIz prefix",
		"gldt tokens", "a pypi-AgEIcHlwaS5vcm prefix cut short", "a private key file",
		// The three kinds of row 9, a near-miss per alternation.
		"curl -u is the user flag", "mysql -p", "mkdir -p build", "cp -pr src dst", "redis-cli ping", "sshpass is a wrapper",
		"docker login prompts for the password", "htpasswd -c file user", "psql -p 5432 -h db",
		`"auth": "basic"`, "auth: required", "_auth is the npmrc key", ".dockerconfigjson is the secret type",
		"credentials: none", "Credentials: see the gcloud-setup skill", "GOOGLE_APPLICATION_CREDENTIALS=/home/me/adc.json", "credentials=~/.config/x",
		// Round 2 of the fix's review: a flag follows whitespace, so `-u`
		// inside a hyphenated word is not one, and `%` needs a letter-led
		// user, so `date -u +%FT%TZ` stays.
		"see run-usage.md:72", "the web-ui:8080 service", "image sh-updater:v1", "date -u +%FT%TZ",
		// The gate's finding 2: a quote, a backtick or a key's colon before
		// the flag, with no user:password after it.
		`the "-u" flag sets the user`, "`-u` or `--user`", `{"flag":"-u"}`, "opts:-u root",
		// Its round 2: each new boundary and separator with no user:password.
		"ARGS=-u root", "(-u) is short", "[-u name]", "a,-u,b", "x;-u root", "x|-u root", `["curl","-u","admin"]`, "-u\tis the user flag",
		"X-User-Id:12345", "--user-agent:Mozilla",
		// The release gate's additions: an escaped break with no
		// user:password after it, and each new client without a password.
		`a literal \n-u is the user flag`, "mongosh --host h", "sqlcmd -S server -d db", "ldapsearch -W -H ldap://h",
		"openssl pkcs12 -passin env:PW", "openssl rsa -passin file:pw.txt", "keytool -storepass:env PW", "lftp -u user sftp://h",
	} {
		// "ASIAN" contains the ASIA prefix: accepted over-redaction, pinned
		// here so a change to it is deliberate.
		_, recs := redactLines([]byte(benign+"\n"), false)
		if want := benign == "ASIAN food"; (len(recs) == 1) != want {
			t.Errorf("%q: redactions %+v, want redacted=%v", benign, recs, want)
		}
	}
}

// TestRedactStructuralGaps: the ways a needle was present, content still
// left, and the run reported success — four from security review 2026-09-17
// finding 4, two from round 2 (a key split across array values, a secret-named
// key beside another hit, and a needle behind a JSON escape, which only the
// decoded text shows) — plus a key that named its own secret in a record
// (finding 2).
// TestRedactFlagAtValueStart: a JSON value that starts with the
// credential flag. The raw line shows a quote before it; the decoded line,
// which fmt prints as `key:value`, a colon, and only the decoded line shows a
// quoted password behind a JSON escape (the gate security review of
// v2.6.2..66f3412, finding 2).
func TestRedactFlagAtValueStart(t *testing.T) {
	for _, line := range []string{`{"text":"-uadmin:FAKEVALUE"}`, `{"text":"-u \"admin:FAKEVALUE\""}`, `{"text":"--user=\"admin:FAKEVALUE\""}`,
		`{"cmd":"curl -u\tadmin:FAKEVALUE"}`, `{"cmd":"ARGS=-uadmin:FAKEVALUE"}`, `{"cmd":"(-uadmin:FAKEVALUE)"}`} {
		out, recs := redactLines([]byte(line+"\n"), true)
		if len(recs) != 1 || strings.Contains(string(out), "FAKEVALUE") {
			t.Errorf("%s: out=%q recs=%+v", line, out, recs)
		}
		if got := RedactJSONL(line + "\n"); strings.Contains(got, "FAKEVALUE") {
			t.Errorf("RedactJSONL kept %s: %q", line, got)
		}
	}
}

func TestRedactStructuralGaps(t *testing.T) {
	cases := []struct {
		name, in string
		jsonl    bool
		leak     string
	}{
		{"duplicate key shadows the secret", `{"user_quote":"AKIAFAKEVALUE","user_quote":"clean"}` + "\n", true, "FAKEVALUE"},
		{"two keys concatenated on one line", "-----BEGIN A KEY-----\nFAKEBODY1\n-----END A KEY----------BEGIN B KEY-----\nFAKEBODY2\n-----END B KEY-----\nafter\n", false, "FAKEBODY"},
		{"dashes rewritten", "–––––BEGIN RSA PRIVATE KEY–––––\nFAKEBODY\n–––––END RSA PRIVATE KEY–––––\nafter\n", false, "FAKEBODY"},
		{"JSONL header line that is valid JSON alone", `"-----BEGIN RSA PRIVATE KEY-----"` + "\nFAKEBODY\n-----END RSA PRIVATE KEY-----\n" + `{"ok":"after"}` + "\n", true, "FAKEBODY"},
		{"key split across array values on one line", `{"tool_output":["-----BEGIN OPENSSH PRIVATE KEY-----","FAKEBODY1","FAKEBODY2","-----END OPENSSH PRIVATE KEY-----"]}` + "\n" + `{"ok":"after"}` + "\n", true, "FAKEBODY"},
		{"needle behind a JSON unicode escape", `{"user_quote":"\u0041KIAFAKEVALUE"}` + "\n" + `{"ok":"after"}` + "\n", true, "FAKEVALUE"},
		// Round 3: a header line that only mentions a footer (finding 2b); a
		// number beyond float64, and two records on one line, beside a needle
		// only the decoded text shows (finding 4).
		{"header line that mentions a footer", "pasted -----BEGIN OPENSSH PRIVATE KEY----- up to -----END below\nFAKEBODY1\nFAKEBODY2\n-----END OPENSSH PRIVATE KEY-----\nafter\n", false, "FAKEBODY"},
		{"number beyond float64 beside an escaped needle", `{"n":1e400,"q":"\u0041KIAFAKEVALUE"}` + "\n" + `{"ok":"after"}` + "\n", true, "FAKEVALUE"},
		{"two records on one line", `{"a":1}{"q":"\u0041KIAFAKEVALUE"}` + "\n" + `{"ok":"after"}` + "\n", true, "FAKEVALUE"},
		{"secret-named key beside another hit", `{"user_quote":"use ghp_FAKEPREFIX","config":{"api_key":"FAKEVALUE"}}` + "\n" + `{"ok":"after"}` + "\n", true, "FAKEVALUE"},
	}
	for _, c := range cases {
		out, recs := redactLines([]byte(c.in), c.jsonl)
		if recs == nil || strings.Contains(string(out), c.leak) {
			t.Errorf("%s: out=%q recs=%+v", c.name, out, recs)
		}
		if strings.Contains(c.in, "after") && !strings.Contains(string(out), "after") {
			t.Errorf("%s: redacted past the block: %q", c.name, out)
		}
	}
	_, recs := redactLines([]byte(`{"tool_output":{"https://api.example.test/v1?token=FAKEVALUE":"404"}}`), true)
	if len(recs) != 1 || strings.Contains(fmt.Sprintf("%+v", recs), "FAKEVALUE") {
		t.Errorf("secret in a key reached the record: %+v", recs)
	}
}

func TestRedactCleanIsByteIdentical(t *testing.T) {
	jsonl := "{\"a\":\"x\",  \"b\":[1,2]}\n\n{\"c\":null}"
	md := "# Proposal\n\nNothing to hide.\n"
	for name, tc := range map[string]struct {
		data  string
		jsonl bool
	}{
		"feedback.jsonl": {jsonl, true},
		"proposals/x.md": {md, false},
	} {
		out, recs := redactLines([]byte(tc.data), tc.jsonl)
		if recs != nil || !bytes.Equal(out, []byte(tc.data)) {
			t.Errorf("%s: out=%q recs=%v, want the input unchanged", name, out, recs)
		}
	}
}

func TestRedactLines(t *testing.T) {
	in := "# Proposal\n\nbefore\n" + pem + "\nafter password=hunter2\nlast\n"
	out, recs := redactLines([]byte(in), false)
	want := "# Proposal\n\nbefore\n[redacted by fyr: 4 lines]\n" + redactedValue + "\nlast\n"
	if string(out) != want {
		t.Errorf("got:\n%s\nwant:\n%s", out, want)
	}
	if len(recs) != 2 || recs[0].Line != 4 || recs[0].Lines != 4 || recs[0].Unterminated || recs[1].Pattern != "secret assignment" || recs[1].Line != 8 {
		t.Errorf("redactions = %+v", recs)
	}

	unterminated := "keep\n-----BEGIN OPENSSH PRIVATE KEY-----\nbody\nbody"
	out, recs = redactLines([]byte(unterminated), false)
	if string(out) != "keep\n[redacted by fyr: 3 lines]" || len(recs) != 1 || !recs[0].Unterminated || recs[0].Lines != 3 {
		t.Errorf("unterminated: out=%q recs=%+v", out, recs)
	}
}

// A needle in a KEY takes its line like any other hit.
func TestRedactKeyHit(t *testing.T) {
	out, recs := redactLines([]byte(`{"token=abc":"v"}`), true)
	if string(out) != marker(redactedValue, true) || len(recs) != 1 {
		t.Errorf("key hit: out=%s recs=%+v", out, recs)
	}
}

// TestRedactText: the exported Redact (fyr dream) removes every shape
// in shapeSecrets, and leaves clean text byte-identical.
func TestRedactText(t *testing.T) {
	for _, sec := range shapeSecrets {
		in := "before\nthe user pasted " + sec + " here\nafter\n"
		got := Redact(in)
		if strings.Contains(got, "FAKEVALUE") {
			t.Errorf("Redact kept %q:\n%s", sec, got)
		}
		if !strings.Contains(got, "before\n") || !strings.Contains(got, "after\n") {
			t.Errorf("Redact dropped a clean line around %q:\n%s", sec, got)
		}
	}
	clean := "no secret here\nsecond line\n"
	if got := Redact(clean); got != clean {
		t.Errorf("clean text changed: %q", got)
	}
}
