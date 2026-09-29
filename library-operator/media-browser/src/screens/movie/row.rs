// The playback row of a movie's page, and what the audience's progress in
// the film does to it: a film they are in the middle of leads with Resume
// and Start over in the place of Play. Every button of the row starts a
// play. The marks are on the status line under the row, and the series
// page draws the same row and line for one episode.

use crate::catalog::Progress;
use crate::views::icon::Icon;

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

    /// The glyph the button draws before its words. Play and Resume both
    /// play the work on from a point, so they share the triangle.
    pub fn icon(self) -> Icon {
        match self {
            Self::Play | Self::Resume => Icon::Play,
            Self::StartOver => Icon::Start,
            Self::Trailer => Icon::Camera,
        }
    }

    /// The buttons as the row draws them, each with its glyph.
    pub fn drawn(row: &[Self]) -> Vec<(Icon, &'static str)> {
        row.iter()
            .map(|button| (button.icon(), button.word()))
            .collect()
    }
}

/// The row a page draws for this progress: Resume and Start over where the
/// audience is in the middle of the work, and Play where they are not, with
/// the trailer button after either where the work holds a trailer file. A
/// work they finished takes the Play row, because there is nothing left of
/// it to resume.
pub fn of(progress: Option<&Progress>, trailer: bool) -> Vec<Button> {
    let mut row = match resuming(progress) {
        true => vec![Button::Resume, Button::StartOver],
        false => vec![Button::Play],
    };
    if trailer {
        row.push(Button::Trailer);
    }
    row
}

/// Whether the audience is in the middle of the work: progress the store
/// holds that is not finished, past the first second. A play at 0 is not
/// started, which is the thread rule's reading too.
pub fn resuming(progress: Option<&Progress>) -> bool {
    progress.is_some_and(|progress| !progress.finished && progress.position > 0)
}

/// Whether the audience started the work: they are in the middle of it,
/// or they finished it.
pub fn started(progress: Option<&Progress>) -> bool {
    progress.is_some_and(|progress| progress.finished) || resuming(progress)
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
    fn a_film_the_audience_is_in_the_middle_of_leads_with_resume_and_start_over() {
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
        assert!(started(Some(&progress(true))));
    }

    #[test]
    fn a_play_at_zero_reads_as_not_started() {
        let cleared = Progress {
            duration: 6_730,
            ..Progress::default()
        };
        assert_eq!(of(Some(&cleared), false), [Button::Play]);
        assert!(!started(Some(&cleared)));
    }

    #[test]
    fn every_button_carries_its_own_word_and_glyph() {
        assert_eq!(
            [
                Button::Play,
                Button::Resume,
                Button::StartOver,
                Button::Trailer,
            ]
            .map(|button| (button.word(), button.icon())),
            [
                ("Play", Icon::Play),
                ("Resume", Icon::Play),
                ("Start over", Icon::Start),
                ("Trailer", Icon::Camera),
            ]
        );
        assert_eq!(
            Button::drawn(&[Button::StartOver]),
            [(Icon::Start, "Start over")]
        );
    }
}
