//! Wakes the clock thread when another thread arms a timer or moves its
//! deadline earlier. The clock waits with no timeout while no timer is
//! armed, so without this signal it would not see a timer that the reader
//! thread or [`crate::Bus::sleep`] arms.

use std::sync::{Condvar, Mutex};
use std::time::Instant;

/// The signal count prevents a lost wakeup. The clock reads it before it
/// reads the deadline, and `wait` returns at once if it has changed since,
/// so a timer armed between that read and the wait still ends the wait.
#[derive(Debug, Default)]
pub(super) struct Alarm {
    state: Mutex<State>,
    changed: Condvar,
}

#[derive(Debug, Default)]
struct State {
    signals: u64,
    closed: bool,
}

impl Alarm {
    /// The number of signals so far, to pass to [`Alarm::wait`].
    pub(super) fn signal_count(&self) -> u64 {
        self.lock().signals
    }

    /// Wakes the clock because a timer was armed or moved earlier.
    pub(super) fn signal(&self) {
        self.lock().signals += 1;
        self.changed.notify_all();
    }

    /// Makes every `wait` return false, so the clock thread ends after the
    /// client drops its reader. A wait with no deadline would otherwise
    /// never return.
    pub(super) fn close(&self) {
        self.lock().closed = true;
        self.changed.notify_all();
    }

    /// Waits until `deadline`, or with no timeout when it is `None`, and
    /// returns early on a signal after `signals_before` was read. Returns
    /// false only when the alarm is closed. The loop waits again after a
    /// spurious wakeup of the condition variable.
    pub(super) fn wait(&self, signals_before: u64, deadline: Option<Instant>) -> bool {
        let mut state = self.lock();
        loop {
            if state.closed {
                return false;
            }
            if state.signals != signals_before {
                return true;
            }
            state = match deadline {
                None => self
                    .changed
                    .wait(state)
                    .expect("the alarm lock is poisoned"),
                Some(at) => {
                    let now = Instant::now();
                    if now >= at {
                        return true;
                    }
                    self.changed
                        .wait_timeout(state, at - now)
                        .expect("the alarm lock is poisoned")
                        .0
                }
            };
        }
    }

    fn lock(&self) -> std::sync::MutexGuard<'_, State> {
        self.state.lock().expect("the alarm lock is poisoned")
    }
}

#[cfg(test)]
mod tests;
