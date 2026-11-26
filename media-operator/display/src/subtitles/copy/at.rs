//! Positional reads of a media file. The cues of a track are spread across
//! the file, and the media is often on a network share, where each read
//! waits for a round trip. So the readers fetch the cues with many reads
//! at once, each at its own offset, and no read moves a shared position.

use std::io;
use std::os::unix::fs::FileExt;
use std::sync::Arc;
use std::sync::atomic::{AtomicU64, AtomicUsize, Ordering};

/// The reads that run at once. On the laptop over NFS, 692 cues of one
/// episode took 0.27 s at 16 at a time, and the cluster headers of that
/// episode took about 14 s one at a time.
const READERS: usize = 16;

/// The most bytes that one read takes. Every size in a media file is a
/// number that the file states, and a damaged or hostile file can state
/// any number. An allocation that fails ends the process, so the readers
/// refuse a larger read before they allocate. The largest element that a
/// reader takes whole is an MP4 `moov`, whose sample tables for a long
/// film are a few megabytes.
pub const LARGEST_READ: usize = 64 << 20;

/// A file that can be read at any offset from several threads at once.
pub trait At: Sync {
    /// Fills `buffer` from `offset`, and returns how many bytes it filled.
    /// It fills less at the end of the file.
    fn fill(&self, offset: u64, buffer: &mut [u8]) -> io::Result<usize>;

    /// Up to `len` bytes from `offset`. Fewer come back at the end of the
    /// file. A read of more than `LARGEST_READ` fails before it allocates.
    fn at(&self, offset: u64, len: usize) -> io::Result<Vec<u8>> {
        if len > LARGEST_READ {
            return Err(io::Error::new(
                io::ErrorKind::InvalidData,
                format!("the file states a size of {len} bytes, over the limit of {LARGEST_READ}"),
            ));
        }
        let mut buffer = vec![0; len];
        let filled = self.fill(offset, &mut buffer)?;
        buffer.truncate(filled);
        Ok(buffer)
    }
}

impl At for std::fs::File {
    fn fill(&self, offset: u64, buffer: &mut [u8]) -> io::Result<usize> {
        let mut filled = 0;
        while filled < buffer.len() {
            match self.read_at(&mut buffer[filled..], offset + filled as u64) {
                Ok(0) => break,
                Ok(read) => filled += read,
                Err(error) if error.kind() == io::ErrorKind::Interrupted => {}
                Err(error) => return Err(error),
            }
        }
        Ok(filled)
    }
}

/// A file held in memory, which the tests build. It allocates as a file
/// does, so a test sees the limit of `LARGEST_READ`.
impl At for [u8] {
    fn fill(&self, offset: u64, buffer: &mut [u8]) -> io::Result<usize> {
        let start = usize::try_from(offset)
            .unwrap_or(usize::MAX)
            .min(self.len());
        let end = start.saturating_add(buffer.len()).min(self.len());
        buffer[..end - start].copy_from_slice(&self[start..end]);
        Ok(end - start)
    }
}

/// The number of the read that the display wants now. A read whose number
/// is no longer the current one stops at its next read of the file, so a
/// track switch does not leave the earlier read's threads reading.
#[derive(Debug, Clone, Default)]
pub struct Current(Arc<AtomicU64>);

impl Current {
    /// Makes `read` the current read.
    pub fn set(&self, read: u64) {
        self.0.store(read, Ordering::SeqCst);
    }

    pub fn get(&self) -> u64 {
        self.0.load(Ordering::SeqCst)
    }
}

/// A file read for the read numbered `read`. Each read fails once another
/// read is current.
pub struct For<'a, F: At + ?Sized> {
    pub file: &'a F,
    pub current: &'a Current,
    pub read: u64,
}

/// The error of a read that another read replaced.
pub fn superseded() -> io::Error {
    io::Error::new(io::ErrorKind::Interrupted, "another read replaced this one")
}

impl<F: At + ?Sized> At for For<'_, F> {
    fn fill(&self, offset: u64, buffer: &mut [u8]) -> io::Result<usize> {
        if self.current.get() != self.read {
            return Err(superseded());
        }
        self.file.fill(offset, buffer)
    }
}

/// Runs `work` for each index below `count` on `READERS` threads, and
/// returns the results in index order, or `None` when a thread panicked,
/// because its results are then missing.
pub fn each<T: Send>(count: usize, work: impl Fn(usize) -> T + Sync) -> Option<Vec<T>> {
    let next = AtomicUsize::new(0);
    let mut results: Vec<(usize, T)> = std::thread::scope(|scope| {
        let workers: Vec<_> = (0..READERS.min(count))
            .map(|_| {
                scope.spawn(|| {
                    let mut done = Vec::new();
                    loop {
                        let index = next.fetch_add(1, Ordering::Relaxed);
                        if index >= count {
                            break done;
                        }
                        done.push((index, work(index)));
                    }
                })
            })
            .collect();
        let mut all = Vec::with_capacity(count);
        for worker in workers {
            all.extend(worker.join().ok()?);
        }
        Some(all)
    })?;
    results.sort_by_key(|(index, _)| *index);
    Some(results.into_iter().map(|(_, result)| result).collect())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_read_past_the_end_returns_what_is_there() {
        let file: &[u8] = b"0123456789";
        assert_eq!(file.at(8, 4).ok(), Some(b"89".to_vec()));
        assert_eq!(file.at(20, 4).ok(), Some(Vec::new()));
    }

    #[test]
    fn each_result_comes_back_in_index_order() {
        assert_eq!(
            each(40, |index| index * 2),
            Some((0..40).map(|index| index * 2).collect::<Vec<_>>())
        );
        assert_eq!(each(0, |index| index), Some(Vec::<usize>::new()));
    }

    /// A thread that panics loses its results, so the whole run fails
    /// instead of returning results that no longer line up with their
    /// indexes.
    #[test]
    fn a_panic_fails_the_run() {
        let run = each(40, |index| {
            assert!(index != 17, "a worker fails");
            index
        });
        assert_eq!(run, None);
    }

    /// A size that a file states is refused before it is allocated, in
    /// memory as on disk.
    #[test]
    fn a_read_over_the_limit_fails_before_it_allocates() {
        let file: &[u8] = b"0123456789";
        assert!(file.at(0, 1 << 52).is_err());
        assert!(file.at(0, LARGEST_READ + 1).is_err());
    }

    /// A read that another read replaced fails at its next read.
    #[test]
    fn a_superseded_read_fails() {
        let file: &[u8] = b"0123456789";
        let current = Current::default();
        current.set(1);
        let reading = For {
            file,
            current: &current,
            read: 1,
        };
        assert!(reading.at(0, 4).is_ok());
        current.set(2);
        assert!(reading.at(0, 4).is_err());
    }

    /// The display reads a real file with `read_at`.
    #[test]
    fn a_file_is_read_at_an_offset() {
        let path = std::env::temp_dir().join(format!("media-display-at-{}", std::process::id()));
        std::fs::write(&path, b"0123456789").expect("writing the file");
        let file = std::fs::File::open(&path).expect("opening the file");
        assert_eq!(file.at(3, 4).ok(), Some(b"3456".to_vec()));
        assert_eq!(file.at(8, 4).ok(), Some(b"89".to_vec()));
        let _ = std::fs::remove_file(&path);
    }
}
