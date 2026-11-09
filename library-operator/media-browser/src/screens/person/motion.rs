// A person's page in motion. A press can move four things: the browser's
// focus outline, the row of posters, the bright part of the axis line,
// and the label over the focused year. Each eases from where it is drawn
// to where the press put it, on the same length and the press's pace, so
// one press is one motion. Every place here is computed from the rest the
// press put the row at, never from the scroll as drawn, so the outline,
// the bright part, and the label land where the row lands. The bright
// part would otherwise jump a year each time a poster crossed the edge of
// the row, because posters are evenly spaced and years are not.

use iced_winit::core::Rectangle;

use super::page::{line, row};
use super::row::Row;
use super::walk::Focus;
use super::{Person, row};
use crate::look;
use crate::views;

impl Person {
    // The row of posters, which depends on whether the page draws an axis
    // over it.
    fn row(&self, bounds: Rectangle) -> Row {
        row(bounds, self.axis.is_some())
    }

    // How far the row scrolls to come to rest.
    fn settled_row(&self, bounds: Rectangle) -> f32 {
        row::offset(self.walk.rest, self.credits.len(), &self.row(bounds))
    }

    // The two ends of the bright part of the line at rest: the years of
    // the dated credits the row shows whole. Nothing where it shows none.
    fn settled_window(&self, bounds: Rectangle) -> Option<(f32, f32)> {
        let axis = self.axis.as_ref()?;
        let shown = row::shown(
            &self.row(bounds),
            self.credits.len(),
            self.settled_row(bounds),
        );
        let years = self.credits[shown].iter().filter_map(|credit| credit.year);
        let (first, last) = years.fold(None, |span: Option<(i64, i64)>, year| {
            Some(span.map_or((year, year), |(first, last)| {
                (first.min(year), last.max(year))
            }))
        })?;
        Some(axis.window(line(bounds), (first, last)))
    }

    // The middle of the focused year, which the label centers on, and
    // nothing while a poster holds focus.
    fn settled_label(&self, bounds: Rectangle) -> Option<f32> {
        let (Focus::Axis(year), Some(axis)) = (self.walk.focus, self.axis.as_ref()) else {
            return None;
        };
        Some(axis.column(line(bounds), year).center_x())
    }

    /// The credit whose dot takes the accent: the one whose poster holds
    /// focus, on a page with an axis, where the credit has a year. While
    /// the axis holds focus, the outline marks the year and no dot does.
    pub fn accented(&self) -> Option<usize> {
        let index = self.walk.poster()?;
        self.axis.as_ref()?.year_of(index).map(|_| index)
    }

    /// How far the row has scrolled as this second draws it.
    pub(super) fn row_at(&self, bounds: Rectangle, at: f64) -> f32 {
        self.row.toward(self.settled_row(bounds), at)
    }

    /// The bright part of the line as this second draws it.
    pub(super) fn window_at(&self, bounds: Rectangle, at: f64) -> Option<(f32, f32)> {
        let (left, right) = self.settled_window(bounds)?;
        Some((
            self.window.0.toward(left, at),
            self.window.1.toward(right, at),
        ))
    }

    /// The words over the focused year and the `x` of their center as this
    /// second draws them, and nothing while a poster holds focus. The
    /// words change at the press, and their place glides.
    pub(super) fn label_at(&self, bounds: Rectangle, at: f64) -> Option<(String, f32)> {
        let middle = self.settled_label(bounds)?;
        let (Focus::Axis(year), Some(axis)) = (self.walk.focus, self.axis.as_ref()) else {
            return None;
        };
        Some((axis.label(year), self.label.toward(middle, at)))
    }

    /// The rectangle the focus outline goes to at these bounds: around the
    /// focused year's column on the axis, or around the focused poster at
    /// the place the row comes to rest. Nothing on a page with no credits.
    pub fn marked(&self, bounds: Rectangle) -> Option<Rectangle> {
        let around = match (self.walk.focus, self.axis.as_ref()) {
            (Focus::Axis(year), Some(axis)) => axis.column(line(bounds), year),
            (Focus::Poster(index), _) if index < self.credits.len() => {
                row::slot(&self.row(bounds), index, self.settled_row(bounds))
            }
            _ => return None,
        };
        Some(views::marked(around))
    }

    /// Follow the places the row, the bright part, and the label go to at
    /// these bounds: ease there on the pace of a press, or snap there
    /// where nothing pressed. The answer is the second the last of them
    /// comes to rest.
    pub fn follow(&mut self, bounds: Rectangle, at: f64, pace: Option<f64>) -> f64 {
        let length = look::GLIDE;
        let mut rests = self.row.follow(self.settled_row(bounds), at, pace, length);
        if let Some((left, right)) = self.settled_window(bounds) {
            rests = rests.max(self.window.0.follow(left, at, pace, length));
            rests = rests.max(self.window.1.follow(right, at, pace, length));
        }
        // While a poster holds focus, the label waits, at rest, over the
        // poster's year, so up from the poster shows it there and moves
        // nothing but the outline.
        let line = line(bounds);
        let label = match (self.walk.focus, self.axis.as_ref()) {
            (Focus::Axis(year), Some(axis)) => Some((axis.column(line, year).center_x(), pace)),
            (Focus::Poster(index), Some(axis)) => axis
                .year_of(index)
                .map(|year| (axis.column(line, year).center_x(), None)),
            _ => None,
        };
        if let Some((middle, pace)) = label {
            rests = rests.max(self.label.follow(middle, at, pace, length));
        }
        rests
    }
}
