package backupclient

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestFindsNestedFilesAndSkipsExcludedOnes(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "keep.txt", "a")
	write(t, dir, "sub/keep2.txt", "bb")
	write(t, dir, "skip.tmp", "ccc")
	write(t, dir, "node_modules/dep.js", "dddd")

	files, err := Scan(dir, []string{"*.tmp", "node_modules/"})
	if err != nil {
		t.Fatal(err)
	}
	paths := relPaths(files)
	want := []string{"keep.txt", "sub/keep2.txt"}
	if !equal(paths, want) {
		t.Fatalf("got %v, want %v", paths, want)
	}
}

func TestRelativePathsUseForwardSlashesAndResultsAreSorted(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "b/file.txt", "x")
	write(t, dir, "a.txt", "y")

	files, err := Scan(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if files[0].RelativePath != "a.txt" || files[1].RelativePath != "b/file.txt" {
		t.Fatalf("unexpected order/paths: %v", relPaths(files))
	}
}

func TestReportsCorrectFileSize(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "f", string(make([]byte, 12345)))

	files, err := Scan(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if files[0].Size != 12345 {
		t.Fatalf("got size %d, want 12345", files[0].Size)
	}
}

func TestNonexistentRootIsAnError(t *testing.T) {
	if _, err := Scan(filepath.Join(os.TempDir(), "no-such-deeparchive-test-dir"), nil); err == nil {
		t.Fatal("expected an error for a nonexistent root")
	}
}

func TestSymlinksAreNotFollowed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevated privileges on Windows")
	}
	dir := t.TempDir()
	target := t.TempDir()
	write(t, target, "outside.txt", "should not appear")
	if err := os.Symlink(target, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "inside.txt", "x")

	files, err := Scan(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	paths := relPaths(files)
	want := []string{"inside.txt"}
	if !equal(paths, want) {
		t.Fatalf("got %v, want %v", paths, want)
	}
}

func write(t *testing.T, dir, rel, content string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func relPaths(files []ScannedFile) []string {
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = f.RelativePath
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
