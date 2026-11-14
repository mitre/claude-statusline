//go:build !linux

package account

import "io/fs"

// linuxPlaceholder is Claude Code's Q_, which applies on Linux only.
func linuxPlaceholder(fs.FileInfo) bool { return false }
