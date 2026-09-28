// The refresh policy: the one place that decides when the browser reads
// the catalog again. Three inputs reach it and nothing else orders a
// read: what changed, whether the screen shows anything, and the clock.
// Every read the browser starts on its own goes through here, so a
// source that marks a change every second cannot order a read every
// second.

use crate::catalog::Change;

// How long progress rows must be quiet before the read runs. A film
// writes a row about once a second, and a position that moved by one
// second changes nothing a person can see on the home page.
const QUIET: f64 = 2.0;

// The most often a stream of progress rows earns a read. A film writes
// rows for two hours, every read is a full read of the screen, and a
// continue-watching row ten seconds behind is current enough.
const INTERVAL: f64 = 10.0;

// The screen state the browser was told, and the one change held for
// the next read.
#[derive(Debug, Default)]
pub(super) struct Refresh {
    // The two things that hide the frame. A film over the surface and
    // the shade over the screen reach the same person, who sees nothing
    // either way, so a read under either one draws nothing.
    covered: bool,
    asleep: bool,
    // One hold and not one per cause, because one read answers every
    // change that arrived before it ran.
    held: Option<Hold>,
}

// What waits to be read: the second the read is due, and the second the
// oldest change in it arrived, which the interval is measured from.
#[derive(Debug, Clone, Copy)]
struct Hold {
    since: f64,
    due: f64,
}

impl Refresh {
    // Whether a film covers the surface, from the Player's status.
    pub(super) fn cover(&mut self, covered: bool) {
        self.covered = covered;
    }

    pub(super) fn covered(&self) -> bool {
        self.covered
    }

    // Whether the shade is down, from the crate's sleep and wake.
    pub(super) fn shade(&mut self, asleep: bool) {
        self.asleep = asleep;
    }

    pub(super) fn asleep(&self) -> bool {
        self.asleep
    }

    // Whether the frame reaches anybody.
    pub(super) fn shown(&self) -> bool {
        !self.covered && !self.asleep
    }

    // Take what the source says changed. A catalog change is due at
    // once. Progress rows move the due second out as they arrive, and
    // the interval caps how far, so a stream that never goes quiet still
    // earns a read.
    pub(super) fn changed(&mut self, change: Change, at: f64) {
        if change == Change::None {
            return;
        }
        let since = self.held.map_or(at, |hold| hold.since);
        let due = match change.catalog() {
            true => at,
            false => (at + QUIET).min(since + INTERVAL),
        };
        self.held = Some(Hold { since, due });
    }

    // The one answer: read now, or not. A hidden screen answers no
    // whatever is held, so the read runs on the first ask after the
    // screen shows again, and the held second is already past by then.
    pub(super) fn due(&mut self, at: f64) -> bool {
        if !self.shown() {
            return false;
        }
        let Some(hold) = self.held else {
            return false;
        };
        if at < hold.due {
            return false;
        }
        self.held = None;
        true
    }

