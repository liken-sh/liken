// The refresh policy: the one place that decides when the browser reads
// a whole screen again. Two inputs reach it: what changed, and whether
// the screen shows anything. A change on a shown screen is due at once,
// and a change on a hidden one is held until the screen shows again,
// because a read under a film or the shade draws nothing.
//
// A progress change a play names does not come here. The browser matches
// it against what the screen draws and reads that work alone (`live.rs`).
// The progress change held here is one that names no play: a stream that
// dropped, or a play that changed while the screen was hidden.

use crate::catalog::Change;

// The screen state the browser was told, and the change held for the next
// read.
#[derive(Debug, Default)]
pub(super) struct Refresh {
    // The two things that hide the frame. A film over the surface and
    // the shade over the screen reach the same person, who sees nothing
    // either way, so a read under either one draws nothing.
    covered: bool,
    asleep: bool,
    // One hold and not one per cause, because one read answers every
    // change that arrived before it ran.
    held: Change,
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

    // Take what the source says changed.
    pub(super) fn changed(&mut self, change: Change) {
        self.held = self.held.and(change);
    }

    // What to read now. A hidden screen answers nothing, whatever is held,
    // so the read runs on the first ask after the screen shows again.
    pub(super) fn due(&mut self) -> Change {
        if !self.shown() {
            return Change::None;
        }
        std::mem::take(&mut self.held)
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

    // One change, then one ask, over every screen state and every kind of
    // change.
    #[test]
    fn a_change_reads_at_once_only_on_a_shown_screen() {
        let cases = [
            (State::Shown, Change::Catalog, Change::Catalog),
            (State::Shown, Change::Both, Change::Both),
            (State::Shown, Change::None, Change::None),
            (State::Shown, Change::Progress, Change::Progress),
            (State::Covered, Change::Catalog, Change::None),
            (State::Covered, Change::Progress, Change::None),
            (State::Asleep, Change::Catalog, Change::None),
            (State::Asleep, Change::Progress, Change::None),
        ];

        for (state, change, expected) in cases {
            let mut refresh = policy(state);
            refresh.changed(change);

            assert_eq!(refresh.due(), expected, "{state:?} {change:?}");
        }
    }

    // One read answers everything held, so a second ask reads nothing
    // until something changes again.
    #[test]
    fn the_read_answers_everything_held() {
        let mut refresh = Refresh::default();
        refresh.changed(Change::Progress);
        refresh.changed(Change::Catalog);

        assert_eq!(refresh.due(), Change::Both);
        assert_eq!(refresh.due(), Change::None);
    }

    // A change held under a film or the shade is read on the first ask
    // after the screen shows again, and it is one read.
    #[test]
    fn the_change_held_while_hidden_reads_when_the_screen_shows_again() {
        let mut refresh = Refresh::default();
        refresh.cover(true);
        refresh.changed(Change::Progress);
        refresh.changed(Change::Catalog);
        assert_eq!(refresh.due(), Change::None);

        refresh.cover(false);

        assert_eq!(refresh.due(), Change::Both);
        assert_eq!(refresh.due(), Change::None);
    }

    // A screen hidden by both waits for both, so a film that ends under
    // the shade reads nothing.
    #[test]
    fn a_screen_the_film_leaves_under_the_shade_is_still_hidden() {
        let mut refresh = Refresh::default();
        refresh.shade(true);
        refresh.cover(true);
        refresh.changed(Change::Catalog);

        refresh.cover(false);

        assert!(!refresh.covered());
        assert!(refresh.asleep());
        assert!(!refresh.shown());
        assert_eq!(refresh.due(), Change::None);
    }
}
