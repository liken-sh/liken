// One stream per item table follows `/v1/updates/<table>`. A catalog
// table's events fold into one changed flag. A progress table's events
// each name a play by the first column of the row's primary key, and the
// stream keeps the play, so the browser reads the progress of that play's
// works and nothing else. The stream sends nothing while the store is
// quiet, not even a heartbeat, so a read timeout means idleness, and only
// EOF or a real error means the stream dropped.

use std::collections::{BTreeMap, BTreeSet};
use std::io::{BufRead, BufReader, ErrorKind};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Condvar, Mutex};
use std::thread;
use std::time::{Duration, Instant};

use crate::catalog::Change;
use crate::harness::Waker;

// The connect timeout bounds one connection attempt. The read timeout
// bounds one socket read; on a quiet stream it is how often the thread
// polls the stop flag, and it says nothing about liveness.
const CONNECT_TIMEOUT: Duration = Duration::from_secs(5);
const READ_TIMEOUT: Duration = Duration::from_secs(30);
const BACKOFF_FLOOR: Duration = Duration::from_millis(250);
const BACKOFF_CEILING: Duration = Duration::from_secs(5);

// The one changed flag and the one waker that all three streams share
// with the source, plus the revision and the signal the search index's
// build thread waits on. The browser clears `changed` when it re-reads,
// so the build thread cannot share that flag without one side losing a
// change. A count of changes lets the build thread compare against the
// revision it last built, and the condvar wakes it on each change. A
// stream that backs off sleeps on the same signal, so a halt ends its
// pause too.
pub(super) struct Shared {
    pub changed: AtomicBool,
    // The progress store's own flag, apart from the catalog's. It marks a
    // change no play names: a progress stream that ended, or an event that
    // did not parse. The browser then reads every progress it draws.
    pub progressed: AtomicBool,
    // The plays the progress streams named since the browser last asked,
    // each with the people a delete on `play_people` took off it.
    pub touched: Mutex<BTreeMap<String, BTreeSet<String>>>,
    pub wake: Mutex<Option<Waker>>,
    pub stop: AtomicBool,
    pub revision: Mutex<u64>,
    pub signal: Condvar,
}

impl Default for Shared {
    fn default() -> Self {
        Self {
            changed: AtomicBool::new(false),
            progressed: AtomicBool::new(false),
            touched: Mutex::new(BTreeMap::new()),
            wake: Mutex::new(None),
            stop: AtomicBool::new(false),
            revision: Mutex::new(0),
            signal: Condvar::new(),
        }
    }
}

impl Shared {
    pub(super) fn stopping(&self) -> bool {
        self.stop.load(Ordering::Acquire)
    }

    // Raise the stop flag and wake every waiting thread, so a dropped
    // source ends its build thread and its backing-off streams now. A
    // waiting thread reads the flag and then sleeps with the revision
    // lock held between the two, so the notify waits for that lock. Without it, a halt that lands
    // between the read and the sleep wakes nobody, and the thread sleeps
    // on with no change to come.
    pub(super) fn halt(&self) {
        self.stop.store(true, Ordering::Release);
        drop(
            self.revision
                .lock()
                .unwrap_or_else(|held| held.into_inner()),
        );
        self.signal.notify_all();
    }

    // The flag is set before the waker fires, so a woken loop always
    // reads changed as true.
    // The mark names its kind because the stream that carries it follows
    // one table, and the table says which of the two stores changed.
    pub(super) fn mark(&self, change: Change) {
        if change.catalog() {
            self.changed.store(true, Ordering::Release);
        }
        if change.progress() {
            self.progressed.store(true, Ordering::Release);
        }
        // Only a catalog change moves the revision, because the search
        // index is built over the catalog's tables and a progress row
        // holds nothing it indexes.
        if change.catalog() {
            *self
                .revision
                .lock()
                .unwrap_or_else(|held| held.into_inner()) += 1;
            self.signal.notify_all();
        }
        let wake = self.wake.lock().unwrap().clone();
        if let Some(wake) = wake {
            wake();
        }
    }
}

