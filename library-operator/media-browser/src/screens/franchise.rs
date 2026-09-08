// One franchise's page: the order of a story across films and series, as a
// wall of rows from first to last. The lines beside the rows are the
// universes, a heading over a row starts an era, and the label beside a
// row is its time on the franchise's own clock. The band's note names
// what that clock counts. A press opens the film or the series,
// the way the set strip's press does, and a press on a gap opens
// nothing. Story order is the one order the page draws, because it is
// the one order a franchise has.

mod card;
mod metro;
mod page;
pub mod strips;
mod wall;

use std::cell::{self, RefCell};
use std::convert::Infallible;

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::{Element, Length, Theme};

use super::{Screen, Step, movie, series};
use crate::art::Art;
use crate::catalog::Source;
use crate::catalog::draw::Date;
use crate::views::band;

pub use metro::Run;
pub use wall::{Cell, Row};

/// The franchise page: the universes, the rows in story order, the runs
/// of the strip, the headings of the eras, and where focus is. Every
/// row, every run, and every heading is built once here, at the read,
/// and not on every frame.
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
    /// The one line under the band's title that says what the times
    /// count from, and none where the file's calendar names no zero.
    pub caption: String,
    /// The eras, as the headings over the rows.
    pub headings: Vec<wall::Heading>,
    /// The row focus is on.
    pub focus: usize,
    /// How far the wall stood scrolled at the last frame. The scroll
    /// moves only when focus leaves the view, so the last position is
    /// part of the page, and the frame that draws it writes it here.
    pub scrolled: cell::Cell<f32>,
}

impl Franchise {
    /// Read one franchise's page, or nothing where that `Library` holds no
    /// franchise under that id. Focus lands on the first row, so a press opens
    /// the first entry of the story.
    pub fn open(library: &str, id: &str, source: &mut dyn Source) -> Option<Self> {
        Self::read(library, id, source).map(|(page, _)| page)
    }

    // The page with focus on the row of the entry at this position in story
    // order, which is where a press on a card of the continue-watching row
    // lands. Focus stays on the first row where the order holds no entry at
    // that position.
    pub fn open_at(
        library: &str,
        id: &str,
        position: i64,
        source: &mut dyn Source,
    ) -> Option<Self> {
        let (mut page, positions) = Self::read(library, id, source)?;
        if let Some(row) = positions.iter().position(|at| *at == position) {
            page.focus = row;
        }
        Some(page)
    }

    // The page with focus on the first row, and the story position of every
    // row, so an entry by position finds its row.
    fn read(library: &str, id: &str, source: &mut dyn Source) -> Option<(Self, Vec<i64>)> {
        let read = source.franchise(library, id)?;
        let positions = read.entries.iter().map(|entry| entry.position).collect();
        let today = Date::today().iso();
        let universes = wall::columns(&read);
        let rows = wall::story(&read, &universes, &today);
        let runs = metro::runs(&rows, &universes);
        let headings = wall::headings(&read.eras, &rows, &read.calendar);
        // The column's width and its caption are measured once, at the
        // read, because they answer the same words on every frame.
        let time = wall::time_width(&rows);
        let caption = wall::caption(&read.calendar, time);
        let page = Self {
            library: library.to_string(),
            id: id.to_string(),
            title: read.title,
            universes,
            runs,
            rows,
            time,
            caption,
            headings,
            focus: 0,
            scrolled: cell::Cell::new(0.0),
        };
        Some((page, positions))
    }

    /// Read the page again, because a scan can write the order while
    /// the page is open. Focus stays where it was, inside what the read
    /// answered.
    pub fn reread(&mut self, source: &mut dyn Source) {
        let Some(fresh) = Self::open(&self.library, &self.id, source) else {
            return;
        };
        let focus = self.focus;
        let scrolled = self.scrolled.get();
        *self = fresh;
        self.focus = self.hold(focus);
        self.scrolled.set(scrolled);
    }

    /// Fold one press in. Down walks forward in story order and up walks
    /// back, one row at a time whatever universe the next row is in, and
    /// up from the first row holds it. Left and right step an era at a
    /// time, to the first row of the era before or after, so a person
    /// crosses a long wall in a few presses. A press opens the film or
    /// the series, and opens nothing on a gap.
    pub fn key(&mut self, key: &str, source: &mut dyn Source) -> Step {
        let row = self.focus;
        if key == "enter" {
            return self.opened(row, source);
        }
        // Up from the first row moves nothing, which is how a press
        // reaches the browser's strip.
        if key == "up" && row == 0 {
            return Step::Still;
        }
        self.focus = match key {
            "up" => row.saturating_sub(1),
            "down" if row + 1 < self.rows.len() => row + 1,
            "left" => wall::before(&self.headings, row).unwrap_or(row),
            "right" => wall::after(&self.headings, row).unwrap_or(row),
            _ => row,
        };
        Step::Stay
    }

    /// The view: the wall, the headings that have left their own tops as
    /// a layer over it, and the band as a layer over both.
    pub fn view<'a, A: Art>(
        &'a self,
        store: &'a RefCell<A>,
        held: bool,
    ) -> Element<'a, Infallible, Theme, Renderer> {
        let wall = canvas(page::Page {
            franchise: self,
            store,
            held,
        })
        .width(Length::Fill)
        .height(Length::Fill)
        .into();
        let held = canvas(page::Held { franchise: self })
            .width(Length::Fill)
            .height(Length::Fill)
            .into();
        let band = band::layer(&self.title, &self.caption);
        iced_widget::Stack::with_children(vec![wall, held, band])
            .width(Length::Fill)
            .height(Length::Fill)
            .into()
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

    // Where focus lands after a re-read: where it was, unless the row it
    // was on went away.
    fn hold(&self, focus: usize) -> usize {
        focus.min(self.rows.len().saturating_sub(1))
    }
}

#[cfg(test)]
mod tests;
