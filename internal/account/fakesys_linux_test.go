//go:build linux

package account

import "syscall"

// Sys reports the fake file's link count the way the Linux stat does.
func (i fakeInfo) Sys() any {
	st := &syscall.Stat_t{Nlink: 2}
	if i.ff.soleLink {
		st.Nlink = 1
	}
	return st
}