    // The second the held read comes due on a shown screen, so the loop
    // wakes for it. The loop sleeps until the next second a screen names,
    // and without this one a coalesced read would wait for the minute.
    // A hidden screen names none, because the lift wakes the loop itself.
    pub(super) fn next_due(&self) -> Option<f64> {
        self.shown()
            .then(|| self.held.map(|hold| hold.due))
            .flatten()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    // The screen states a case names.
    #[derive(Debug, Clone, Copy)]
    enum State {
        Shown,
        Covered,
        Asleep,
    }

    // A policy in one state, so a case states the screen and the change
    // and reads the answer.
    fn policy(state: State) -> Refresh {
        let mut refresh = Refresh::default();
        match state {
            State::Shown => {}
            State::Covered => refresh.cover(true),
            State::Asleep => refresh.shade(true),
        }
        refresh
    }

    // One change at second zero, then one ask at the second the case
    // names, over every screen state and every kind of change.
    #[test]
    fn one_change_reads_at_the_second_its_kind_and_the_screen_name() {
        let cases = [
            (State::Shown, Change::Catalog, 0.0, true),
            (State::Shown, Change::Both, 0.0, true),
            (State::Shown, Change::None, 0.0, false),
            (State::Shown, Change::Progress, 0.0, false),
            (State::Shown, Change::Progress, 1.9, false),
            (State::Shown, Change::Progress, 2.0, true),
            (State::Covered, Change::Catalog, 0.0, false),
            (State::Covered, Change::Catalog, 3600.0, false),
            (State::Covered, Change::Progress, 3600.0, false),
            (State::Asleep, Change::Catalog, 3600.0, false),
            (State::Asleep, Change::Progress, 3600.0, false),
        ];

        for (state, change, at, expected) in cases {
            let mut refresh = policy(state);
            refresh.changed(change, 0.0);

            assert_eq!(refresh.due(at), expected, "{state:?} {change:?} at {at}");
        }
    }

    // One read answers everything held, so a second ask reads nothing
    // until something changes again.
    #[test]
    fn the_read_answers_everything_held() {
        let mut refresh = Refresh::default();
        refresh.changed(Change::Progress, 0.0);
        refresh.changed(Change::Catalog, 1.0);

        assert!(refresh.due(1.0));
        assert!(!refresh.due(60.0));
    }

    // Rows that keep arriving move the due second out, so the read holds
    // while a film writes.
    #[test]
    fn progress_rows_that_keep_arriving_hold_the_read() {
        let mut refresh = Refresh::default();

        for second in 0..8 {
            let at = f64::from(second);
            refresh.changed(Change::Progress, at);
            assert!(!refresh.due(at), "second {at}");
        }
    }

    // The interval caps that hold, so a film that never goes quiet still
    // earns one read every ten seconds.
    #[test]
    fn a_stream_that_never_goes_quiet_reads_once_an_interval() {
        let mut refresh = Refresh::default();
        let mut reads = Vec::new();

        for second in 0..=30 {
            let at = f64::from(second);
            refresh.changed(Change::Progress, at);
            if refresh.due(at) {
                reads.push(at);
            }
        }

        assert_eq!(reads, [10.0, 21.0]);
    }

    // A shown screen names the second its held read comes due, and a
    // hidden one names nothing.
    #[test]
    fn the_held_read_names_its_second_only_on_a_shown_screen() {
        let cases = [
            (State::Shown, Change::None, None),
            (State::Shown, Change::Catalog, Some(0.0)),
            (State::Shown, Change::Progress, Some(2.0)),
            (State::Covered, Change::Catalog, None),
            (State::Asleep, Change::Progress, None),
        ];

        for (state, change, expected) in cases {
            let mut refresh = policy(state);
            refresh.changed(change, 0.0);

            assert_eq!(refresh.next_due(), expected, "{state:?} {change:?}");
        }
    }

    // A change held under a film or the shade is read on the first ask
    // after the screen shows again, and it is one read.
    #[test]
    fn the_change_held_while_hidden_reads_when_the_screen_shows_again() {
        let mut refresh = Refresh::default();
        refresh.cover(true);
        refresh.changed(Change::Progress, 1.0);
        refresh.changed(Change::Catalog, 2.0);
        assert!(!refresh.due(600.0));

        refresh.cover(false);

        assert!(refresh.due(600.0));
        assert!(!refresh.due(601.0));
    }

    // A screen hidden by both waits for both, so a film that ends under
    // the shade reads nothing.
    #[test]
    fn a_screen_the_film_leaves_under_the_shade_is_still_hidden() {
        let mut refresh = Refresh::default();
        refresh.shade(true);
        refresh.cover(true);
        refresh.changed(Change::Catalog, 1.0);

        refresh.cover(false);

        assert!(!refresh.covered());
        assert!(refresh.asleep());
        assert!(!refresh.shown());
        assert!(!refresh.due(2.0));
    }
}
