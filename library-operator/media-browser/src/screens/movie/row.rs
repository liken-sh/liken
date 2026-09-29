// The button row of a movie's page, and what the audience's progress in
// the film does to it: a film they are in the middle of leads with Resume
// and Start over in the place of Play. The two marks close the row, and the
// series page draws the same row for one episode.

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
    /// Mark the work watched for everyone at the screen.
    MarkWatched,
    /// Clear the work's progress for everyone at the screen.
    ClearProgress,
}

impl Button {
    /// The word the button draws.
    pub fn word(self) -> &'static str {
        match self {
            Self::Play => "Play",
            Self::Resume => "Resume",
            Self::StartOver => "Start over",
            Self::Trailer => "Trailer",
            Self::MarkWatched => "Mark watched",
            Self::ClearProgress => "Clear progress",
        }
    }
}

/// The row a page draws for this progress: Resume and Start over where the
/// audience is in the middle of the work, and Play where they are not, with
/// the trailer button after either where the work holds a trailer file. A
/// work they finished takes the Play row, because there is nothing left of
/// it to resume.
///
/// The marks come last, so the buttons that play keep their places. Mark
/// watched stands on a work they have not finished, and Clear progress on
/// a work they started. A cleared work is not started, so the two marks
/// undo each other.
pub fn of(progress: Option<&Progress>, trailer: bool) -> Vec<Button> {
    let mut row = match resuming(progress) {
        true => vec![Button::Resume, Button::StartOver],
        false => vec![Button::Play],
    };
    if trailer {
        row.push(Button::Trailer);
    }
    if !progress.is_some_and(|progress| progress.finished) {
        row.push(Button::MarkWatched);
    }
    if started(progress) {
        row.push(Button::ClearProgress);
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
    fn a_film_no_play_names_leads_with_play_and_offers_the_watched_mark() {
        assert_eq!(of(None, false), [Button::Play, Button::MarkWatched]);
        assert_eq!(
            of(None, true),
            [Button::Play, Button::Trailer, Button::MarkWatched]
        );
    }

    #[test]
    fn a_film_the_audience_is_in_the_middle_of_leads_with_resume_and_offers_both_marks() {
        assert_eq!(
            of(Some(&progress(false)), false),
            [
                Button::Resume,
                Button::StartOver,
                Button::MarkWatched,
                Button::ClearProgress
            ]
        );
        assert_eq!(
            of(Some(&progress(false)), true),
            [
                Button::Resume,
                Button::StartOver,
                Button::Trailer,
                Button::MarkWatched,
                Button::ClearProgress
            ]
        );
    }

    #[test]
    fn a_film_the_audience_finished_leads_with_play_and_offers_the_clear() {
        assert_eq!(
            of(Some(&progress(true)), true),
            [Button::Play, Button::Trailer, Button::ClearProgress]
        );
        assert!(!resuming(Some(&progress(true))));
    }

    #[test]
    fn a_play_at_zero_reads_as_not_started() {
        let cleared = Progress {
            duration: 6_730,
            ..Progress::default()
        };
        assert_eq!(
            of(Some(&cleared), false),
            [Button::Play, Button::MarkWatched]
        );
        assert!(!started(Some(&cleared)));
    }

    #[test]
    fn every_button_carries_its_own_word() {
        let words: Vec<&str> = of(Some(&progress(false)), true)
            .iter()
            .map(|button| button.word())
            .collect();
        assert_eq!(
            words,
            [
                "Resume",
                "Start over",
                "Trailer",
                "Mark watched",
                "Clear progress"
            ]
        );
    }
}
