// The state a page is in between the select that asks for a film and the
// film that covers the page. It is a pure function of the clock: the second
// it was entered, and the second the exit began. The line it names the work
// by is read once, where the state is entered, so no frame reads the
// catalog.

use super::{Screen, episode_caption};
use crate::catalog::{Selection, Source};
use crate::look;
use crate::views::curtain::Curtain;

/// The loading state one page is in, as a function of the clock.
#[derive(Debug, Clone, PartialEq)]
pub struct Loading {
    // The second the press entered the state.
    entered: f64,
    // The exit, once it has begun.
    left: Option<Exit>,
    // The work that starts, in the words the curtain draws under the mark,
    // and empty where the page's logo names that work already.
    line: String,
}

// The exit: the second it ends, and how far away the page stood when it
// began, so the motion runs back from there.
#[derive(Debug, Clone, Copy, PartialEq)]
struct Exit {
    until: f64,
    from: f32,
}

impl Loading {
    /// The state a press enters at this second, naming the work that
    /// starts by this line, which is empty where the page names it.
    pub fn entered(at: f64, line: String) -> Self {
        Self {
            entered: at,
            left: None,
            line,
        }
    }

    /// Name another work under the mark, on the same clock.
    pub fn rename(&mut self, line: String) {
        self.line = line;
    }

    /// Start the exit at this second. A state that is already leaving keeps
    /// the exit it runs, so a second ask does not restart it.
    pub fn leave(&mut self, at: f64) {
        if self.left.is_some() {
            return;
        }
        self.left = Some(Exit {
            until: at + look::RETURN,
            from: self.away(at),
        });
    }

    /// Whether the exit has begun.
    pub fn leaving(&self) -> bool {
        self.left.is_some()
    }

    /// How far the page has gone, from 0 whole to 1 fully away.
    pub fn away(&self, at: f64) -> f32 {
        match self.left {
            None => look::eased(look::share(at - self.entered, look::DEPARTURE)),
            // The share left is measured from the second the exit ends
            // and not from the second it began, so the last frame of the
            // exit lands on exactly zero.
            Some(exit) => exit.from * look::share(exit.until - at, look::RETURN),
        }
    }

    /// Whether the exit has run its length. The browser then drops the
    /// state and the page is whole again.
    pub fn done(&self, at: f64) -> bool {
        matches!(self.left, Some(exit) if at >= exit.until)
    }

    /// What one frame draws at this second.
    pub fn curtain(&self, at: f64) -> Curtain<'_> {
        Curtain {
            away: self.away(at),
            phase: at,
            line: &self.line,
        }
    }
}

/// The line that names the work a selection starts, for the curtain over
/// this page, and empty where the page's own logo or centred name already
/// names that work.
///
/// A page's curtain draws that page's logo, so the logo names the work
/// only where the page is the work's own. The person who takes the
/// up-next offer stays on the page the last play started from, so the next
/// episode starts under its series' logo and the next film of a set under
/// the logo of the film before it. An episode is named the way the offer's
/// card names it, so the card the person took and the wait that follows it
/// read the same.
pub fn line(
    page: &Screen,
    source: &mut dyn Source,
    library: &str,
    selection: &Selection,
) -> String {
    if names(page, library, selection) {
        return String::new();
    }
    match selection {
        Selection::Episode {
            series,
            season,
            episode,
        } => source
            .episodes(library, series)
            .into_iter()
            .find(|row| (row.season, row.episode) == (*season, *episode))
            .map(|row| episode_caption(row.episode, &row.title))
            .unwrap_or_default(),
        Selection::Movie { id } | Selection::Trailer { id } => source
            .movie(library, id)
            .map(|details| details.title)
            .unwrap_or_default(),
    }
}

// Whether the logo this page's curtain draws names the work a selection
// starts: a film or its trailer on the film's own page, and a series'
// trailer on the series' page. An episode is never the work a series page
// names, because the page's logo is the series'.
fn names(page: &Screen, library: &str, selection: &Selection) -> bool {
    match (page, selection) {
        (Screen::Movie(movie), Selection::Movie { id } | Selection::Trailer { id }) => {
            movie.library == library && movie.id == *id
        }
        (Screen::Series(series), Selection::Trailer { id }) => {
            series.library == library && series.id == *id
        }
        _ => false,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    // The second the press lands on, which every case measures from.
    const PRESS: f64 = 3.0;

    #[test]
    fn the_press_enters_the_state_with_the_page_whole() {
        let state = Loading::entered(PRESS, String::new());
        assert_eq!(state.away(PRESS), 0.0);
        assert!(!state.leaving());
        assert!(!state.done(PRESS));
    }

    #[test]
    fn the_page_is_fully_away_after_the_departure() {
        let state = Loading::entered(PRESS, String::new());
        assert_eq!(state.away(PRESS + look::DEPARTURE), 1.0);
    }

    #[test]
    fn the_page_leaves_without_going_back() {
        let state = Loading::entered(PRESS, String::new());
        let mut last = 0.0;
        for step in 1..=10 {
            let away = state.away(PRESS + look::DEPARTURE * f64::from(step) / 10.0);
            assert!(away > last, "{away} at step {step}");
            last = away;
        }
    }

    #[test]
    fn the_state_holds_with_no_ceiling() {
        let state = Loading::entered(PRESS, String::new());
        for held in [1.0, 60.0, 3_600.0] {
            assert_eq!(state.away(PRESS + look::DEPARTURE + held), 1.0);
            assert!(!state.done(PRESS + look::DEPARTURE + held));
        }
    }

    #[test]
    fn the_exit_runs_the_page_back_and_ends() {
        let held = PRESS + 10.0;
        let mut state = Loading::entered(PRESS, String::new());
        state.leave(held);

        assert!(state.leaving());
        assert_eq!(state.away(held), 1.0);
        assert_eq!(state.away(held + look::RETURN / 2.0), 0.5);
        assert_eq!(state.away(held + look::RETURN), 0.0);
        assert!(state.done(held + look::RETURN));
    }

    #[test]
    fn an_exit_before_the_page_is_away_runs_back_from_where_it_stood() {
        let part = PRESS + look::DEPARTURE / 2.0;
        let mut state = Loading::entered(PRESS, String::new());
        let stood = state.away(part);
        state.leave(part);

        assert_eq!(state.away(part), stood);
        assert_eq!(state.away(part + look::RETURN), 0.0);
    }

    #[test]
    fn a_second_ask_to_leave_does_not_restart_the_exit() {
        let held = PRESS + 10.0;
        let mut state = Loading::entered(PRESS, String::new());
        state.leave(held);
        state.leave(held + look::RETURN / 2.0);

        assert!(state.done(held + look::RETURN));
    }

    #[test]
    fn the_curtain_carries_the_clock_the_mark_pulses_on() {
        let state = Loading::entered(PRESS, String::new());
        let curtain = state.curtain(PRESS + look::DEPARTURE);
        assert_eq!(curtain.away, 1.0);
        assert_eq!(curtain.phase, PRESS + look::DEPARTURE);
    }

    #[test]
    fn the_curtain_carries_the_line_the_state_was_entered_with() {
        let state = Loading::entered(PRESS, "E07 · A Name".into());
        assert_eq!(state.curtain(PRESS).line, "E07 · A Name");
    }
}
