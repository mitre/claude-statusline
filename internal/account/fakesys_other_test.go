//go:build !linux

package account

// Sys carries no link count off Linux, where the placeholder check never
// applies.
func (i fakeInfo) Sys() any { return nil }
