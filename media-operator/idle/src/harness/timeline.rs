// The timed decisions of a run, kept out of the code that needs a window:
// which script keys are due, whether the run has reached its deadline, and
// when the loop draws its next frame. Every decision is a function of the
// clock, so a test drives it with numbers and never opens a window.

/// The script and the deadline, with a cursor into the script. The cursor only
/// moves forward, so a step fires once.
#[derive(Debug, Default)]
pub struct Timeline {
    script: Vec<(f64, String)>,
    quit_after: Option<f64>,
    next_step: usize,
}

impl Timeline {
    /// The schedule the flags asked for, at second zero.
    pub fn new(script: Vec<(f64, String)>, quit_after: Option<f64>) -> Self {
        Self {
            script,
            quit_after,
            next_step: 0,
        }
    }

    /// The keys at or before `at`, in the order the script names them. The
    /// cursor moves past every key this call returned, so a later call never
    /// looks back. The harness hands each one to the same call a keyboard
    /// press takes, and that call holds the rule for the key that ends a run.
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

    /// True once the clock reaches the `--quit-after` second.
    pub fn past_deadline(&self, at: f64) -> bool {
        self.quit_after.is_some_and(|limit| at >= limit)
    }

    /// The next second the run itself must catch: the next script key, or the
    /// deadline. The loop folds it into the wake time, so a scripted or
    /// deadlined run sleeps between its seconds instead of drawing every
    /// pass, and a measurement under `--quit-after` reads a paced run.
    pub fn next_due(&self) -> Option<f64> {
        let step = self.script.get(self.next_step).map(|(when, _)| *when);
        [step, self.quit_after]
            .into_iter()
            .flatten()
            .min_by(f64::total_cmp)
    }
}

/// What the loop does until it draws again.
#[derive(Debug, PartialEq)]
pub enum Wake {
    /// Draw now, and take the next pass of the loop as soon as it comes.
    Now,
    /// Draw at this second on the screen's clock, and sleep until then.
    At(f64),
    /// Draw when an event arrives, and on nothing else.
    Never,
}

/// When the loop draws its next frame. `at` is the second of the frame that
/// was drawn last, and `next` is the earliest second anything is due: the
/// screen's own change, the next script key, the next capture, or the
/// deadline, whichever comes first.
///
/// A screen at rest changes on its own schedule, once a minute for a clock
/// that draws no seconds, and a loop that drew at the rate of the display
/// would build sixty identical frames a second for it. `immediate` is the
/// exception: a surface that changed size holds a stale frame, and the loop
/// draws now whatever the schedule says.
pub fn wake(immediate: bool, at: f64, next: Option<f64>) -> Wake {
    if immediate {
        return Wake::Now;
    }
    match next {
        // A second the clock never reaches is a screen with nothing scheduled,
        // and it is also the one value a wake time cannot hold.
        Some(next) if next.is_infinite() => Wake::Never,
        Some(next) if next > at => Wake::At(next),
        Some(_) => Wake::Now,
        None => Wake::Never,
    }
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
    fn every_step_the_clock_has_passed_comes_out_in_order_and_once() {
        let mut timeline = scripted(&[(0.1, "up"), (0.2, "down"), (0.3, "p")]);
        assert_eq!(timeline.due(0.25), keys(&["up", "down"]));
        assert_eq!(timeline.due(0.25), keys(&[]));
        assert_eq!(timeline.due(9.0), keys(&["p"]));
    }

    #[test]
    fn a_run_ends_at_its_deadline() {
        let timeline = Timeline::new(Vec::new(), Some(3.0));
        assert!(!timeline.past_deadline(2.999));
        assert!(timeline.past_deadline(3.0));
    }

    #[test]
    fn a_run_with_no_script_and_no_deadline_has_nothing_due() {
        assert_eq!(Timeline::default().next_due(), None);
        assert!(!Timeline::default().past_deadline(86_400.0));
    }

    #[test]
    fn the_next_step_is_due_until_it_fires_and_then_the_one_after_it() {
        let mut timeline = scripted(&[(1.0, "p"), (2.5, "q")]);
        assert_eq!(timeline.next_due(), Some(1.0));

        timeline.due(1.0);

        assert_eq!(timeline.next_due(), Some(2.5));
    }

    #[test]
    fn the_deadline_is_due_when_it_comes_before_the_next_step() {
        let timeline = Timeline::new(vec![(5.0, "p".into())], Some(3.0));
        assert_eq!(timeline.next_due(), Some(3.0));
    }

    #[test]
    fn a_resized_surface_draws_now_whatever_the_schedule_says() {
        assert_eq!(wake(true, 4.0, Some(60.0)), Wake::Now);
        assert_eq!(wake(true, 4.0, None), Wake::Now);
    }

    #[test]
    fn a_screen_that_changes_later_sleeps_until_then() {
        assert_eq!(wake(false, 4.0, Some(60.0)), Wake::At(60.0));
    }

    #[test]
    fn a_screen_that_has_changed_draws_now() {
        assert_eq!(wake(false, 4.0, Some(4.0)), Wake::Now);
        assert_eq!(wake(false, 4.0, Some(3.5)), Wake::Now);
    }

    #[test]
    fn a_screen_with_nothing_scheduled_waits_for_an_event() {
        assert_eq!(wake(false, 4.0, None), Wake::Never);
        assert_eq!(wake(false, 4.0, Some(f64::INFINITY)), Wake::Never);
    }
}
