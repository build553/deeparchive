//go:build windows

package backupclient

import "io/fs"

// Windows has no Unix permission bits; a fixed placeholder is restored
// as owner-read/write. Real ACL preservation is not implemented —
// documented limitation, not silently wrong.
func fileMode(_ fs.FileInfo) uint32 {
	return 0o644
}
