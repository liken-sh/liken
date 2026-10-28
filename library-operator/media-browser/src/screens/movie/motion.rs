// A movie's page in motion. The page scrolls down to keep the focused
// block in view, and every row under the playback row scrolls across to
// keep its anchor in view. Each scroll eases from where it is drawn to
// where a press puts it, and the browser's focus outline travels to the
// focused button, mark, poster, or headshot at the place those scrolls
// come to rest, so the rows and the outline land together.

use iced_winit::core::Rectangle;

use super::page::{Blocks, placed, rows, status_line, under};
use super::{Focus, Movie, row, watch};
use crate::look;
use crate::views::across::Across;
use crate::views::{self, buttons, watch as line};

impl Movie {
    /// Every block of the page at these bounds, and the scroll down as
    /// this second draws it.
    pub(super) fn drawn(&self, bounds: Rectangle, at: f64) -> (Blocks, f32) {
        let (blocks, offset) = placed(self, bounds);
        (blocks, self.scroll.toward(offset, at))
    }

    /// The rectangle the focus outline goes to on this page at these
    /// bounds: around the focused button, mark, member, heading, or
    /// headshot, at the place every scroll comes to rest.
    pub fn marked(&self, bounds: Rectangle) -> Option<Rectangle> {
        let (blocks, offset) = placed(self, bounds);
        let around = self.playback(&blocks, offset).or_else(|| {
            rows(self, &blocks, bounds, offset, Some(self.focus))
                .iter()
                .find_map(Across::focused)
        });
        around.map(views::marked)
    }

    // The box of the focused button of the playback row or the focused
    // mark on the status line under it, at this scroll down, or nothing
    // while focus is on a row under them.
    fn playback(&self, blocks: &Blocks, offset: f32) -> Option<Rectangle> {
        let at = blocks.buttons.at(offset);
        match self.focus {
            Focus::Buttons(index) => {
                let words = row::Button::drawn(&self.buttons());
                let names: Vec<&str> = words.iter().map(|(_, name)| *name).collect();
                (index < names.len()).then(|| buttons::button(&names, index, at))
            }
            Focus::Marks(index) => {
                let status = self.status();
                let marks = watch::Mark::drawn(&self.marks());
                line::focused(&status_line(self, &status, &marks, Some(index)), under(at))
            }
            Focus::Strip(_) | Focus::Franchise(..) | Focus::Stripe(..) => None,
        }
    }

    /// Follow the places the page and every row scroll to at these
    /// bounds: ease there on the pace of a press, or snap there where
    /// nothing pressed. The answer is the second the last scroll comes to
    /// rest.
    pub fn follow(&mut self, bounds: Rectangle, at: f64, pace: Option<f64>) -> f64 {
        let (blocks, offset) = placed(self, bounds);
        let across: Vec<f32> = rows(self, &blocks, bounds, offset, Some(self.focus))
            .iter()
            .map(Across::scrolled)
            .collect();
        let down = self.scroll.follow(offset, at, pace, look::SCROLL);
        down.max(self.rows.follow(&across, at, pace))
    }
}

#[cfg(test)]
mod tests;
