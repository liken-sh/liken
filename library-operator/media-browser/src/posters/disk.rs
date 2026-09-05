// The disk cache stores the scaled result under the same key as memory.
// Each request still stats its source before a hit, so replacement invalidates
// the local result without reading the source bytes.

use std::fs::{self, File, OpenOptions};
use std::io::{ErrorKind, Write};
use std::path::{Path, PathBuf};
use std::sync::Mutex;
use std::sync::atomic::{AtomicBool, AtomicU64, Ordering};

use super::key::Key;
use super::store::Poster;

mod format;
mod trim;

use format::{FileRead, ReadOutcome, SourceStamp};
use trim::Index;

/// The default disk cache holds 512 MiB of scaled posters.
pub const DEFAULT_BUDGET: usize = 512 * 1024 * 1024;

static WARNED: AtomicBool = AtomicBool::new(false);
static TEMP_ID: AtomicU64 = AtomicU64::new(0);

pub(super) enum Result {
    Cache(Poster),
    Source(Option<Poster>),
}

struct State {
    version: PathBuf,
    index: Mutex<Index>,
    writes: AtomicBool,
}

pub(super) struct DiskCache {
    state: Option<State>,
}

impl DiskCache {
    pub(super) fn new(root: PathBuf) -> Self {
        Self::with_budget(root, DEFAULT_BUDGET)
    }

    fn with_budget(root: PathBuf, budget: usize) -> Self {
        match open(root, budget) {
            Ok(state) => Self { state: Some(state) },
            Err(error) => {
                warn_once(&error);
                Self { state: None }
            }
        }
    }

    pub(super) fn resolve<F>(&self, key: &Key, source: &Path, mut decode: F) -> Result
    where
        F: FnMut() -> Option<Poster>,
    {
        let Some(state) = &self.state else {
            return Result::Source(decode());
        };
        let first = source_stamp(source);
        match first {
            Ok(Some(stamp)) => {
                if let Some(poster) = state.load(key, Some(stamp)) {
                    return Result::Cache(poster);
                }
                let (poster, stable) = decode_stable(stamp, &mut decode, || source_stamp(source));
                if let (Some(stamp), Some(poster)) = (stable, poster.as_ref()) {
                    state.store(key, stamp, poster);
                }
                Result::Source(poster)
            }
            Err(error) if error.kind() == ErrorKind::NotFound => {
                // A deleted source can use the last result. Every other metadata
                // error refuses stale data because it cannot prove the identity.
                if let Some(poster) = state.load(key, None) {
                    return Result::Cache(poster);
                }
                Result::Source(decode())
            }
            Ok(None) | Err(_) => Result::Source(decode()),
        }
    }
}

impl State {
    fn load(&self, key: &Key, source: Option<SourceStamp>) -> Option<Poster> {
        let path = trim::path(&self.version, key)?;
        let file = {
            let mut index = self
                .index
                .lock()
                .expect("the disk cache mutex is never poisoned");
            if !index.contains(&path) {
                return None;
            }
            match format::read_file(&path, key) {
                Ok(FileRead::Bytes(bytes)) => bytes,
                Ok(FileRead::Invalid) => {
                    remove_invalid(&path, &mut index);
                    return None;
                }
                Err(error) if error.kind() == ErrorKind::NotFound => {
                    index.forget(&path);
                    return None;
                }
                Err(_) => return None,
            }
        };
        match format::parse(&file, key, source) {
            ReadOutcome::Hit(poster) => Some(poster),
            ReadOutcome::MetadataMiss => None,
            ReadOutcome::Invalid => {
                let mut index = self
                    .index
                    .lock()
                    .expect("the disk cache mutex is never poisoned");
                remove_invalid(&path, &mut index);
                None
            }
        }
    }

    fn store(&self, key: &Key, stamp: SourceStamp, poster: &Poster) {
        if !self.writes.load(Ordering::Acquire) {
            return;
        }
        let Some(bytes) = format::encode(key, stamp, poster) else {
            return;
        };
        if let Err(error) = self.publish(key, &bytes) {
            self.writes.store(false, Ordering::Release);
            warn_once(&error);
        }
    }

    fn publish(&self, key: &Key, bytes: &[u8]) -> std::io::Result<()> {
        self.publish_with_flush(key, bytes, File::sync_all)
    }

