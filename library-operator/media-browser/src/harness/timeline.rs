// The timed decisions of a run, kept out of the code that needs a window:
// which script keys are due, whether a capture is due, and whether the run
// is over. Every decision is a function of the clock, so a test drives it
// with numbers and never opens a window.

use std::path::PathBuf;

use super::QUIT;

/// The script steps whose time has come, and whether one of them ended the run.
#[derive(Debug, Default, PartialEq)]
pub struct Due {
    pub keys: Vec<String>,
    pub quit: bool,
}

/// The script, the capture times, the deadline, and a cursor into each of the
/// two lists. The cursors only move forward, so a step or a capture fires once.
#[derive(Debug, Default)]
pub struct Timeline {
    script: Vec<(f64, String)>,
    capture_dir: Option<PathBuf>,
    capture_at: Vec<f64>,
    quit_after: Option<f64>,
    next_script: usize,
    next_capture: usize,
}

impl Timeline {
    /// The schedule the flags asked for, at second zero.
    pub fn new(
        script: Vec<(f64, String)>,
        capture_dir: Option<PathBuf>,
        capture_at: Vec<f64>,
        quit_after: Option<f64>,
    ) -> Self {
        Self {
            script,
            capture_dir,
            capture_at,
            quit_after,
            next_script: 0,
            next_capture: 0,
        }
    }

    /// The steps at or before `at`, in order. The batch stops at the quit key,
    /// and the cursor moves past every step it returned, so a later call never
    /// looks back.
    pub fn due(&mut self, at: f64) -> Due {
        let mut due = Due::default();

        while let Some((when, key)) = self.script.get(self.next_script)
            && *when <= at
        {
            let key = key.clone();
            self.next_script += 1;
            if key == QUIT {
                due.quit = true;
                break;
            }
            due.keys.push(key);
        }

        due
    }

    /// The path this frame should be written to, if the frame is the first one
    /// at or after the next capture time.
    pub fn due_capture(&mut self, at: f64) -> Option<PathBuf> {
        let dir = self.capture_dir.as_ref()?;
        let when = *self.capture_at.get(self.next_capture)?;
        if at < when {
            return None;
        }
        self.next_capture += 1;
        Some(dir.join(format!("{when:06.2}.png")))
    }

    /// True once the clock reaches the `--quit-after` second.
    pub fn past_deadline(&self, at: f64) -> bool {
        self.quit_after.is_some_and(|limit| at >= limit)
    }

    /// True once the run has taken its last capture or reached its deadline.
    pub fn ended(&self, at: f64) -> bool {
        self.past_captures() || self.past_deadline(at)
    }

    fn past_captures(&self) -> bool {
        self.capture_dir.is_some()
            && !self.capture_at.is_empty()
            && self.next_capture >= self.capture_at.len()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn scripted(steps: &[(f64, &str)]) -> Timeline {
        Timeline {
            script: steps
                .iter()
                .map(|(at, key)| (*at, key.to_string()))
                .collect(),
            ..Default::default()
        }
    }

    fn capturing(dir: &str, capture_at: Vec<f64>) -> Timeline {
        Timeline {
            capture_dir: Some(PathBuf::from(dir)),
            capture_at,
            ..Default::default()
        }
    }

    fn keys(names: &[&str]) -> Due {
        Due {
            keys: names.iter().map(|name| name.to_string()).collect(),
            quit: false,
        }
    }

    #[test]
    fn a_step_is_due_at_its_second_and_not_before() {
        let mut timeline = scripted(&[(1.0, "p")]);
        assert_eq!(timeline.due(0.999), Due::default());
        assert_eq!(timeline.due(1.0), keys(&["p"]));
    }

    #[test]
    fn every_step_the_clock_has_passed_comes_out_in_order_and_once() {
        let mut timeline = scripted(&[(0.1, "up"), (0.2, "down"), (0.3, "p")]);
        assert_eq!(timeline.due(0.25), keys(&["up", "down"]));
        assert_eq!(timeline.due(0.25), Due::default());
        assert_eq!(timeline.due(9.0), keys(&["p"]));
    }

    #[test]
    fn the_quit_key_ends_the_batch_and_the_run() {
        let mut timeline = scripted(&[(0.1, "up"), (0.2, "q"), (0.3, "p")]);
        assert_eq!(
            timeline.due(9.0),
            Due {
                keys: vec!["up".to_string()],
                quit: true,
            }
        );
    }

    #[test]
    fn a_capture_is_due_on_the_first_frame_at_or_after_its_second() {
        let mut timeline = capturing("/frames", vec![0.5]);
        assert_eq!(timeline.due_capture(0.49), None);
        assert_eq!(
            timeline.due_capture(0.52),
            Some(PathBuf::from("/frames/000.50.png"))
        );
        assert_eq!(timeline.due_capture(0.53), None);
    }

    #[test]
    fn a_capture_is_named_for_the_second_it_was_asked_for() {
        let mut timeline = capturing("/frames", vec![12.25, 100.0]);
        assert_eq!(
            timeline.due_capture(20.0),
            Some(PathBuf::from("/frames/012.25.png"))
        );
        assert_eq!(
            timeline.due_capture(200.0),
            Some(PathBuf::from("/frames/100.00.png"))
        );
    }

    #[test]
    fn capture_seconds_without_a_directory_capture_nothing() {
        let mut timeline = Timeline::new(Vec::new(), None, vec![0.5], None);
        assert_eq!(timeline.due_capture(1.0), None);
        assert!(!timeline.ended(1.0));
    }

    #[test]
    fn a_run_with_no_captures_and_no_deadline_never_ends() {
        assert!(!Timeline::default().ended(86_400.0));
    }

    #[test]
    fn a_run_ends_after_its_last_capture() {
        let mut timeline = capturing("/frames", vec![0.5, 1.5]);
        assert!(!timeline.ended(0.0));
        timeline.due_capture(0.5);
        assert!(!timeline.ended(0.5));
        timeline.due_capture(1.5);
        assert!(timeline.ended(1.5));
    }

    #[test]
    fn a_run_ends_at_its_deadline() {
        let timeline = Timeline::new(Vec::new(), None, Vec::new(), Some(3.0));
        assert!(!timeline.past_deadline(2.999));
        assert!(timeline.past_deadline(3.0));
        assert!(timeline.ended(3.0));
    }
}
