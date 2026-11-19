package search

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/kesokaj/fyr/internal/trail"
)

// ErrNotRegular is a source that is not a regular file: a FIFO, a device, a
// directory or a symlink. It is refused before a byte is read, so a FIFO
// planted at a conversation path cannot hang ingest, eval or the timer's
// dream (SD48 of the 2026-10-27 audit).
var ErrNotRegular = errors.New("not a regular file")

// copyFileImpl copies the regular file from to a new file to. The source is
// opened without blocking and without following a final symlink
// (trail.OpenNonBlocking), then checked on the open descriptor, so nothing
// swapped in after a check is read.
func copyFileImpl(from, to string) error {
	in, err := trail.OpenNonBlocking(from)
	if err != nil {
		if fi, lerr := os.Lstat(from); lerr == nil && !fi.Mode().IsRegular() {
			return fmt.Errorf("%s: %w", from, ErrNotRegular)
		}
		return err
	}
	defer in.Close()
	fi, err := in.Stat()
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%s: %w", from, ErrNotRegular)
	}
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// roDSN opens path read-only; the path is escaped as dsn does.
func roDSN(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path),
		RawQuery: fmt.Sprintf("mode=ro&_pragma=busy_timeout(%d)", busyTimeoutMS)}
	return u.String()
}

// copyDB copies name and its -wal and -shm files from dir into tmp and
// returns the copy's path; a missing main file is os.ErrNotExist.
func copyDB(dir, name, tmp string) (string, error) {
	for _, suffix := range []string{"", "-wal", "-shm"} {
		err := copyFile(filepath.Join(dir, name+suffix), filepath.Join(tmp, name+suffix))
		if err != nil && (suffix == "" || !errors.Is(err, os.ErrNotExist)) {
			return "", err
		}
	}
	return filepath.Join(tmp, name), nil
}

// copyRetryWait is the pause before a second copy of a database whose first
// copy was torn; zero in tests.
var copyRetryWait = time.Second

// copyFile is a variable so a test can tear the first copy.
var copyFile = copyFileImpl

// openCopy copies name from dir into tmp, opens the copy read-only and
// reads once, since sql.Open is lazy. A copy taken while its writer was
// mid-checkpoint opens as NOTADB or CORRUPT: the three copies are removed
// and, after copyRetryWait, copied and opened once more; a second failure
// is returned. The live database is never opened (audit 2026-09-30, SD7).
func openCopy(dir, name, tmp string) (*sql.DB, error) {
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt == 1 {
			for _, suffix := range []string{"", "-wal", "-shm"} {
				os.Remove(filepath.Join(tmp, name+suffix))
			}
			time.Sleep(copyRetryWait)
		}
		var cp string
		if cp, err = copyDB(dir, name, tmp); err != nil {
			return nil, err
		}
		var db *sql.DB
		if db, err = sql.Open("sqlite", roDSN(cp)); err != nil {
			return nil, err
		}
		var n int
		if err = db.QueryRow(`SELECT count(*) FROM sqlite_master`).Scan(&n); err == nil {
			return db, nil
		}
		db.Close()
		if !isUnreadable(err) {
			return nil, err
		}
	}
	return nil, err
}
