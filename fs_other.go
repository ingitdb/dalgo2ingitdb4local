//go:build !linux

package dalgo2fsingitdb

// ExactNames reports false: there is no cheap way here to know that a folder
// holds names exactly. APFS treats NFC and NFD names as one even on a
// case-sensitive volume, and Windows drops trailing dots even in a
// case-sensitive folder, so the folder is listed.
func (osFS) ExactNames(string) bool { return false }
