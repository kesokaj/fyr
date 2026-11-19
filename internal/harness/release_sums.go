package harness

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// sumsVersionMarker is the line `/preflight` writes at the top of
// SHA256SUMS, naming the release those hashes belong to, so one release's
// checksums are never taken for another's (security review 2026-09-17).
// `sha256sum -c` ignores a line starting with '#', so the file stays readable
// by it. One marker, `# fyr vX.Y.Z`, since v3.0.0: no release this binary
// takes was published under the name before the rename (plan fyr D5a). It is
// matched on any line, and a file whose markers name two releases is refused
// (parseSums, IN28).
//
// The checksums are not signed (ADR-053): they say a download is whole and is
// the file the release lists, not who made the release. That rests on the
// operator's GitHub credential and HTTPS, as a checkout place's pull does.
var sumsVersionMarker = regexp.MustCompile(`(?m)^#\s*fyr\s+(v[0-9]+\.[0-9]+\.[0-9]+)\s*$`)

// maxSumsBytes bounds what a checksum file can make this process read into
// memory. Variable so a test can lower it.
var maxSumsBytes int64 = 1 << 20

// parseSums validates and returns the single release version marker in sums.
// what names the subject in messages.
func parseSums(sums []byte, what string) (string, error) {
	markers := sumsVersionMarker.FindAllSubmatch(sums, -1)
	if markers == nil {
		return "", fmt.Errorf("the SHA256SUMS of %s names no release version; it cannot be told apart from another release's", what)
	}
	// The marker may stand on any line; a second one naming another release
	// is refused, never settled by which comes first (IN28).
	for _, m := range markers[1:] {
		if !bytes.Equal(m[1], markers[0][1]) {
			return "", fmt.Errorf("the SHA256SUMS of %s names two releases, %s and %s; it belongs to one", what, markers[0][1], m[1])
		}
	}
	return string(markers[0][1]), nil
}

// readSums reads sumsDir's SHA256SUMS and returns it and the release it
// names. what names the subject in messages.
func readSums(sumsDir, what string) (sums []byte, tag string, err error) {
	sumsPath := filepath.Join(sumsDir, "SHA256SUMS")
	if _, err := os.Stat(sumsPath); err != nil {
		return nil, "", fmt.Errorf("%s has no SHA256SUMS", what)
	}
	sums, err = readCapped(sumsPath, maxSumsBytes)
	if err != nil {
		return nil, "", fmt.Errorf("%s: SHA256SUMS is not usable: %w", what, err)
	}
	tag, err = parseSums(sums, what)
	if err != nil {
		return nil, "", err
	}
	return sums, tag, nil
}

// checksumFor reads one file's hash out of a sha256sum-format listing, in
// which a binary-mode line marks the name with a leading '*'. The hash must
// look like one, and two lines naming the same file are refused rather than
// resolved by order: an ambiguity in it is a reason to stop, not to guess.
func checksumFor(sums, name string) (string, error) {
	var found string
	for _, line := range strings.Split(sums, "\n") {
		hash, file, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		if strings.TrimPrefix(strings.TrimSpace(file), "*") != name {
			continue
		}
		hash = strings.ToLower(hash)
		if len(hash) != 64 || strings.Trim(hash, "0123456789abcdef") != "" {
			return "", fmt.Errorf("the line naming %s does not carry a SHA-256", name)
		}
		if found != "" && found != hash {
			return "", fmt.Errorf("two different hashes are given for %s", name)
		}
		found = hash
	}
	if found == "" {
		return "", fmt.Errorf("no line names %s", name)
	}
	return found, nil
}

// readCapped is the one capped file read: it refuses a file over max bytes
// before reading it (a release asset, a settings file, a checksum file), so no
// caller streams an unbounded file into memory.
func readCapped(path string, max int64) ([]byte, error) {
	f, err := openNonBlock(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	if info.Size() > max {
		return nil, fmt.Errorf("%d bytes is larger than the %d this reads", info.Size(), max)
	}
	// One byte past the cap: a stream whose Stat size says nothing (a FIFO, a
	// special file) fails closed instead of being cut short.
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("more than the %d bytes this reads", max)
	}
	return data, nil
}

// ErrZipNoSums: no SHA256SUMS beside a zip — a zip of a branch, which no
// release lists, or a release zip downloaded alone. The caller installs it
// marked UNVERIFIED (R9, ADR-041, ADR-053).
var ErrZipNoSums = errors.New("no SHA256SUMS beside the zip")

// VerifyZipSums checks a release zip against the SHA256SUMS beside it: the
// zip's own line, by its file name, against sum. It returns the release the
// file names. No SHA256SUMS is ErrZipNoSums; anything else that fails is a
// refusal (plan D7).
func VerifyZipSums(zipPath, sum string) (string, error) {
	dir := filepath.Dir(zipPath)
	// Only "does not exist" means there are none. Any other error (a folder
	// that cannot be read, say) is a refusal: an unreadable checksum file is
	// not the absence of one.
	if _, err := os.Stat(filepath.Join(dir, "SHA256SUMS")); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", ErrZipNoSums
		}
		return "", fmt.Errorf("cannot check for checksums beside %s: %w", filepath.Base(zipPath), err)
	}
	what := filepath.Base(zipPath)
	sums, tag, err := readSums(dir, what)
	if err != nil {
		return "", err
	}
	want, err := checksumFor(string(sums), what)
	if err != nil {
		return "", fmt.Errorf("the SHA256SUMS beside it are release %s's and do not list %s: %w — release %s publishes %s: if that is the release you want, download that file from its assets (GitHub's \"Source code\" archive is a different file); otherwise put this zip in a folder with its own release's SHA256SUMS", tag, what, err, tag, zipNamesIn(sums))
	}
	if want != sum {
		return "", fmt.Errorf("checksum mismatch for %s against the SHA256SUMS of %s: the zip is not the one that release published — download %s from the release's assets again (GitHub's \"Source code\" archive is a different file)", what, tag, zipNamesIn(sums))
	}
	return tag, nil
}

// zipNamesIn lists the .zip files a SHA256SUMS names, for a refusal that has
// to say which file the release actually published.
func zipNamesIn(sums []byte) string {
	var names []string
	for _, line := range strings.Split(string(sums), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && strings.HasSuffix(f[1], ".zip") {
			names = append(names, strings.TrimPrefix(f[1], "*"))
		}
	}
	if len(names) == 0 {
		return "no zip"
	}
	return strings.Join(names, ", ")
}