    fn publish_with_flush<F>(&self, key: &Key, bytes: &[u8], flush: F) -> std::io::Result<()>
    where
        F: FnOnce(&File) -> std::io::Result<()>,
    {
        let Some(path) = trim::path(&self.version, key) else {
            self.writes.store(false, Ordering::Release);
            return Err(std::io::Error::other("the poster cache key is too long"));
        };
        let Some(parent) = path.parent() else {
            self.writes.store(false, Ordering::Release);
            return Err(std::io::Error::other("the poster cache path has no parent"));
        };
        let prepared = (|| {
            fs::create_dir_all(parent)?;
            validate_shard(parent)?;
            let temp_path = parent.join(format!(
                ".tmp-{}-{}",
                std::process::id(),
                TEMP_ID.fetch_add(1, Ordering::Relaxed)
            ));
            let mut temp = TempFile::create(temp_path)?;
            temp.file.write_all(bytes)?;
            flush(&temp.file)?;
            Ok(temp)
        })();
        let mut temp = match prepared {
            Ok(temp) => temp,
            Err(error) => {
                self.writes.store(false, Ordering::Release);
                return Err(error);
            }
        };
        let mut index = self
            .index
            .lock()
            .expect("the disk cache mutex is never poisoned");
        if !self.writes.load(Ordering::Acquire) {
            return Ok(());
        }
        let committed = (|| {
            validate_shard(parent)?;
            fs::rename(&temp.path, &path)?;
            temp.published = true;
            let metadata = path.symlink_metadata()?;
            if !metadata.file_type().is_file() {
                return Err(std::io::Error::other(
                    "the poster cache entry is not a file",
                ));
            }
            index.published(path.clone(), metadata)
        })();
        if committed.is_err() {
            self.writes.store(false, Ordering::Release);
        }
        committed
    }
}

fn open(root: PathBuf, budget: usize) -> std::io::Result<State> {
    let version = root.join("v1");
    fs::create_dir_all(&version)?;
    let file_type = version.symlink_metadata()?.file_type();
    if !file_type.is_dir() || file_type.is_symlink() {
        return Err(std::io::Error::other(
            "the poster cache version is not a directory",
        ));
    }
    let index = Index::scan(&version, budget)?;
    Ok(State {
        version,
        index: Mutex::new(index),
        writes: AtomicBool::new(true),
    })
}

fn validate_shard(path: &Path) -> std::io::Result<()> {
    let file_type = path.symlink_metadata()?.file_type();
    if !file_type.is_dir() || file_type.is_symlink() {
        return Err(std::io::Error::other(
            "the poster cache shard is not a directory",
        ));
    }
    Ok(())
}

fn source_stamp(path: &Path) -> std::io::Result<Option<SourceStamp>> {
    Ok(SourceStamp::from_metadata(&path.metadata()?))
}

fn decode_stable<F, S>(
    before: SourceStamp,
    decode: &mut F,
    mut stamp: S,
) -> (Option<Poster>, Option<SourceStamp>)
where
    F: FnMut() -> Option<Poster>,
    S: FnMut() -> std::io::Result<Option<SourceStamp>>,
{
    let first = decode();
    let Ok(Some(after)) = stamp() else {
        return (first, None);
    };
    if after == before {
        let stable = first.as_ref().map(|_| after);
        return (first, stable);
    }
    let second = decode();
    let stable = match stamp() {
        Ok(Some(final_stamp)) if final_stamp == after && second.is_some() => Some(final_stamp),
        _ => None,
    };
    (second, stable)
}

fn remove_invalid(path: &Path, index: &mut Index) {
    let _ = fs::remove_file(path);
    index.forget(path);
}

fn warn_once(error: &std::io::Error) {
    if !WARNED.swap(true, Ordering::Relaxed) {
        eprintln!("media-browser: the poster disk cache failed: {error}");
    }
}

struct TempFile {
    path: PathBuf,
    file: File,
    published: bool,
}

impl TempFile {
    fn create(path: PathBuf) -> std::io::Result<Self> {
        let file = OpenOptions::new()
            .write(true)
            .create_new(true)
            .open(&path)?;
        Ok(Self {
            path,
            file,
            published: false,
        })
    }
}

impl Drop for TempFile {
    fn drop(&mut self) {
        if !self.published {
            let _ = fs::remove_file(&self.path);
        }
    }
}

#[cfg(test)]
mod tests;
