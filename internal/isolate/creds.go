package isolate

import "github.com/kesokaj/fyr/internal/isolate/creds"

type credFinding = creds.Finding

func scanCreds(dir string) map[string]credFinding {
	return creds.ScanWith(dir, creds.Options{OpenRegular: OpenRegular})
}

func newCreds(baseline, after map[string]credFinding) []string {
	return creds.New(baseline, after)
}
