// The window watchdog. When the compositor restarts, the client loses
// its window and cannot reconnect to the new compositor, so the client
// exits and `respawn.rs` starts it again. `IDLE_WINDOW_GRACE_SECONDS`
// turns the watchdog on, and the operator sets it only on the idle
// container.

use std::time::{Duration, Instant};

/// The exit code of a client that has no window. `respawn.rs` starts the
/// client again only on this code, and a person who reads a container's
/// last state can tell this cause from a crash.
pub const NO_WINDOW: i32 = 7;

/// The grace period, and the time since which the client has had no
/// window.
#[derive(Debug)]
pub struct Watchdog {
    grace: Option<Duration>,
    missing_since: Option<Instant>,
}

impl Watchdog {
    /// Creates the watchdog. The client starts with no window, so the
    /// grace period starts at `now`. A `grace` of `None` turns the
    /// watchdog off.
    pub fn new(grace: Option<Duration>, now: Instant) -> Self {
        Self {
            grace,
            missing_since: Some(now),
        }
    }

    /// Records that the client has no window. A grace period that is
    /// already running keeps its start time, so repeated failures do not
    /// extend it.
    pub fn missing(&mut self, now: Instant) {
        self.missing_since.get_or_insert(now);
    }

    /// Records that the client has a window, which stops the grace
    /// period.
    pub fn present(&mut self) {
        self.missing_since = None;
    }

    /// When the running grace period ends, or `None` when it is not
    /// running or the watchdog is off. A client with no window receives
    /// no events, so the loop sleeps until this time and then calls
    /// [`Watchdog::expire_if_late`].
    pub fn deadline(&self) -> Option<Instant> {
        Some(self.missing_since? + self.grace?)
    }

    /// Exits the process when the grace period has ended with no
    /// window. The message gives the grace in seconds, so the container's
    /// log shows the value the operator set.
    pub fn expire_if_late(&self, now: Instant) {
        let Some(grace) = self.grace else {
            return;
        };
        if self.late(now) {
            self.expire(&format!("no window after {} seconds", grace.as_secs_f64()));
        }
    }

    fn late(&self, now: Instant) -> bool {
        match (self.grace, self.missing_since) {
            (Some(grace), Some(since)) => now.duration_since(since) >= grace,
            _ => false,
        }
    }

    /// Exits the process with [`NO_WINDOW`]. When the watchdog is off it
    /// returns, so a run outside a pod continues and its caller reports
    /// the failure. The message says that the exit is deliberate,
    /// because a non-zero exit looks like a crash in a log.
    pub fn expire(&self, reason: &str) {
        if self.grace.is_none() {
            return;
        }
        eprintln!(
            "idle-screen: {reason}, so the client exits with code {NO_WINDOW} to start again"
        );
        std::process::exit(NO_WINDOW)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn with_grace(seconds: u64) -> (Watchdog, Instant) {
        let now = Instant::now();
        (Watchdog::new(Some(Duration::from_secs(seconds)), now), now)
    }

    // `expire_if_late` exits the process, so the tests check `late`,
    // the condition it acts on.
    #[test]
    fn a_watchdog_that_is_off_never_expires() {
        let now = Instant::now();
        let watchdog = Watchdog::new(None, now);
        assert_eq!(watchdog.deadline(), None);
        assert!(
            !watchdog.late(now + Duration::from_secs(86_400)),
            "a watchdog that is off is late after a day"
        );
    }

    #[test]
    fn the_grace_runs_from_the_launch() {
        let (watchdog, now) = with_grace(30);
        assert_eq!(watchdog.deadline(), Some(now + Duration::from_secs(30)));
        assert!(
            !watchdog.late(now + Duration::from_secs(29)),
            "the watchdog is late at 29 seconds of a 30 second grace"
        );
        assert!(
            watchdog.late(now + Duration::from_secs(30)),
            "the watchdog is not late at 30 seconds of a 30 second grace"
        );
    }

    #[test]
    fn a_window_stops_the_grace_period() {
        let (mut watchdog, now) = with_grace(30);
        watchdog.present();
        assert_eq!(watchdog.deadline(), None);
        assert!(
            !watchdog.late(now + Duration::from_secs(60)),
            "the watchdog is late with a window present"
        );
    }

    #[test]
    fn a_window_that_goes_away_restarts_the_grace_period() {
        let (mut watchdog, now) = with_grace(30);
        watchdog.present();
        watchdog.missing(now + Duration::from_secs(60));

        assert_eq!(watchdog.deadline(), Some(now + Duration::from_secs(90)));
        assert!(
            !watchdog.late(now + Duration::from_secs(89)),
            "the watchdog is late 29 seconds after the window went away, want not late until 30"
        );
        assert!(
            watchdog.late(now + Duration::from_secs(90)),
            "the watchdog is not late 30 seconds after the window went away"
        );
    }

    #[test]
    fn a_second_missing_window_does_not_extend_a_running_grace() {
        let (mut watchdog, now) = with_grace(30);
        watchdog.missing(now + Duration::from_secs(20));
        assert_eq!(watchdog.deadline(), Some(now + Duration::from_secs(30)));
        assert!(
            watchdog.late(now + Duration::from_secs(30)),
            "the watchdog is not late 30 seconds after the first missing window"
        );
    }

    #[test]
    fn a_grace_that_has_not_ended_leaves_the_process_running() {
        let (watchdog, now) = with_grace(30);
        watchdog.expire_if_late(now + Duration::from_secs(29));
    }

    #[test]
    fn a_watchdog_that_is_off_leaves_the_process_running() {
        Watchdog::new(None, Instant::now()).expire("no window");
    }
}
