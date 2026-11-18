// These tests use real threads. Each `wait` runs on its own thread and sends
// its result on a channel, so a test can check whether the wait has returned.

use std::sync::Arc;
use std::sync::mpsc::{self, RecvTimeoutError};
use std::time::{Duration, Instant};

use super::*;

/// How long a test waits for `wait` to return. It is far longer than a
/// scheduling delay, so a test fails only when the wait is stuck.
const WAIT_TIMEOUT: Duration = Duration::from_millis(500);

/// Starts `wait` on a new thread and returns the channel that receives its
/// result.
fn waiting(alarm: &Arc<Alarm>, deadline: Option<Instant>) -> mpsc::Receiver<bool> {
    let signals = alarm.signal_count();
    let (result, results) = mpsc::channel();
    let alarm = Arc::clone(alarm);
    std::thread::spawn(move || result.send(alarm.wait(signals, deadline)));
    results
}

#[test]
fn a_wait_with_no_deadline_blocks_until_a_signal() {
    let alarm = Arc::new(Alarm::default());
    let results = waiting(&alarm, None);

    assert_eq!(
        results.recv_timeout(WAIT_TIMEOUT),
        Err(RecvTimeoutError::Timeout)
    );

    alarm.signal();
    assert_eq!(results.recv_timeout(WAIT_TIMEOUT), Ok(true));
}

#[test]
fn a_wait_ends_at_its_deadline() {
    let alarm = Arc::new(Alarm::default());
    let results = waiting(&alarm, Some(Instant::now() + Duration::from_millis(20)));

    assert_eq!(results.recv_timeout(WAIT_TIMEOUT), Ok(true));
}

#[test]
fn a_wait_for_a_late_deadline_ends_on_a_signal() {
    let alarm = Arc::new(Alarm::default());
    let results = waiting(&alarm, Some(Instant::now() + Duration::from_secs(3600)));

    alarm.signal();

    assert_eq!(results.recv_timeout(WAIT_TIMEOUT), Ok(true));
}

#[test]
fn a_signal_before_the_wait_starts_is_not_lost() {
    let alarm = Alarm::default();
    let signals = alarm.signal_count();

    alarm.signal();

    assert!(
        alarm.wait(signals, None),
        "the wait after an earlier signal returned false, want true"
    );
}

#[test]
fn a_closed_alarm_ends_every_wait() {
    let alarm = Arc::new(Alarm::default());
    let results = waiting(&alarm, None);

    alarm.close();

    assert_eq!(results.recv_timeout(WAIT_TIMEOUT), Ok(false));
    assert!(
        !alarm.wait(alarm.signal_count(), None),
        "a wait on a closed alarm returned true, want false"
    );
}
