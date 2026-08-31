// The window watchdog. A client with no window draws nothing while the screen
// shows the compositor's background, and that is what a compositor restart
// under a running idle pod leaves behind. Nothing inside the process can open
// the connection again, so the client exits and the kubelet restarts the
// container with backoff until the compositor answers.
//
// `IDLE_WINDOW_GRACE_SECONDS` arms it, and the operator sets that variable on
// the idle container alone. A pod that expects no window sets it nowhere and
// the watchdog stays off.

use std::time::{Duration, Instant};

/// The exit code a client with no window leaves. It is the code
/// `display/window.lua` exits with, so a person reading a container's last
/// state reads the same number for the same reason on either client.
pub const NO_WINDOW: i32 = 7;

/// The grace, and the moment the window went away.
#[derive(Debug)]
pub struct Watchdog {
    grace: Option<Duration>,
    missing_since: Option<Instant>,
}

impl Watchdog {
    /// The watchdog the grace arms. A client starts with no window, so the
    /// grace runs from `now`.
    pub fn new(grace: Option<Duration>, now: Instant) -> Self {
        Self {
            grace,
            missing_since: Some(now),
        }
    }

    /// The window went away, or one never arrived. A grace already running is
    /// left alone, so a second failure while it runs does not extend it.
    pub fn missing(&mut self, now: Instant) {
        self.missing_since.get_or_insert(now);
    }

    /// A window is up. The grace stops.
    pub fn present(&mut self) {
        self.missing_since = None;
    }

    /// Whether the grace is running. The loop takes every pass it can while it
    /// is, because a client with no window gets no event, and
    /// [`Watchdog::expire_if_late`] runs between passes.
    pub fn counting(&self) -> bool {
        self.grace.is_some() && self.missing_since.is_some()
    }

    /// Leave the process when the grace has run out with no window. The
    /// message names the grace, so a person reading the container's log reads
    /// the number the operator set.
    pub fn expire_if_late(&self, now: Instant) {
        let Some(grace) = self.grace else {
            return;
        };
        if self.late(now) {
            self.expire(&format!("no window after {} seconds", grace.as_secs_f64()));
        }
    }

    /// Whether the grace has run out with no window.
    fn late(&self, now: Instant) -> bool {
        match (self.grace, self.missing_since) {
            (Some(grace), Some(since)) => now.duration_since(since) >= grace,
            _ => false,
        }
    }

    /// Leave the process, because there is no window and none is coming. A
    /// watchdog that no grace armed returns instead, so a run outside a pod
    /// carries on and its caller reports the failure its own way.
    ///
    /// One line says what happened and that the exit is deliberate, because a
    /// non-zero exit in a log reads as a crash otherwise.
    pub fn expire(&self, reason: &str) {
        if self.grace.is_none() {
            return;
        }
        eprintln!(
            "idle-screen: {reason}; exiting {NO_WINDOW} so the kubelet restarts this container"
        );
        std::process::exit(NO_WINDOW)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn armed(seconds: u64) -> (Watchdog, Instant) {
        let now = Instant::now();
        (Watchdog::new(Some(Duration::from_secs(seconds)), now), now)
    }

    // `expire_if_late` leaves the process, so these tests read the grace
    // through `late`, which is the same answer that call acts on.
    #[test]
    fn an_unarmed_watchdog_never_expires() {
        let now = Instant::now();
        let watchdog = Watchdog::new(None, now);
        assert!(!watchdog.counting());
        assert!(!watchdog.late(now + Duration::from_secs(86_400)));
    }

    #[test]
    fn the_grace_runs_from_the_launch() {
        let (watchdog, now) = armed(30);
        assert!(watchdog.counting());
        assert!(!watchdog.late(now + Duration::from_secs(29)));
        assert!(watchdog.late(now + Duration::from_secs(30)));
    }

    #[test]
    fn a_window_stops_the_grace() {
        let (mut watchdog, now) = armed(30);
        watchdog.present();
        assert!(!watchdog.counting());
        assert!(!watchdog.late(now + Duration::from_secs(60)));
    }

    #[test]
    fn a_window_that_goes_away_starts_the_grace_again() {
        let (mut watchdog, now) = armed(30);
        watchdog.present();
        watchdog.missing(now + Duration::from_secs(60));

        assert!(watchdog.counting());
        assert!(!watchdog.late(now + Duration::from_secs(89)));
        assert!(watchdog.late(now + Duration::from_secs(90)));
    }

    #[test]
    fn a_second_failure_does_not_extend_a_running_grace() {
        let (mut watchdog, now) = armed(30);
        watchdog.missing(now + Duration::from_secs(20));
        assert!(watchdog.late(now + Duration::from_secs(30)));
    }

    #[test]
    fn a_grace_that_has_not_run_out_leaves_the_process_alone() {
        let (watchdog, now) = armed(30);
        watchdog.expire_if_late(now + Duration::from_secs(29));
    }

    #[test]
    fn an_unarmed_watchdog_leaves_the_process_alone() {
        Watchdog::new(None, Instant::now()).expire("no window");
    }
}
