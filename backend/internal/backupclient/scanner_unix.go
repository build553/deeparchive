//go:build unix

package backupclient

import "io/fs"

func fileMode(fi fs.FileInfo) uint32 {
	return uint32(fi.Mode().Perm())
}