impl Shared {
    // Keep one play a progress stream named, and the person a delete took
    // off it, then wake the loop. The play is kept before the waker fires,
    // so a woken loop always finds it.
    pub(super) fn touch(&self, play: String, removed: Option<String>) {
        self.touched
            .lock()
            .unwrap_or_else(|held| held.into_inner())
            .entry(play)
            .or_default()
            .extend(removed);
        let wake = self.wake.lock().unwrap().clone();
        if let Some(wake) = wake {
            wake();
        }
    }

    // Take every play kept since the last call.
    pub(super) fn take_touched(&self) -> BTreeMap<String, BTreeSet<String>> {
        std::mem::take(&mut *self.touched.lock().unwrap_or_else(|held| held.into_inner()))
    }
}

// The play one event of a progress table names, and the person a delete
// on `play_people` took off it. Every progress table leads its primary key
// with the play. An event that names no play is nothing.
fn named(table: &str, line: &str) -> Option<(String, Option<String>)> {
    let event: serde_json::Value = serde_json::from_str(line).ok()?;
    let notify = event.get("notify")?.as_array()?;
    let kind = notify.first()?.as_str()?;
    let key = notify.get(1)?.as_array()?;
    let play = key.first()?.as_str()?.to_string();
    let removed = match (table, kind) {
        ("play_people", "delete") => key.get(1).and_then(|person| person.as_str()),
        _ => None,
    };
    Some((play, removed.map(str::to_string)))
}

// The thread runs for the life of the source. The backoff resets once
// a stream answers, so a healthy agent is rejoined at the floor
// after a single drop.
pub(super) fn follow(shared: Arc<Shared>, base: String, table: &'static str, change: Change) {
    thread::spawn(move || {
        let agent = ureq::AgentBuilder::new()
            .timeout_connect(CONNECT_TIMEOUT)
            .timeout_read(READ_TIMEOUT)
            .build();
        let url = format!("{base}/v1/updates/{table}");
        let mut backoff = BACKOFF_FLOOR;
        while !shared.stopping() {
            if stream(&agent, &url, &shared, table, change) {
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
fn stream(agent: &ureq::Agent, url: &str, shared: &Shared, table: &str, change: Change) -> bool {
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
                let event = line.trim();
                if !event.is_empty() {
                    fold(shared, table, change, event);
                }
                line.clear();
            }
            Err(error) if matches!(error.kind(), ErrorKind::WouldBlock | ErrorKind::TimedOut) => {
                continue;
            }
            Err(_) => break,
        }
    }
    shared.mark(change);
    true
}

// Fold one event in. A catalog event marks the catalog changed. A
// progress event keeps the play it names, and one that names no play
// marks the progress store changed, so the browser reads everything
// rather than lose the change.
fn fold(shared: &Shared, table: &str, change: Change, event: &str) {
    if !change.progress() {
        shared.mark(change);
        return;
    }
    match named(table, event) {
        Some((play, removed)) => shared.touch(play, removed),
        None => shared.mark(change),
    }
}

// The pause sleeps on the signal a halt raises, so a dropped source ends
// a backing-off thread at once, and a thread that waits out its backoff
// wakes at its end and not before. A change on another stream raises the
// same signal, and the thread sleeps again until its deadline.
fn pause(shared: &Shared, backoff: Duration) {
    let deadline = Instant::now() + backoff;
    let mut revision = shared
        .revision
        .lock()
        .unwrap_or_else(|held| held.into_inner());
    while !shared.stopping() {
        let now = Instant::now();
        if now >= deadline {
            return;
        }
        revision = shared
            .signal
            .wait_timeout(revision, deadline - now)
            .unwrap_or_else(|held| held.into_inner())
            .0;
    }
}

#[cfg(test)]
mod tests;
