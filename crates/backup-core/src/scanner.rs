//! Filesystem scanning: walks a root directory and returns the files to
//! back up, honoring exclude patterns. Symlinks are never followed —
//! deliberately: following them risks infinite loops on cyclic symlinks and
//! can silently pull files from outside the intended backup root.

use std::path::{Path, PathBuf};
use std::time::SystemTime;

use ignore::overrides::OverrideBuilder;
use ignore::WalkBuilder;
use time::format_description::well_known::Rfc3339;
use time::OffsetDateTime;

use crate::error::{Error, Result};

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ScannedFile {
    pub absolute_path: PathBuf,
    /// Relative to the scan root, forward-slash-separated regardless of
    /// platform, so manifests are portable across Windows/macOS/Linux.
    pub relative_path: String,
    pub size: u64,
    pub mode: u32,
    pub modified: String, // RFC 3339
}

/// Walks `root`, returning every regular file not matched by
/// `exclude_patterns` (`.gitignore`-style globs, e.g. `"*.tmp"`,
/// `"node_modules/"`). Directories, symlinks, and anything else that isn't a
/// regular file are skipped. Results are sorted by relative path for
/// deterministic manifests (two scans of an unchanged tree must agree).
pub fn scan(root: &Path, exclude_patterns: &[String]) -> Result<Vec<ScannedFile>> {
    if !root.is_dir() {
        return Err(Error::Scan(format!(
            "backup root {} does not exist or is not a directory",
            root.display()
        )));
    }

    let mut overrides = OverrideBuilder::new(root);
    for pattern in exclude_patterns {
        // ignore::overrides treats a bare pattern as an include; prefixing
        // with "!" makes it an exclude, matching this function's contract.
        overrides
            .add(&format!("!{pattern}"))
            .map_err(|e| Error::Scan(format!("invalid exclude pattern {pattern:?}: {e}")))?;
    }
    let overrides = overrides
        .build()
        .map_err(|e| Error::Scan(format!("building exclude patterns: {e}")))?;

    let mut files = Vec::new();
    let walker = WalkBuilder::new(root)
        .follow_links(false)
        .overrides(overrides)
        // Scanning a backup source should not be governed by an unrelated
        // .gitignore the user happens to have lying around in it.
        .git_ignore(false)
        .git_global(false)
        .git_exclude(false)
        .ignore(false)
        .build();

    for entry in walker {
        let entry = entry.map_err(|e| Error::Scan(e.to_string()))?;
        let file_type = match entry.file_type() {
            Some(t) => t,
            None => continue, // e.g. stdin sentinel; not possible for a real walk, but handle it
        };
        if !file_type.is_file() {
            continue;
        }

        let absolute_path = entry.path().to_path_buf();
        let metadata = entry.metadata().map_err(|e| {
            Error::Scan(format!(
                "reading metadata for {}: {e}",
                absolute_path.display()
            ))
        })?;

        let relative_path = absolute_path
            .strip_prefix(root)
            .unwrap_or(&absolute_path)
            .components()
            .map(|c| c.as_os_str().to_string_lossy().into_owned())
            .collect::<Vec<_>>()
            .join("/");

        files.push(ScannedFile {
            absolute_path,
            relative_path,
            size: metadata.len(),
            mode: file_mode(&metadata),
            modified: format_modified(metadata.modified().ok()),
        });
    }

    files.sort_by(|a, b| a.relative_path.cmp(&b.relative_path));
    Ok(files)
}

#[cfg(unix)]
fn file_mode(metadata: &std::fs::Metadata) -> u32 {
    use std::os::unix::fs::PermissionsExt;
    metadata.permissions().mode() & 0o7777
}

#[cfg(not(unix))]
fn file_mode(_metadata: &std::fs::Metadata) -> u32 {
    // Windows has no Unix permission bits; a fixed placeholder is restored
    // as owner-read/write. Real ACL preservation is not implemented —
    // documented limitation, tracked for the desktop app milestone.
    0o644
}

fn format_modified(modified: Option<SystemTime>) -> String {
    modified
        .and_then(|t| OffsetDateTime::from(t).format(&Rfc3339).ok())
        .unwrap_or_else(|| "1970-01-01T00:00:00Z".to_string())
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::fs;

    #[test]
    fn finds_nested_files_and_skips_excluded_ones() {
        let dir = tempfile::tempdir().unwrap();
        fs::write(dir.path().join("keep.txt"), b"a").unwrap();
        fs::create_dir(dir.path().join("sub")).unwrap();
        fs::write(dir.path().join("sub/keep2.txt"), b"bb").unwrap();
        fs::write(dir.path().join("skip.tmp"), b"ccc").unwrap();
        fs::create_dir(dir.path().join("node_modules")).unwrap();
        fs::write(dir.path().join("node_modules/dep.js"), b"dddd").unwrap();

        let files = scan(
            dir.path(),
            &["*.tmp".to_string(), "node_modules/".to_string()],
        )
        .unwrap();
        let paths: Vec<_> = files.iter().map(|f| f.relative_path.as_str()).collect();

        assert_eq!(paths, vec!["keep.txt", "sub/keep2.txt"]);
    }

    #[test]
    fn relative_paths_use_forward_slashes_and_results_are_sorted() {
        let dir = tempfile::tempdir().unwrap();
        fs::create_dir(dir.path().join("b")).unwrap();
        fs::write(dir.path().join("b/file.txt"), b"x").unwrap();
        fs::write(dir.path().join("a.txt"), b"y").unwrap();

        let files = scan(dir.path(), &[]).unwrap();
        assert_eq!(files[0].relative_path, "a.txt");
        assert_eq!(files[1].relative_path, "b/file.txt");
        assert!(!files[1].relative_path.contains('\\'));
    }

    #[test]
    fn reports_correct_file_size() {
        let dir = tempfile::tempdir().unwrap();
        fs::write(dir.path().join("f"), vec![0u8; 12345]).unwrap();
        let files = scan(dir.path(), &[]).unwrap();
        assert_eq!(files[0].size, 12345);
    }

    #[test]
    fn nonexistent_root_is_an_error() {
        assert!(scan(Path::new("/no/such/path/deeparchive-test"), &[]).is_err());
    }

    #[cfg(unix)]
    #[test]
    fn symlinks_are_not_followed() {
        let dir = tempfile::tempdir().unwrap();
        let target_dir = tempfile::tempdir().unwrap();
        fs::write(target_dir.path().join("outside.txt"), b"should not appear").unwrap();
        std::os::unix::fs::symlink(target_dir.path(), dir.path().join("link")).unwrap();
        fs::write(dir.path().join("inside.txt"), b"x").unwrap();

        let files = scan(dir.path(), &[]).unwrap();
        let paths: Vec<_> = files.iter().map(|f| f.relative_path.as_str()).collect();
        assert_eq!(paths, vec!["inside.txt"]);
    }
}
