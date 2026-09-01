// One stream per item table follows `/v1/updates/<table>` and folds
// every event into one changed flag. The stream sends nothing while
// the catalog is quiet, not even a heartbeat, so a read timeout means
// idleness, and only EOF or a real error means the stream dropped.

use std::io::{BufRead, BufReader, ErrorKind};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex};
use std::thread;
use std::time::{Duration, Instant};

use crate::harness::Waker;

// The connect timeout bounds one connection attempt. The read timeout
// bounds one socket read; on a quiet stream it is how often the thread
// polls the stop flag, and it says nothing about liveness.
const CONNECT_TIMEOUT: Duration = Duration::from_secs(5);
const READ_TIMEOUT: Duration = Duration::from_secs(30);
const BACKOFF_FLOOR: Duration = Duration::from_millis(250);
const BACKOFF_CEILING: Duration = Duration::from_secs(5);
const STOP_POLL: Duration = Duration::from_millis(50);

// The one changed flag and the one waker that all three streams share
// with the source.
pub(super) struct Shared {
    pub changed: AtomicBool,
    pub wake: Mutex<Option<Waker>>,
    pub stop: AtomicBool,
}

impl Default for Shared {
    fn default() -> Self {
        Self {
            changed: AtomicBool::new(false),
            wake: Mutex::new(None),
            stop: AtomicBool::new(false),
        }
    }
}

impl Shared {
    fn stopping(&self) -> bool {
        self.stop.load(Ordering::Acquire)
    }

    // The flag is set before the waker fires, so a woken loop always
    // reads changed as true.
    fn mark(&self) {
        self.changed.store(true, Ordering::Release);
        let wake = self.wake.lock().unwrap().clone();
        if let Some(wake) = wake {
            wake();
        }
    }
}

// The thread runs for the life of the source. The backoff resets once
// a stream answers, so a healthy sidecar is rejoined at the floor
// after a single drop.
pub(super) fn follow(shared: Arc<Shared>, base: String, table: &'static str) {
    thread::spawn(move || {
        let agent = ureq::AgentBuilder::new()
            .timeout_connect(CONNECT_TIMEOUT)
            .timeout_read(READ_TIMEOUT)
            .build();
        let url = format!("{base}/v1/updates/{table}");
        let mut backoff = BACKOFF_FLOOR;
        while !shared.stopping() {
            if stream(&agent, &url, &shared) {
                backoff = BACKOFF_FLOOR;
            }
            pause(&shared, backoff);
            backoff = (backoff * 2).min(BACKOFF_CEILING);
        }
    });
}

// A stream that ends marks changed, because the events between its end
// and the next stream are gone, and only a full re-read covers them. A
// failed connect does not mark, because the end that preceded it
// already did.
fn stream(agent: &ureq::Agent, url: &str, shared: &Shared) -> bool {
    let Ok(response) = agent.post(url).call() else {
        return false;
    };
    let mut reader = BufReader::new(response.into_reader());
    let mut line = String::new();
    loop {
        if shared.stopping() {
            return true;
        }
        match reader.read_line(&mut line) {
            Ok(0) => break,
            Ok(_) => {
                if !line.trim().is_empty() {
                    shared.mark();
                }
                line.clear();
            }
            Err(error) if matches!(error.kind(), ErrorKind::WouldBlock | ErrorKind::TimedOut) => {
                continue;
            }
            Err(_) => break,
        }
    }
    shared.mark();
    true
}

// The pause polls the stop flag, so a dropped source ends a
// backing-off thread within one poll interval.
fn pause(shared: &Shared, backoff: Duration) {
    let deadline = Instant::now() + backoff;
    while !shared.stopping() && Instant::now() < deadline {
        thread::sleep(STOP_POLL);
    }
}

#[cfg(test)]
mod tests;
