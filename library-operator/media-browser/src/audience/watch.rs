// The `Person` list as a file that changes while the browser runs.
// library-operator writes the list into a `ConfigMap` that the screen pod
// mounts. The kubelet updates a mounted `ConfigMap` in three steps: it
// writes the new files into a new directory beside the old one, points a
// temporary symlink at that directory, and renames the symlink over
// `..data`. The file's path resolves through `..data`, so after a swap the
// path names a new inode, and a watch on the file itself sees nothing
// after the first swap. The watch is on the directory instead, which holds
// the `..data` link on a cluster and the plain file on a local run.
//
// The thread keeps the list current in the repository's three steps: it
// opens the inotify watch, then reads the whole file once, so a change
// that lands during the read still arrives as an event. When the watch
// ends for any reason other than a drop, as when the directory is
// deleted, the thread opens it again and reads the file again. No timer
// reads the file.

use std::ffi::OsString;
use std::mem::MaybeUninit;
use std::os::fd::OwnedFd;
use std::os::unix::ffi::OsStrExt;
use std::path::{Path, PathBuf};
use std::sync::{Arc, Condvar, Mutex, MutexGuard};
use std::thread::{self, JoinHandle};
use std::time::Duration;

use rustix::fs::inotify::{self, CreateFlags, ReadFlags, WatchFlags};
use rustix::io::Errno;

use super::{Person, people_from_json};
use crate::harness::Waker;

// The pause before the thread tries again to open a watch that did not
// open, such as one on a directory that is not there yet. It is a retry
// of the subscription, and no read happens until the watch is open.
const RETRY: Duration = Duration::from_secs(5);

// The link the kubelet renames into place on every update.
const DATA: &str = "..data";

// The events that can mean the file has new contents: an editor that
// writes the file in place closes it after the write, and the kubelet and
// most editors rename a finished file over the old one. A deleted or
// moved directory ends the watch, and the kernel then queues
// `IN_IGNORED`.
fn watched() -> WatchFlags {
    WatchFlags::CLOSE_WRITE
        | WatchFlags::MOVED_TO
        | WatchFlags::DELETE_SELF
        | WatchFlags::MOVE_SELF
        | WatchFlags::ONLYDIR
}

/// What the watch read since the browser last asked: the newest list
/// that parsed, and the reason for each read that failed. A failed read
/// replaces no list, so the browser keeps the last good one.
#[derive(Debug, Default, PartialEq)]
pub struct Delivery {
    /// The newest list that parsed, or nothing where no read succeeded.
    pub people: Option<Vec<Person>>,
    /// One line for each read that failed, in the order they failed.
    pub failures: Vec<String>,
}

/// The thread that keeps the `Person` list current from one file. A drop
/// removes the watch and joins the thread.
pub struct Watch {
    shared: Arc<Shared>,
    thread: Option<JoinHandle<()>>,
}

#[derive(Default)]
struct Shared {
    state: Mutex<State>,
    // The thread's pause before a retry waits on this, so a drop ends the
    // pause at once.
    signal: Condvar,
    wake: Mutex<Option<Waker>>,
}

#[derive(Default)]
struct State {
    delivery: Delivery,
    stop: bool,
    // The inotify descriptor and the watch on it, while one is open. A
    // drop removes the watch, and the kernel then queues `IN_IGNORED`,
    // which ends the thread's blocking read. The lock orders the two, so a
    // drop never misses a watch the thread is about to follow.
    open: Option<(Arc<OwnedFd>, i32)>,
}

impl Watch {
    /// Start the thread that watches the directory this file is in and
    /// reads the file on each change.
    pub fn start(path: PathBuf) -> Self {
        let shared = Arc::new(Shared::default());
        let thread = thread::Builder::new()
            .name("people-watch".into())
            .spawn({
                let shared = shared.clone();
                move || run(&shared, &path)
            })
            .expect("the people watch thread starts");
        Self {
            shared,
            thread: Some(thread),
        }
    }

    /// Take the handle that wakes the loop. The thread calls it after each
    /// read, whether the read succeeded or failed.
    pub fn wake_by(&self, wake: Waker) {
        *lock(&self.shared.wake) = Some(wake);
    }

    /// Take what the watch read since the last call.
    pub fn take(&self) -> Delivery {
        std::mem::take(&mut lock(&self.shared.state).delivery)
    }
}

impl Drop for Watch {
    fn drop(&mut self) {
        {
            let mut state = lock(&self.shared.state);
            state.stop = true;
            if let Some((fd, wd)) = &state.open {
                // A watch the kernel already removed answers an error,
                // and its `IN_IGNORED` is already queued for the thread.
                let _ = inotify::remove_watch(&**fd, *wd);
            }
        }
        self.shared.signal.notify_all();
        if let Some(thread) = self.thread.take() {
            let _ = thread.join();
        }
    }
}

