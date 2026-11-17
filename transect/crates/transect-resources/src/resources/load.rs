//! Reading the directory under `--resources`: every `.yaml` and `.yml`
//! file in it, recursively, handed to `parse` to sort.

use std::path::{Path, PathBuf};

use super::Sorts;
use super::parse::{Read, sort};

/// Every resource under `dir`.  A directory that cannot be read loads
/// as nothing, with its error logged, so a missing directory at start
/// and one removed while `transect` runs load the same.
pub fn load<S: Sorts>(dir: &Path) -> S {
    let files = match yaml_files(dir) {
        Ok(files) => files,
        Err(error) => {
            tracing::warn!(dir = %dir.display(), %error, "reading the resources directory");
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

/// Every `.yaml` and `.yml` file under `dir`, sorted, so that the page
/// lists the files in the same order, and the first of two `Layers` is
/// the same one, on each load.
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
