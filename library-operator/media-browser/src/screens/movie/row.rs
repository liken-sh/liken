// The button row of a movie's page, and what the audience's progress in
// the film does to it: a film they are in the middle of leads with Resume
// and Start over in the place of Play.

use crate::catalog::Progress;

/// One button of the row. The words are the row's own, so a page draws
/// them and a press reads them from one place.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Button {
    /// Play the film from the beginning.
    Play,
    /// Play the film from the second the audience reached.
    Resume,
    /// Play the film from the beginning, beside Resume.
    StartOver,
    /// Play the film's trailer.
    Trailer,
}

impl Button {
    /// The word the button draws.
    pub fn word(self) -> &'static str {
        match self {
            Self::Play => "Play",
            Self::Resume => "Resume",
            Self::StartOver => "Start over",
            Self::Trailer => "Trailer",
        }
    }
}

/// The row a page draws for this progress: Resume and Start over where the
/// audience is in the middle of the film, and Play where they are not, with
/// the trailer button after either where the movie holds a trailer file. A
/// film they finished takes the Play row, because there is nothing left of
/// it to resume.
pub fn of(progress: Option<&Progress>, trailer: bool) -> &'static [Button] {
    match (resuming(progress), trailer) {
        (true, true) => &[Button::Resume, Button::StartOver, Button::Trailer],
        (true, false) => &[Button::Resume, Button::StartOver],
        (false, true) => &[Button::Play, Button::Trailer],
        (false, false) => &[Button::Play],
    }
}

/// Whether the audience is in the middle of the film: progress the store
/// holds that is not finished.
pub fn resuming(progress: Option<&Progress>) -> bool {
    progress.is_some_and(|progress| !progress.finished)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn progress(finished: bool) -> Progress {
        Progress {
            position: 2_912,
            duration: 6_730,
            finished,
            ..Progress::default()
        }
    }

    #[test]
    fn a_film_no_play_names_leads_with_play() {
        assert_eq!(of(None, false), [Button::Play]);
        assert_eq!(of(None, true), [Button::Play, Button::Trailer]);
    }

    #[test]
    fn a_film_the_audience_is_in_the_middle_of_leads_with_resume() {
        assert_eq!(
            of(Some(&progress(false)), false),
            [Button::Resume, Button::StartOver]
        );
        assert_eq!(
            of(Some(&progress(false)), true),
            [Button::Resume, Button::StartOver, Button::Trailer]
        );
    }

    #[test]
    fn a_film_the_audience_finished_leads_with_play() {
        assert_eq!(
            of(Some(&progress(true)), true),
            [Button::Play, Button::Trailer]
        );
        assert!(!resuming(Some(&progress(true))));
    }

    #[test]
    fn every_button_carries_its_own_word() {
        let words: Vec<&str> = of(Some(&progress(false)), true)
            .iter()
            .map(|button| button.word())
            .collect();
        assert_eq!(words, ["Resume", "Start over", "Trailer"]);
    }
}
