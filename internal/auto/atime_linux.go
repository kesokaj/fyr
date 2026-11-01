package auto

import (
	"os"
	"syscall"
	"time"
)

func accessed(info os.FileInfo) (time.Time, int, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return time.Time{}, 0, false
	}
	return time.Unix(int64(st.Atim.Sec), int64(st.Atim.Nsec)), int(st.Uid), true
}