// The thread: open the watch, read once, follow the events, and start
// again when the watch ends, until a drop stops it.
fn run(shared: &Shared, path: &Path) {
    let (directory, name) = split(path);
    loop {
        match open(shared, &directory) {
            Ok(Some(fd)) => {
                shared.deliver(read(path));
                follow(shared, &fd, &name, path);
                lock(&shared.state).open = None;
            }
            Ok(None) => return,
            Err(error) => {
                eprintln!(
                    "media-browser: the people directory could not be watched: {}: {error}",
                    directory.display()
                );
                if !shared.pause() {
                    return;
                }
            }
        }
    }
}

// The directory to watch and the file's name inside it. A bare file name
// is in the working directory.
fn split(path: &Path) -> (PathBuf, OsString) {
    let directory = match path.parent() {
        Some(parent) if !parent.as_os_str().is_empty() => parent.to_path_buf(),
        _ => PathBuf::from("."),
    };
    let name = path.file_name().unwrap_or_default().to_os_string();
    (directory, name)
}

// Open an inotify descriptor with a watch on the directory, and record it
// where a drop finds it. The answer is nothing where a drop already
// stopped the thread.
fn open(shared: &Shared, directory: &Path) -> rustix::io::Result<Option<Arc<OwnedFd>>> {
    let fd = Arc::new(inotify::init(CreateFlags::CLOEXEC)?);
    let wd = inotify::add_watch(&*fd, directory, watched())?;
    let mut state = lock(&shared.state);
    if state.stop {
        return Ok(None);
    }
    state.open = Some((fd.clone(), wd));
    Ok(Some(fd))
}

// Read events until the watch ends. A batch of events that names the file
// or the `..data` link reads the file once, after the last event of the
// batch. A full queue drops events, so an overflow reads the file too.
fn follow(shared: &Shared, fd: &OwnedFd, name: &OsString, path: &Path) {
    let mut buffer = [MaybeUninit::<u8>::uninit(); 4096];
    let mut events = inotify::Reader::new(fd, &mut buffer);
    let mut changed = false;
    loop {
        let event = match events.next() {
            Ok(event) => event,
            Err(Errno::INTR) => continue,
            Err(error) => {
                eprintln!("media-browser: the people watch failed: {error}");
                return;
            }
        };
        let flags = event.events();
        if flags.intersects(ReadFlags::IGNORED | ReadFlags::DELETE_SELF | ReadFlags::MOVE_SELF) {
            return;
        }
        let named = event.file_name().is_some_and(|file| {
            let file = file.to_bytes();
            file == name.as_bytes() || file == DATA.as_bytes()
        });
        changed |= named || flags.contains(ReadFlags::QUEUE_OVERFLOW);
        if changed && events.is_buffer_empty() {
            changed = false;
            shared.deliver(read(path));
        }
    }
}

// Read and parse the whole file.
fn read(path: &Path) -> Result<Vec<Person>, String> {
    let bytes = std::fs::read(path).map_err(|error| format!("{}: {error}", path.display()))?;
    people_from_json(&bytes).map_err(|error| format!("{}: {error}", path.display()))
}

impl Shared {
    // Keep one read and wake the loop. The read is kept before the waker
    // fires, so a woken loop always finds it.
    fn deliver(&self, read: Result<Vec<Person>, String>) {
        {
            let mut state = lock(&self.state);
            match read {
                Ok(people) => state.delivery.people = Some(people),
                Err(failure) => state.delivery.failures.push(failure),
            }
        }
        let wake = lock(&self.wake).clone();
        if let Some(wake) = wake {
            wake();
        }
    }

    // Wait out the retry pause. The answer is whether the thread goes on,
    // which is false once a drop stopped it.
    fn pause(&self) -> bool {
        let state = lock(&self.state);
        if state.stop {
            return false;
        }
        let (state, _) = self
            .signal
            .wait_timeout(state, RETRY)
            .unwrap_or_else(|held| held.into_inner());
        !state.stop
    }
}

// A poisoned lock still holds a whole value here, because every writer
// replaces a field in one step.
fn lock<T>(mutex: &Mutex<T>) -> MutexGuard<'_, T> {
    mutex.lock().unwrap_or_else(|held| held.into_inner())
}

// The kubelet's layout of a mounted `ConfigMap`, which the tests build.
#[cfg(test)]
pub(crate) mod mount;

#[cfg(test)]
mod tests;
