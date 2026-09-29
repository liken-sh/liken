//! The alarm the clock thread waits on. The clock sleeps until the armed
//! deadline, or with no timeout while nothing is armed, so an idle screen
//! wakes this thread for nothing. Another thread can arm or move a window
//! while the clock sleeps: the reader thread on a delivery, and the client
//! through [`crate::Bus::sleep`]. That thread rings the alarm, and the clock
//! reads the deadline again.

use std::sync::{Condvar, Mutex};
use std::time::Instant;

/// A ring count and a closed mark behind one lock, and the condition
/// variable the clock waits on.
///
/// The count guards against a lost ring. The clock reads the count before it
/// reads the deadline, and its wait returns at once when the count moved
/// since that read. A window armed between the clock's read of the deadline
/// and the start of its wait therefore still ends the wait.
#[derive(Debug, Default)]
pub(super) struct Alarm {
    state: Mutex<State>,
    bell: Condvar,
}

#[derive(Debug, Default)]
struct State {
    rings: u64,
    closed: bool,
}

impl Alarm {
    /// The ring count now. The clock reads it before it reads the deadline,
    /// and hands it to [`Alarm::wait`].
    pub(super) fn seen(&self) -> u64 {
        self.lock().rings
    }

    /// Wake the clock, because a window was armed or moved earlier.
    pub(super) fn ring(&self) {
        self.lock().rings += 1;
        self.bell.notify_all();
    }

    /// Wake the clock for the last time, because the client dropped its
    /// reader. The clock sleeps with no timeout while nothing is armed, so
    /// without this call the thread never ends.
    pub(super) fn close(&self) {
        self.lock().closed = true;
        self.bell.notify_all();
    }

    /// Sleep until `deadline`, or with no timeout when it is `None`, and
    /// return early on a ring after `seen`. The answer is false only when the
    /// alarm closed, which ends the clock thread.
    ///
    /// The loop absorbs spurious wakeups: a return from the condition
    /// variable that no ring, close, or deadline caused waits again.
    pub(super) fn wait(&self, seen: u64, deadline: Option<Instant>) -> bool {
        let mut state = self.lock();
        loop {
            if state.closed {
                return false;
            }
            if state.rings != seen {
                return true;
            }
            state = match deadline {
                None => self
                    .bell
                    .wait(state)
                    .expect("no thread panics with the lock"),
                Some(at) => {
                    let now = Instant::now();
                    if now >= at {
                        return true;
                    }
                    self.bell
                        .wait_timeout(state, at - now)
                        .expect("no thread panics with the lock")
                        .0
                }
            };
        }
    }

    fn lock(&self) -> std::sync::MutexGuard<'_, State> {
        self.state.lock().expect("no thread panics with the lock")
    }
}

#[cfg(test)]
mod tests;
