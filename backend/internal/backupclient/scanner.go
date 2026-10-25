package backupclient

// Filesystem scanning: walks a root directory and returns the files to
// back up, honoring exclude patterns. Symlinks are never followed —
// deliberately: following them risks infinite loops on cyclic symlinks
// and can silently pull files from outside the intended backup root.

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type ScannedFile struct {
	AbsolutePath string
	// Relative to the scan root, forward-slash-separated regardless of
	// platform, so manifests are portable across Windows/macOS/Linux.
	RelativePath string
	Size         uint64
	Mode         uint32
	Modified     string // RFC 3339
}

// Scan walks root, returning every regular file not matched by
// excludePatterns (gitignore-flavored: a pattern ending in "/" excludes
// a directory by name anywhere in the tree — e.g. "node_modules/" — a
// pattern with no "/" is matched against each file's base name — e.g.
// "*.tmp" — and anything else is matched against the file's full
// relative path). Directories, symlinks, and anything else that isn't a
// regular file are skipped. Results are sorted by relative path for
// deterministic manifests (two scans of an unchanged tree must agree).
func Scan(root string, excludePatterns []string) ([]ScannedFile, error) {
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("backup root %s does not exist or is not a directory", root)
	}

	var dirExcludes, baseExcludes, pathExcludes []string
	for _, p := range excludePatterns {
		switch {
		case strings.HasSuffix(p, "/"):
			dirExcludes = append(dirExcludes, strings.TrimSuffix(p, "/"))
		case strings.Contains(p, "/"):
			pathExcludes = append(pathExcludes, p)
		default:
			baseExcludes = append(baseExcludes, p)
		}
	}

	var files []ScannedFile
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		if d.IsDir() {
			for _, ex := range dirExcludes {
				if d.Name() == ex {
					return filepath.SkipDir
				}
			}
			return nil
		}
		// Never follow symlinks — a symlink's DirEntry.Type() reports
		// ModeSymlink without WalkDir dereferencing it.
		if d.Type()&fs.ModeSymlink != 0 || !d.Type().IsRegular() {
			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			rel = path
		}
		rel = filepath.ToSlash(rel)

		for _, ex := range baseExcludes {
			if matched, _ := filepath.Match(ex, d.Name()); matched {
				return nil
			}
		}
		for _, ex := range pathExcludes {
			if matched, _ := filepath.Match(ex, rel); matched {
				return nil
			}
		}

		fi, err := d.Info()
		if err != nil {
			return fmt.Errorf("reading metadata for %s: %w", path, err)
		}

		files = append(files, ScannedFile{
			AbsolutePath: path,
			RelativePath: rel,
			Size:         uint64(fi.Size()),
			Mode:         fileMode(fi),
			Modified:     fi.ModTime().UTC().Format(time.RFC3339),
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scanning %s: %w", root, err)
	}

	sort.Slice(files, func(i, j int) bool { return files[i].RelativePath < files[j].RelativePath })
	return files, nil
}
