// The line under a page's playback row: where the audience stands in the
// work, and the two marks that change it. The playback row holds only the
// buttons that start a play, so a press there always plays something. The
// marks are bookkeeping, and they draw on this line beside the status
// they change. A movie's page and an episode's row draw the same line.

use super::row;
use crate::catalog::Progress;
use crate::catalog::progress;
use crate::screens::TitleMark;
use crate::screens::facts;
use crate::views::watch::Icon;

/// One mark on the status line. Each one applies to everyone at the
/// screen, at once.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Mark {
    /// Mark the work watched.
    Watched,
    /// Clear the work's progress.
    Cleared,
}

impl Mark {
    /// The word the mark draws.
    pub fn word(self) -> &'static str {
        match self {
            Self::Watched => "Mark watched",
            Self::Cleared => "Clear progress",
        }
    }

    /// The glyph the mark draws before its words.
    pub fn icon(self) -> Icon {
        match self {
            Self::Watched => Icon::Check,
            Self::Cleared => Icon::Undo,
        }
    }

    /// The marks as the status line draws them, each with its glyph.
    pub fn drawn(marks: &[Self]) -> Vec<(Icon, &'static str)> {
        marks
            .iter()
            .map(|mark| (mark.icon(), mark.word()))
            .collect()
    }

    /// The mark the bus message carries.
    pub fn title(self) -> TitleMark {
        match self {
            Self::Watched => TitleMark::Watched,
            Self::Cleared => TitleMark::Cleared,
        }
    }
}

/// The marks the line offers for this progress. Mark watched stands on a
/// work the audience has not finished, and Clear progress on a work they
/// started. A cleared work is not started, so the two marks undo each
/// other. Every work offers at least one of the two.
pub fn marks(progress: Option<&Progress>) -> Vec<Mark> {
    let mut marks = Vec::with_capacity(2);
    if !progress.is_some_and(|progress| progress.finished) {
        marks.push(Mark::Watched);
    }
    if row::started(progress) {
        marks.push(Mark::Cleared);
    }
    marks
}

/// What the line says about where the audience stands.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Status {
    /// The words of the line.
    pub words: String,
    /// Whether the audience finished the work, which draws a check
    /// before the words.
    pub finished: bool,
}

/// The status for this progress, in a work of this duration in seconds.
/// The duration is the one a mark states: the play's own where it names
/// one, and the catalog's running time where it does not. A part the
/// duration cannot give, such as the time left in a work of unknown
/// length, leaves the line with no gap and no separator.
pub fn status(progress: Option<&Progress>, duration: i64) -> Status {
    let words = match progress {
        Some(progress) if progress.finished => {
            return Status {
                words: "Watched".to_string(),
                finished: true,
            };
        }
        Some(progress) if row::resuming(Some(progress)) => {
            let remaining = facts::runtime(duration - progress.position);
            facts::joined(&[
                &format!(
                    "{} watched",
                    progress::position(progress.position, duration)
                ),
                &match remaining.is_empty() {
                    true => String::new(),
                    false => format!("{remaining} remaining"),
                },
            ])
        }
        _ => facts::joined(&["Not started", &facts::runtime(duration)]),
    };
    Status {
        words,
        finished: false,
    }
}

/// The share of the work the bar under the playback row draws: none on a
/// work the audience has not started, the whole bar on one they finished,
/// and the share they reached on one they are in the middle of.
pub fn share(progress: Option<&Progress>) -> f32 {
    match progress {
        Some(progress) if progress.finished => 1.0,
        Some(progress) if row::resuming(Some(progress)) => progress.played().fraction(),
        _ => 0.0,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    // The film of these cases: 1h 39m 42s long.
    const RUNTIME: i64 = 5_982;

    fn reached(position: i64, duration: i64, finished: bool) -> Progress {
        Progress {
            position,
            duration,
            finished,
            ..Progress::default()
        }
    }

    #[test]
    fn a_work_they_have_not_started_offers_the_watched_mark_alone() {
        assert_eq!(marks(None), [Mark::Watched]);
        assert_eq!(marks(Some(&reached(0, RUNTIME, false))), [Mark::Watched]);
    }

    #[test]
    fn a_work_they_are_in_the_middle_of_offers_both_marks() {
        assert_eq!(
            marks(Some(&reached(787, RUNTIME, false))),
            [Mark::Watched, Mark::Cleared]
        );
    }

    #[test]
    fn a_work_they_finished_offers_the_clear_alone() {
        assert_eq!(
            marks(Some(&reached(RUNTIME, RUNTIME, true))),
            [Mark::Cleared]
        );
    }

    #[test]
    fn every_mark_carries_its_own_word_glyph_and_bus_mark() {
        assert_eq!(
            [Mark::Watched, Mark::Cleared].map(|mark| (mark.word(), mark.icon(), mark.title())),
            [
                ("Mark watched", Icon::Check, TitleMark::Watched),
                ("Clear progress", Icon::Undo, TitleMark::Cleared)
            ]
        );
        assert_eq!(
            Mark::drawn(&[Mark::Cleared]),
            [(Icon::Undo, "Clear progress")]
        );
    }

    #[test]
    fn the_status_says_where_the_audience_stands() {
        let cases = [
            (None, RUNTIME, "Not started · 1h 39m", false),
            (
                Some(reached(0, RUNTIME, false)),
                RUNTIME,
                "Not started · 1h 39m",
                false,
            ),
            (
                Some(reached(787, RUNTIME, false)),
                RUNTIME,
                "0:13:07 watched · 1h 26m remaining",
                false,
            ),
            (
                Some(reached(1_300, 2_880, false)),
                2_880,
                "21:40 watched · 26m remaining",
                false,
            ),
            (
                Some(reached(RUNTIME, RUNTIME, true)),
                RUNTIME,
                "Watched",
                true,
            ),
        ];
        for (progress, duration, words, finished) in cases {
            assert_eq!(
                status(progress.as_ref(), duration),
                Status {
                    words: words.to_string(),
                    finished
                }
            );
        }
    }

    #[test]
    fn a_duration_the_catalog_does_not_hold_leaves_its_part_off_the_line() {
        assert_eq!(status(None, 0).words, "Not started");
        assert_eq!(
            status(Some(&reached(787, 0, false)), 0).words,
            "13:07 watched"
        );
    }

    #[test]
    fn the_bar_draws_nothing_the_share_reached_or_the_whole_work() {
        assert_eq!(share(None), 0.0);
        assert_eq!(share(Some(&reached(0, RUNTIME, false))), 0.0);
        assert_eq!(share(Some(&reached(1_500, 6_000, false))), 0.25);
        assert_eq!(share(Some(&reached(10, RUNTIME, true))), 1.0);
    }
}
