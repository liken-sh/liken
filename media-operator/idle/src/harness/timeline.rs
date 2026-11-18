// When a run's script keys fire, when the run reaches its deadline, and
// when the loop draws its next frame. The functions take the time as an
// argument, so tests call them with numbers and open no window.

/// The `--script` keys, the `--quit-after` deadline, and the index of
/// the next script key to fire. The index only increases, so each key
/// fires once.
#[derive(Debug, Default)]
pub struct Timeline {
    script: Vec<(f64, String)>,
    quit_after: Option<f64>,
    next_step: usize,
}

impl Timeline {
    pub fn new(script: Vec<(f64, String)>, quit_after: Option<f64>) -> Self {
        Self {
            script,
            quit_after,
            next_step: 0,
        }
    }

    /// Returns the script keys scheduled at or before `at`, in script
    /// order, and skips past them so a later call does not return them
    /// again. The harness handles each key as a keyboard press.
    pub fn due(&mut self, at: f64) -> Vec<String> {
        let mut keys = Vec::new();

        while let Some((when, key)) = self.script.get(self.next_step)
            && *when <= at
        {
            keys.push(key.clone());
            self.next_step += 1;
        }

        keys
    }

    /// True once `at` reaches the `--quit-after` second.
    pub fn past_deadline(&self, at: f64) -> bool {
        self.quit_after.is_some_and(|limit| at >= limit)
    }

    /// The earlier of the next script key's second and the deadline. The
    /// loop sleeps until then, so a run measured with `--quit-after`
    /// draws only when the screen changes, as it does in a pod.
    pub fn next_due(&self) -> Option<f64> {
        let step = self.script.get(self.next_step).map(|(when, _)| *when);
        [step, self.quit_after]
            .into_iter()
            .flatten()
            .min_by(f64::total_cmp)
    }
}

/// The longest the loop sleeps between two reads of the screen's
/// sources, in seconds. A bus message normally wakes the loop through
/// the screen's waker, and this bound makes the loop read the bus even
/// when that wake fails. A wake with nothing due draws no frame.
pub const BACKSTOP: f64 = 1.0;

/// When the loop draws its next frame.
#[derive(Debug, PartialEq)]
pub enum Wake {
    /// Draw now.
    Now,
    /// Draw at this second on the run's clock. The loop sleeps toward it
    /// in steps of at most [`BACKSTOP`] and reads the screen's sources on
    /// each wake.
    At(f64),
    /// Draw only when an event arrives.
    Never,
}

/// Returns when the loop draws next. `at` is the current second on the
/// run's clock, and `next` is the earliest second at which anything is
/// due: a change in the screen, the next script key, the next capture,
/// or the deadline. `immediate` is set after the surface changes size,
/// because the last frame no longer fits it.
pub fn wake(immediate: bool, at: f64, next: Option<f64>) -> Wake {
    if immediate {
        return Wake::Now;
    }
    match next {
        // An infinite second means that nothing is scheduled.
        Some(next) if next.is_infinite() => Wake::Never,
        Some(next) if next > at => Wake::At(next),
        Some(_) => Wake::Now,
        None => Wake::Never,
    }
}

/// The second the loop wakes next while it waits for a frame due at
/// `next`.
pub fn sleep_until(at: f64, next: f64) -> f64 {
    next.min(at + BACKSTOP)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn scripted(steps: &[(f64, &str)]) -> Timeline {
        Timeline::new(
            steps
                .iter()
                .map(|(at, key)| (*at, key.to_string()))
                .collect(),
            None,
        )
    }

    fn keys(names: &[&str]) -> Vec<String> {
        names.iter().map(|name| name.to_string()).collect()
    }

    #[test]
    fn a_step_is_due_at_its_second_and_not_before() {
        let mut timeline = scripted(&[(1.0, "p")]);
        assert_eq!(timeline.due(0.999), keys(&[]));
        assert_eq!(timeline.due(1.0), keys(&["p"]));
    }

    #[test]
    fn due_returns_every_passed_step_in_order_and_once() {
        let mut timeline = scripted(&[(0.1, "up"), (0.2, "down"), (0.3, "p")]);
        assert_eq!(timeline.due(0.25), keys(&["up", "down"]));
        assert_eq!(timeline.due(0.25), keys(&[]));
        assert_eq!(timeline.due(9.0), keys(&["p"]));
    }

    #[test]
    fn a_run_ends_at_its_deadline() {
        let timeline = Timeline::new(Vec::new(), Some(3.0));
        assert!(
            !timeline.past_deadline(2.999),
            "the run is past its 3 second deadline at 2.999 seconds"
        );
        assert!(
            timeline.past_deadline(3.0),
            "the run is not past its 3 second deadline at 3 seconds"
        );
    }

    #[test]
    fn a_run_with_no_script_and_no_deadline_has_nothing_due() {
        assert_eq!(Timeline::default().next_due(), None);
        assert!(
            !Timeline::default().past_deadline(86_400.0),
            "a run with no deadline is past it"
        );
    }

    #[test]
    fn next_due_moves_to_the_following_step_after_a_step_fires() {
        let mut timeline = scripted(&[(1.0, "p"), (2.5, "q")]);
        assert_eq!(timeline.next_due(), Some(1.0));

        timeline.due(1.0);

        assert_eq!(timeline.next_due(), Some(2.5));
    }

    #[test]
    fn next_due_is_the_deadline_when_it_comes_before_the_next_step() {
        let timeline = Timeline::new(vec![(5.0, "p".into())], Some(3.0));
        assert_eq!(timeline.next_due(), Some(3.0));
    }

    #[test]
    fn a_resized_surface_draws_now_whatever_the_schedule_says() {
        assert_eq!(wake(true, 4.0, Some(60.0)), Wake::Now);
        assert_eq!(wake(true, 4.0, None), Wake::Now);
    }

    #[test]
    fn a_screen_that_changes_later_wakes_at_that_second() {
        assert_eq!(wake(false, 4.0, Some(60.0)), Wake::At(60.0));
    }

    #[test]
    fn a_screen_whose_change_is_due_draws_now() {
        assert_eq!(wake(false, 4.0, Some(4.0)), Wake::Now);
        assert_eq!(wake(false, 4.0, Some(3.5)), Wake::Now);
    }

    #[test]
    fn a_distant_frame_sleeps_no_longer_than_the_backstop() {
        assert_eq!(sleep_until(4.0, 60.0), 4.0 + BACKSTOP);
        assert_eq!(sleep_until(4.0, 4.5), 4.5);
    }

    #[test]
    fn a_screen_with_nothing_scheduled_draws_only_on_an_event() {
        assert_eq!(wake(false, 4.0, None), Wake::Never);
        assert_eq!(wake(false, 4.0, Some(f64::INFINITY)), Wake::Never);
    }
}
