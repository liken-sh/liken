use std::collections::HashMap;
use std::fs;
use std::path::{Path, PathBuf};
use std::time::SystemTime;

use super::super::key::Key;

#[derive(Clone, Copy)]
struct Entry {
    bytes: u64,
    modified: SystemTime,
}

pub(super) struct Index {
    entries: HashMap<PathBuf, Entry>,
    used: u64,
    budget: u64,
}

impl Index {
    pub(super) fn scan(version: &Path, budget: usize) -> std::io::Result<Self> {
        let mut index = Self {
            entries: HashMap::new(),
            used: 0,
            budget: u64::try_from(budget).unwrap_or(u64::MAX),
        };
        for shard in fs::read_dir(version)? {
            let shard = shard?;
            let file_type = shard.file_type()?;
            if !file_type.is_dir() || !valid_shard(&shard.file_name().to_string_lossy()) {
                continue;
            }
            for file in fs::read_dir(shard.path())? {
                let file = file?;
                let file_type = file.file_type()?;
                let name = file.file_name();
                let name = name.to_string_lossy();
                if file_type.is_file() && name.starts_with(".tmp-") {
                    let _ = fs::remove_file(file.path());
                    continue;
                }
                if !file_type.is_file() || !valid_name(&name) {
                    continue;
                }
                let metadata = file.path().symlink_metadata()?;
                if !metadata.file_type().is_file() {
                    continue;
                }
                index.record(file.path(), metadata.len(), metadata.modified()?);
            }
        }
        index.trim()?;
        Ok(index)
    }

    pub(super) fn contains(&self, path: &Path) -> bool {
        self.entries.contains_key(path)
    }

    pub(super) fn forget(&mut self, path: &Path) {
        if let Some(entry) = self.entries.remove(path) {
            self.used = self.used.saturating_sub(entry.bytes);
        }
    }

    pub(super) fn published(
        &mut self,
        path: PathBuf,
        metadata: fs::Metadata,
    ) -> std::io::Result<()> {
        self.forget(&path);
        self.record(path, metadata.len(), metadata.modified()?);
        self.trim()
    }

    fn record(&mut self, path: PathBuf, bytes: u64, modified: SystemTime) {
        self.used = self.used.saturating_add(bytes);
        self.entries.insert(path, Entry { bytes, modified });
    }

    fn trim(&mut self) -> std::io::Result<()> {
        while self.used > self.budget {
            let Some(oldest) = self
                .entries
                .iter()
                .min_by_key(|(_, entry)| entry.modified)
                .map(|(path, _)| path.clone())
            else {
                break;
            };
            fs::remove_file(&oldest)?;
            self.forget(&oldest);
        }
        Ok(())
    }

    #[cfg(test)]
    pub(super) fn used(&self) -> u64 {
        self.used
    }
}

pub(super) fn path(version: &Path, key: &Key) -> Option<PathBuf> {
    let digest = key.digest()?;
    let mut hex = String::with_capacity(64);
    for byte in digest {
        use std::fmt::Write;
        write!(&mut hex, "{byte:02x}").ok()?;
    }
    Some(version.join(&hex[..2]).join(&hex[2..]))
}

fn valid_shard(name: &str) -> bool {
    name.len() == 2 && name.bytes().all(|byte| byte.is_ascii_hexdigit())
}

fn valid_name(name: &str) -> bool {
    name.len() == 62 && name.bytes().all(|byte| byte.is_ascii_hexdigit())
}
