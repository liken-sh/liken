// A series' page in motion. The wall under the header scrolls down to
// keep the focused still or row in view, and every row under the seasons
// scrolls across to keep its anchor in view. Each scroll eases from where
// it is drawn to where a press puts it, and the browser's focus outline
// travels to the focused still, bar, button, mark, poster, or headshot at
// the place those scrolls come to rest. The header never scrolls, so a
// press between the wall and the header's row moves the outline and no
// scroll.

use iced_winit::core::Rectangle;

use super::page::{Placed, RAIL, placed, row_at, rows, status_line, stills, under};
use super::{Focus, Series, row, watch};
use crate::look;
use crate::views::across::Across;
use crate::views::{self, buttons, rail, wall, watch as line};

impl Series {
    /// The rectangle the focus outline goes to on this page at these
    /// bounds: around the focused button, mark, still, bar, member,
    /// heading, or headshot, at the place every scroll comes to rest.
    pub fn marked(&self, bounds: Rectangle) -> Option<Rectangle> {
        let placed = placed(self, bounds);
        let around = match self.focus {
            Focus::Buttons(index) | Focus::Episode(_, index) => self.button_box(bounds, index),
            Focus::EpisodeMark(_, index) => self.mark_box(bounds, index),
            Focus::Still(index) => self.still_box(&placed, index),
            Focus::Rail(bar) => {
                let (side, fit) = RAIL;
                rail::focused(placed.whole, &self.bars, Some(bar), side, fit)
            }
            Focus::Franchise(..) | Focus::Stripe(..) => {
                rows(self, &placed, placed.offset, Some(self.focus))
                    .iter()
                    .find_map(Across::focused)
            }
        };
        around.map(views::marked)
    }

    // The box of one button of the header's row: the series' own, or an
    // open episode's.
    fn button_box(&self, bounds: Rectangle, index: usize) -> Option<Rectangle> {
        let words = row::Button::drawn(&self.buttons());
        let names: Vec<&str> = words.iter().map(|(_, name)| *name).collect();
        (index < names.len()).then(|| buttons::button(&names, index, row_at(self, bounds)))
    }

    // The box of one mark on the status line under an open episode's row.
    fn mark_box(&self, bounds: Rectangle, index: usize) -> Option<Rectangle> {
        let still = self.opened()?;
        let status = still.status();
        let marks = watch::Mark::drawn(&self.marks());
        let line = status_line(still, &status, &marks, Some(index));
        line::focused(&line, under(row_at(self, bounds)))
    }

    // The box of one still in the grid of its season, where the wall
    // comes to rest.
    fn still_box(&self, placed: &Placed, index: usize) -> Option<Rectangle> {
        let season = self.seasons.iter().position(|season| {
            index >= season.run.first && index < season.run.first + season.run.count
        })?;
        let grid = stills(self, placed, season, placed.offset, Some(self.focus));
        wall::focused(&grid)
    }

    /// Follow the places the wall and every row under the seasons scroll
    /// to at these bounds: ease there on the pace of a press, or snap
    /// there where nothing pressed. The answer is the second the last
    /// scroll comes to rest.
    pub fn follow(&mut self, bounds: Rectangle, at: f64, pace: Option<f64>) -> f64 {
        let placed = placed(self, bounds);
        let across: Vec<f32> = rows(self, &placed, placed.offset, Some(self.focus))
            .iter()
            .map(Across::scrolled)
            .collect();
        let down = self.scroll.follow(placed.offset, at, pace, look::SCROLL);
        down.max(self.rows.follow(&across, at, pace))
    }
}

#[cfg(test)]
mod tests;
