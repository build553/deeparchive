package backupclient

import "fmt"

type MissingChunkError struct{ Hash string }

func (e *MissingChunkError) Error() string {
	return fmt.Sprintf("missing chunk %s needed to reassemble file", e.Hash)
}

type ChunkLengthMismatchError struct {
	Hash string
	Got  int
	Want uint64
}

func (e *ChunkLengthMismatchError) Error() string {
	return fmt.Sprintf("chunk %s has length %d, expected %d", e.Hash, e.Got, e.Want)
}

type UnsupportedManifestFormatError struct{ Version uint32 }

func (e *UnsupportedManifestFormatError) Error() string {
	return fmt.Sprintf("unsupported manifest format version %d", e.Version)
}
