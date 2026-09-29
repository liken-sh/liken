// An episode's own row on the series page: the buttons that play the
// episode or pick up the series from it, the marks on the status line
// under the row, and what a press on either asks the browser for.
// `pickup.rs` holds which episodes Pick up here marks.

use super::{Focus, Series, Still, progress};
use crate::catalog::{Selection, Source};
use crate::focus;
use crate::screens::movie::{marked_duration, row, watch};
use crate::screens::{Step, upnext};

impl Series {
    // One press while an episode's row holds focus. Select presses the
    // button and returns focus to the still, so the page a film returns to
    // is the wall. Left and right move across the row, and down reaches
    // the marks on the status line under it. Up moves no focus, so the
    // press reaches the browser's strip.
    pub(super) fn on_episode(
        &mut self,
        (index, button): (usize, usize),
        key: &str,
        source: &mut dyn Source,
    ) -> Step {
        if index >= self.stills.len() {
            self.focus = Focus::Still(0);
            return Step::Stay;
        }
        let buttons = self.episode_row(index);
        match key {
            "enter" => {
                let Some(pressed) = buttons.get(button).copied() else {
                    return Step::Stay;
                };
                self.focus = Focus::Still(index);
                self.press(index, pressed, source)
            }
            // Every episode offers at least one mark, so down always
            // lands on the status line.
            "down" => {
                self.focus = Focus::EpisodeMark(index, 0);
                Step::Stay
            }
            "up" => Step::Stay,
            _ => {
                self.focus = Focus::Episode(index, focus::row(button, buttons.len(), key));
                Step::Stay
            }
        }
    }

    // One press on a mark under an episode's row. Select marks the episode
    // and returns focus to the still, as a button of the row does. Up
    // returns to the first button of the row, and down closes the row and
    // returns to the still it was opened from.
    pub(super) fn on_episode_mark(&mut self, (index, mark): (usize, usize), key: &str) -> Step {
        let Some(still) = self.stills.get(index) else {
            self.focus = Focus::Still(0);
            return Step::Stay;
        };
        let marks = watch::marks(still.progress.as_ref());
        match key {
            "enter" => {
                let Some(pressed) = marks.get(mark).copied() else {
                    return Step::Stay;
                };
                self.focus = Focus::Still(index);
                self.mark(index, pressed)
            }
            "up" => {
                self.focus = Focus::Episode(index, 0);
                Step::Stay
            }
            "down" => {
                self.focus = Focus::Still(index);
                Step::Stay
            }
            _ => {
                self.focus = Focus::EpisodeMark(index, focus::row(mark, marks.len(), key));
                Step::Stay
            }
        }
    }

    // One mark on one episode, for everyone at the screen.
    fn mark(&self, index: usize, mark: watch::Mark) -> Step {
        let still = &self.stills[index];
        Step::Mark {
            library: self.library.clone(),
            selection: self.episode(still),
            mark: mark.title(),
            duration: marked_duration(still.progress.as_ref(), still.duration),
        }
    }

    // The selection that names one episode of this series.
    fn episode(&self, still: &Still) -> Selection {
        Selection::Episode {
            series: self.id.clone(),
            season: still.season,
            episode: still.episode,
        }
    }

    // What one button of an episode's row asks for: the episode from the
    // second the audience reached, or the episode from the beginning.
    fn press(&self, index: usize, button: row::Button, source: &mut dyn Source) -> Step {
        let still = &self.stills[index];
        let numbers = (still.season, still.episode);
        let selection = self.episode(still);
        let start = match button {
            row::Button::Resume => progress::start(still),
            row::Button::Play
            | row::Button::StartOver
            | row::Button::Trailer
            | row::Button::PickUp => None,
        };
        let play = Step::Play {
            library: self.library.clone(),
            selection,
            start,
            next: upnext::after_episode(
                source,
                &self.library,
                &self.id,
                &self.title,
                numbers,
                self.via.as_ref(),
            )
            .map(Box::new),
        };
        match button {
            row::Button::PickUp => Step::PickUp {
                library: self.library.clone(),
                series: self.id.clone(),
                episodes: self.picked_up(index),
                play: Box::new(play),
            },
            _ => play,
        }
    }

    /// The row of one episode: Play, or Resume and Start over while the
    /// audience is in the middle of it, as a movie's page draws them with
    /// no trailer, because an episode holds none. Pick up here follows
    /// where a press of it would mark an earlier episode or clear a later
    /// one, and not where it would do no more than Play.
    pub fn episode_row(&self, index: usize) -> Vec<row::Button> {
        let still = &self.stills[index];
        let mut buttons = row::of(still.progress.as_ref(), false);
        if self.reach.changes(still) {
            buttons.push(row::Button::PickUp);
        }
        buttons
    }
}
