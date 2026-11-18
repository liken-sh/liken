//! Reads every `.yaml` and `.yml` file under the `--resources`
//! directory, recursively, and passes them to `parse` to sort.

use std::path::{Path, PathBuf};

use super::Sorts;
use super::parse::{Read, sort};

/// Loads every resource under `dir`.  When the directory cannot be
/// read, logs a warning and returns no resources, so a directory that is
/// missing at start and one deleted while `transect` runs give the same
/// result.
pub fn load<S: Sorts>(dir: &Path) -> S {
    let files = match yaml_files(dir) {
        Ok(files) => files,
        Err(error) => {
            tracing::warn!(dir = %dir.display(), %error, "The resources directory cannot be read, so no resources are loaded");
            Vec::new()
        }
    };
    let read = files.into_iter().map(|path| Read {
        path: path
            .strip_prefix(dir)
            .unwrap_or(&path)
            .display()
            .to_string(),
        text: std::fs::read_to_string(&path).map_err(|error| error.to_string()),
    });
    let mut sorted: S = sort(read.collect());
    let loaded = sorted.directory();
    loaded.dir = Some(dir.display().to_string());
    let documents: usize = loaded.files.iter().map(|file| file.documents.len()).sum();
    let left_out = loaded
        .files
        .iter()
        .flat_map(|file| &file.documents)
        .filter(|document| document.conditions.iter().any(|c| c.trouble()))
        .count();
    let unread = loaded.files.iter().filter(|f| f.problem.is_some()).count();
    tracing::info!(
        files = loaded.files.len(),
        documents,
        left_out,
        unread,
        "loaded resources"
    );
    sorted
}

/// Every `.yaml` and `.yml` file under `dir`, sorted by path.  The page
/// then lists the files in the same order on each load, and when two
/// documents declare the same resource, the same one is read first and
/// used.
fn yaml_files(dir: &Path) -> std::io::Result<Vec<PathBuf>> {
    let mut files = Vec::new();
    let mut pending = vec![dir.to_path_buf()];
    while let Some(dir) = pending.pop() {
        for entry in std::fs::read_dir(&dir)? {
            let path = entry?.path();
            if path.is_dir() {
                pending.push(path);
            } else if is_yaml(&path) {
                files.push(path);
            }
        }
    }
    files.sort();
    Ok(files)
}

pub fn is_yaml(path: &Path) -> bool {
    path.extension()
        .is_some_and(|ext| ext == "yaml" || ext == "yml")
}
