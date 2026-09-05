// One franchise's page: the order of a story across films and series, as a
// wall of rows from first to last. The lines beside the rows are the
// universes, the rail at the left is the eras, and the label beside a
// row is its time on the franchise's own clock, under a caption that
// names what that clock counts. A press opens the film or the series,
// the way the set strip's press does, and a press on a gap opens
// nothing. Story order is the one order the page draws, because it is
// the one order a franchise has.

mod card;
mod metro;
mod page;
pub mod strips;
mod wall;

use std::cell::RefCell;
use std::convert::Infallible;

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::{Element, Length, Theme};

use super::{Screen, Step, movie, series};
use crate::catalog::Source;
use crate::catalog::draw::Date;
use crate::focus;
use crate::posters::Posters;
use crate::views::{band, rail};

pub use metro::Run;
pub use wall::{Cell, Row};

/// Where focus is on the page: one row, or one bar of the rail.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Focus {
    Row(usize),
    Rail(usize),
}

/// The franchise page: the universes, the rows in story order, the runs
/// of the strip, the bars of the rail, and where focus is. Every row,
/// every run, and every bar is built once here, at the read, and not on
/// every frame.
#[derive(Debug)]
pub struct Franchise {
    /// The catalog's library column of the `Library` of kind
    /// franchises, `namespace/name`.
    pub library: String,
    /// The franchise's id inside that library.
    pub id: String,
    /// The name a person reads, which the band carries.
    pub title: String,
    /// The universes, in the order the file names them. A cell and a run
    /// both name one by its place in this list.
    pub universes: Vec<String>,
    /// The lines of the metro strip: one run per universe some row
    /// names, in the order the runs start.
    pub runs: Vec<Run>,
    /// The wall in story order, which is the one order a franchise has.
    pub rows: Vec<Row>,
    /// The width of the time column, from the widest label the rows
    /// carry, and none where no row carries one.
    pub time: f32,
    /// The caption over that column, in the lines the column holds, and
    /// none where the file's calendar names no zero.
    pub caption: Vec<String>,
    /// The eras, as the bars of the rail beside the rows.
    pub eras: Vec<rail::Bar>,
    /// Where focus is.
    pub focus: Focus,
}

impl Franchise {
    /// Read one franchise's page, or nothing where that `Library` holds no
    /// franchise under that id. Focus lands on the first row, so a press opens
    /// the first entry of the story.
    pub fn open(library: &str, id: &str, source: &mut dyn Source) -> Option<Self> {
        let read = source.franchise(library, id)?;
        let today = Date::today().iso();
        let universes = wall::columns(&read);
        let rows = wall::story(&read, &universes, &today);
        let runs = metro::runs(&rows, &universes);
        let eras = wall::bars(&read.eras, &rows);
        // The column's width and its caption are measured once, at the
        // read, because they answer the same words on every frame.
        let time = wall::time_width(&rows);
        let caption = wall::caption(&read.calendar, time);
        Some(Self {
            library: library.to_string(),
            id: id.to_string(),
            title: read.title,
            universes,
            runs,
            rows,
            time,
            caption,
            eras,
            focus: Focus::Row(0),
        })
    }

    /// Read the page again, because a scan can write the order while
    /// the page is open. Focus stays where it was, inside what the read
    /// answered.
    pub fn reread(&mut self, source: &mut dyn Source) {
        let Some(fresh) = Self::open(&self.library, &self.id, source) else {
            return;
        };
        let focus = self.focus;
        *self = fresh;
        self.focus = self.hold(focus);
    }

    /// Fold one press in. Down walks forward in story order and up walks
    /// back, one row at a time whatever universe the next row is in, and
    /// up from the first row holds it. Left lands on the rail, where up
    /// and down move a bar and right returns to the first row of that
    /// bar. Right on a row does nothing, because there is no rail on the
    /// right yet. A press opens the film or the series, and opens nothing
    /// on a gap or on a bar.
    pub fn key(&mut self, key: &str, source: &mut dyn Source) -> Step {
        match self.focus {
            Focus::Row(row) => self.on_row(row, key, source),
            Focus::Rail(bar) => self.on_rail(bar, key),
        }
    }

    /// The view: the wall under the band, and the band as a layer over
    /// it.
    pub fn view<'a, P: Posters>(
        &'a self,
        posters: &'a RefCell<P>,
    ) -> Element<'a, Infallible, Theme, Renderer> {
        let wall = canvas(page::Page {
            franchise: self,
            posters,
        })
        .width(Length::Fill)
        .height(Length::Fill)
        .into();
        let band = band::layer(&self.title, &[], None);
        iced_widget::Stack::with_children(vec![wall, band])
            .width(Length::Fill)
            .height(Length::Fill)
            .into()
    }

    // One press on a cell of the wall.
    fn on_row(&mut self, row: usize, key: &str, source: &mut dyn Source) -> Step {
        if key == "enter" {
            return self.opened(row, source);
        }
        self.focus = match key {
            "up" => Focus::Row(row.saturating_sub(1)),
            "down" if row + 1 < self.rows.len() => Focus::Row(row + 1),
            "left" => match rail::covering(&self.eras, row) {
                Some(bar) => Focus::Rail(bar),
                None => Focus::Row(row),
            },
            _ => Focus::Row(row),
        };
        Step::Stay
    }

    // One press on the rail. Up and down move a bar, and right and a
    // select both jump to the first row the bar covers, which is what
    // the rail is for.
    fn on_rail(&mut self, bar: usize, key: &str) -> Step {
        self.focus = match key {
            "up" | "down" => Focus::Rail(focus::list(bar, self.eras.len(), key)),
            "right" | "enter" => match self.eras.get(bar) {
                Some(bar) => Focus::Row(bar.first),
                None => Focus::Rail(bar),
            },
            _ => Focus::Rail(bar),
        };
        Step::Stay
    }

    // The page one cell opens: the film's or the series' own. A member
    // stands in the same story as the page, so it replaces the page and
    // does not cover it, the way a sibling in a set strip does.
    fn opened(&self, row: usize, source: &mut dyn Source) -> Step {
        let Some((library, kind, id)) = self.rows.get(row).and_then(|row| row.cell.opens()) else {
            return Step::Stay;
        };
        let opened =
            match kind {
                "movies" => movie::Movie::open(library, id, source)
                    .map(|page| Screen::Movie(Box::new(page))),
                _ => series::Series::open(library, id, source)
                    .map(|page| Screen::Series(Box::new(page))),
            };
        match opened {
            Some(screen) => Step::Replace(screen),
            None => Step::Stay,
        }
    }

    // Where focus lands after a re-read: where it was, unless the row,
    // the cell, or the bar it was on went away.
    fn hold(&self, focus: Focus) -> Focus {
        match focus {
            Focus::Row(..) if self.rows.is_empty() => Focus::Row(0),
            Focus::Row(row) => Focus::Row(row.min(self.rows.len() - 1)),
            Focus::Rail(..) if self.eras.is_empty() => Focus::Row(0),
            Focus::Rail(bar) => Focus::Rail(bar.min(self.eras.len() - 1)),
        }
    }
}

#[cfg(test)]
mod tests;
