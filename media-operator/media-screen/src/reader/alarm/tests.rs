// The alarm, proved with real threads. A waiter runs on a thread of its own
// and reports on a channel when its wait returns, so each test reads whether
// the wait is still blocked or has returned.

use std::sync::Arc;
use std::sync::mpsc::{self, RecvTimeoutError};
use std::time::{Duration, Instant};

use super::*;

/// How long a test gives a wait to return. It is much longer than a
/// scheduler delay, so a wait that returns late fails only when it is wrong.
const PROMPT: Duration = Duration::from_millis(500);

/// A wait on its own thread, from the ring count read now, to `deadline`.
/// The channel carries the wait's answer when it returns.
fn waiting(alarm: &Arc<Alarm>, deadline: Option<Instant>) -> mpsc::Receiver<bool> {
    let seen = alarm.seen();
    let (answer, answers) = mpsc::channel();
    let alarm = Arc::clone(alarm);
    std::thread::spawn(move || answer.send(alarm.wait(seen, deadline)));
    answers
}

#[test]
fn a_wait_with_nothing_armed_blocks_until_a_ring() {
    let alarm = Arc::new(Alarm::default());
    let answers = waiting(&alarm, None);

    assert_eq!(answers.recv_timeout(PROMPT), Err(RecvTimeoutError::Timeout));

    alarm.ring();
    assert_eq!(answers.recv_timeout(PROMPT), Ok(true));
}

#[test]
fn a_wait_ends_at_its_deadline() {
    let alarm = Arc::new(Alarm::default());
    let answers = waiting(&alarm, Some(Instant::now() + Duration::from_millis(20)));

    assert_eq!(answers.recv_timeout(PROMPT), Ok(true));
}

#[test]
fn a_wait_for_a_late_deadline_ends_on_a_ring() {
    let alarm = Arc::new(Alarm::default());
    let answers = waiting(&alarm, Some(Instant::now() + Duration::from_secs(3600)));

    alarm.ring();

    assert_eq!(answers.recv_timeout(PROMPT), Ok(true));
}

#[test]
fn a_ring_before_the_wait_starts_is_not_lost() {
    let alarm = Alarm::default();
    let seen = alarm.seen();

    alarm.ring();

    assert!(alarm.wait(seen, None));
}

#[test]
fn a_closed_alarm_ends_every_wait() {
    let alarm = Arc::new(Alarm::default());
    let answers = waiting(&alarm, None);

    alarm.close();

    assert_eq!(answers.recv_timeout(PROMPT), Ok(false));
    assert!(!alarm.wait(alarm.seen(), None));
}
