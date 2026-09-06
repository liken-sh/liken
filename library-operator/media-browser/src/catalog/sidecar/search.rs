// The search index over the sidecar's replica. The sidecar owns it
// because the sidecar owns the replica and already follows the updates
// feed, so the signal that a row changed is the signal to build again.
// The build runs on a thread of its own with a read-only connection of
// its own, never on the frame, and a `Search` answers an empty wall
// until the first build lands.

use std::path::{Path, PathBuf};
use std::sync::{Arc, Mutex};
use std::thread;
use std::time::{Duration, Instant};

use rusqlite::{Connection, OpenFlags};

use super::updates::Shared;
use crate::catalog::Slot;
use crate::catalog::search::Index;

// The one read that builds an index off the replica.
pub(super) mod read;

/// How long the updates feed must be quiet before a build starts. A scan
/// writes thousands of rows and the feed signals each one, so a build
/// per signal would hold the CPU for the length of the scan. Two seconds
/// of quiet turns a burst into one build.
pub const QUIET: Duration = Duration::from_secs(2);

// How often a waiting thread reads the stop flag, so a dropped source
// ends the thread within this long.
const STOP_POLL: Duration = Duration::from_millis(50);

/// The index as the frame reads it and the build thread replaces it. The
/// frame's source and its `reader()` are two sources over one replica,
/// and both hold one shelf so one build serves both.
#[derive(Debug, Default)]
pub struct Shelf {
    index: Mutex<Option<Arc<Index>>>,
}

impl Shelf {
    /// The ranked hits for this text, or nothing until the first build
    /// lands.
    pub fn find(&self, text: &str) -> Vec<Slot> {
        let index = self.held();
        index.map_or_else(Vec::new, |index| index.find(text))
    }

    /// The index as it stands, or nothing before the first build.
    pub fn held(&self) -> Option<Arc<Index>> {
        self.lock().clone()
    }

    fn put(&self, index: Index) {
        *self.lock() = Some(Arc::new(index));
    }

    fn lock(&self) -> std::sync::MutexGuard<'_, Option<Arc<Index>>> {
        self.index.lock().unwrap()
    }
}

/// Build the index now, and again after every change followed by `quiet`
/// with no further change. The thread runs until the shared state is
/// halted, which the source's `Drop` does.
pub fn follow(shelf: Arc<Shelf>, shared: Arc<Shared>, database: PathBuf, quiet: Duration) {
    thread::spawn(move || {
        let mut built = 0;
        loop {
            if let Some(index) = read(&database) {
                shelf.put(index);
            }
            match wait(&shared, built, quiet) {
                Some(revision) => built = revision,
                None => return,
            }
        }
    });
}

// One build off a read-only connection of its own. A replica that is
// not there yet, or a read that fails, builds nothing and keeps the
// index that stands; the failure is logged because nothing else reports
// it, and a search that answers nothing looks the same either way.
fn read(database: &Path) -> Option<Index> {
    let flags = OpenFlags::SQLITE_OPEN_READ_ONLY | OpenFlags::SQLITE_OPEN_NO_MUTEX;
    let connection = Connection::open_with_flags(database, flags)
        .inspect_err(|error| {
            eprintln!(
                "media-browser: cannot open the catalog {} to index it: {error}",
                database.display()
            );
        })
        .ok()?;
    read::index(&connection)
        .inspect_err(|error| eprintln!("media-browser: cannot index the catalog: {error}"))
        .ok()
}

// Wait for a change past the last build, then for the quiet period. The
// answer is the revision the next build covers, or nothing when the
// source stopped.
fn wait(shared: &Shared, built: u64, quiet: Duration) -> Option<u64> {
    let mut revision = shared.revision.lock().unwrap();
    while !shared.stopping() && *revision <= built {
        revision = shared.signal.wait_timeout(revision, STOP_POLL).unwrap().0;
    }
    // Every further change moves the deadline out again, so a scan that
    // signals every few hundred milliseconds gets one build at its end.
    let mut deadline = Instant::now() + quiet;
    loop {
        if shared.stopping() {
            return None;
        }
        let mark = *revision;
        let now = Instant::now();
        if now >= deadline {
            return Some(mark);
        }
        revision = shared
            .signal
            .wait_timeout(revision, (deadline - now).min(STOP_POLL))
            .unwrap()
            .0;
        if *revision != mark {
            deadline = Instant::now() + quiet;
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    // A quiet period short enough to measure in a test.
    const BRIEF: Duration = Duration::from_millis(120);

    #[test]
    fn a_shelf_with_no_index_answers_nothing() {
        let shelf = Shelf::default();
        assert!(shelf.find("batman").is_empty());
        assert!(shelf.held().is_none());
    }

    #[test]
    fn a_change_rebuilds_only_after_the_feed_falls_quiet() {
        let shared = Shared::default();
        shared.mark();
        let started = Instant::now();
        assert_eq!(wait(&shared, 0, BRIEF), Some(1));
        assert!(started.elapsed() >= BRIEF);
    }

    #[test]
    fn a_further_change_puts_the_quiet_period_out_again() {
        let shared = Arc::new(Shared::default());
        shared.mark();
        let again = shared.clone();
        thread::spawn(move || {
            thread::sleep(BRIEF / 2);
            again.mark();
        });
        let started = Instant::now();
        assert_eq!(wait(&shared, 0, BRIEF), Some(2));
        assert!(started.elapsed() >= BRIEF + BRIEF / 2);
    }

    #[test]
    fn a_stopped_source_ends_the_wait() {
        let shared = Arc::new(Shared::default());
        let stopping = shared.clone();
        thread::spawn(move || {
            thread::sleep(BRIEF / 4);
            stopping.halt();
        });
        assert_eq!(wait(&shared, 0, BRIEF), None);
    }

    #[test]
    fn a_stop_during_the_quiet_period_ends_the_wait() {
        let shared = Arc::new(Shared::default());
        shared.mark();
        let stopping = shared.clone();
        thread::spawn(move || {
            thread::sleep(BRIEF / 4);
            stopping.halt();
        });
        assert_eq!(wait(&shared, 0, BRIEF), None);
    }

    #[test]
    fn a_catalog_that_is_not_there_indexes_as_nothing() {
        assert!(read(Path::new("/nonexistent/catalog.db")).is_none());
    }
}
