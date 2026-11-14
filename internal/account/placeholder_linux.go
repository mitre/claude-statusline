//go:build linux

package account

import (
	"io/fs"
	"syscall"
)

// linuxPlaceholder is Claude Code's Q_: on Linux (WSL included), an empty
// read-only record with a single link is a placeholder, not a live record.
func linuxPlaceholder(fi fs.FileInfo) bool {
	if !fi.Mode().IsRegular() || fi.Size() != 0 || fi.Mode().Perm()&0o222 != 0 {
		return false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && st.Nlink == 1
}
