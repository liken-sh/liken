// The wall screen: the slots one query answers, as a wall of art under
// a band that carries the query's heading. A long wall draws a rail at
// its right edge whose bars jump through the list and whose button
// cycles the order. A wall over a `Search` holds the field a person
// types into, which the browser's strip draws, and the grid a remote
// with no keyboard picks letters from.

use std::cell::RefCell;
use std::convert::Infallible;

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::{Element, Length, Rectangle, Theme, mouse};

use super::Step;
use super::slots::Slots;
use crate::catalog::{Query, Source};
use crate::focus;
use crate::posters::Posters;
use crate::views::{area, band, card, clip_marked, wall};

// The rail module: which walls draw one, what its bars say, and how it
// draws beside the slots.
mod rail;
// The search module: what a wall over a `Search` holds, the presses it
// takes for itself, and the layer it draws over the band.
mod search;

pub use search::{Search, searched};
use search::{Typing, grid_height};

/// Where focus is on a wall: on the slots, or on one cell of the rail,
/// which is the sort button where the wall has one and then the bars
/// under it.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub enum Focus {
    #[default]
    Slots,
    Rail(usize),
}

/// The wall screen: the slots the query answered and the heading as the
/// band draws it. The heading is built at every read and not on every
/// frame. `search` is the field and the grid a wall over a `Search`
/// types on, and nothing on every other wall.
#[derive(Debug)]
pub struct Wall {
    pub slots: Slots,
    pub heading: String,
    pub search: Option<Search>,
    /// The bars of the rail beside the slots, and none on a wall that
    /// draws no rail.
    pub bars: Vec<rail::Jump>,
    /// Where focus is.
    pub focus: Focus,
}

impl Wall {
    /// Read the query's slots, with focus on the first of them.
    pub fn open(query: Query, source: &mut dyn Source) -> Self {
        let mut wall = Self {
            slots: Slots::open(query, source),
            heading: String::new(),
            search: None,
            bars: Vec::new(),
            focus: Focus::Slots,
        };
        wall.headed();
        wall.railed();
        wall
    }

    /// Read the titles again and keep focus in range, because a change
    /// can remove the focused title.
    pub fn reread(&mut self, source: &mut dyn Source) {
        self.slots.reread(source);
        self.headed();
        self.railed();
    }

    // The bars again, because the answer they name has changed. Focus
    // returns to the slots where the rail no longer holds the cell it
    // was on.
    fn railed(&mut self) {
        self.bars = rail::bars(&self.slots.items, &self.slots.query, rail::region());
        if let Focus::Rail(cell) = self.focus {
            let cells = rail::cells(&self.bars, &self.slots.query);
            self.focus = match self.bars.is_empty() {
                true => Focus::Slots,
                false => Focus::Rail(cell.min(cells - 1)),
            };
        }
    }

    // The band carries every query's heading, and a genre's carries the
    // counts by kind.
    fn headed(&mut self) {
        self.heading = self.slots.heading();
    }

    /// Fold one press in. The arrows move across the slots, and up from
    /// the first row moves nothing, which is how a press reaches the
    /// browser's strip.
    ///
    /// On a search wall, a letter, a digit, or the space types and hides
    /// the grid. While the grid is shown, the arrows move its focus and
    /// select presses the focused cell into the field.
    pub fn key(&mut self, key: &str, source: &mut dyn Source) -> Step {
        if let Some(step) = self.typed(key, source) {
            return step;
        }
        match self.focus {
            Focus::Slots => self.on_slots(key, source),
            Focus::Rail(cell) => self.on_rail(cell, key, source),
        }
    }

    // One press while the slots hold focus. A right press at the right
    // edge of a row moves onto the bar that covers the row.
    fn on_slots(&mut self, key: &str, source: &mut dyn Source) -> Step {
        if key == "right"
            && let Some(cell) = self.onto()
        {
            self.focus = Focus::Rail(cell);
            return Step::Stay;
        }
        if key == "up" && self.slots.focus < wall::COLUMNS {
            return Step::Still;
        }
        self.slots.key(key, source)
    }

    // The cell of the rail a right press from the focused slot moves
    // onto, or nothing where the slot is not at the right edge of its
    // row or the wall draws no rail.
    fn onto(&self) -> Option<usize> {
        let last = self.slots.items.len().checked_sub(1)?;
        let index = self.slots.focus;
        if index % wall::COLUMNS != wall::COLUMNS - 1 && index != last {
            return None;
        }
        let bar = rail::covering(&self.bars, index / wall::COLUMNS)?;
        Some(bar + rail::first(&self.slots.query))
    }

    // One press while a cell of the rail holds focus: up and down along
    // the cells, select on a bar onto the first slot it covers, select on
    // the button into the next order, and left back to the slots. Up from
    // the top cell moves nothing, and the browser's strip takes that
    // press.
    fn on_rail(&mut self, cell: usize, key: &str, source: &mut dyn Source) -> Step {
        if key == "up" && cell == 0 {
            return Step::Still;
        }
        match (key, rail::barred_at(cell, &self.slots.query)) {
            ("up" | "down", _) => {
                let cells = rail::cells(&self.bars, &self.slots.query);
                self.focus = Focus::Rail(focus::list(cell, cells, key));
                let standing = self.standing();
                self.slots.stand(standing);
            }
            ("left" | "enter", Some(bar)) => {
                let head = self.head(bar);
                self.focus = Focus::Slots;
                self.slots.focus = head;
            }
            ("enter", None) => self.resort(source),
            ("left", None) => self.focus = Focus::Slots,
            _ => {}
        }
        Step::Stay
    }

    /// Whether a slot of the wall carries the focus mark: only while the
    /// slots hold focus and neither the keyboard grid, the rail, nor the
    /// browser's strip has taken it. `held` is whether the wall holds
    /// focus at all.
    pub fn marks(&self, held: bool) -> bool {
        held && self.grid().is_none() && self.focus == Focus::Slots
    }

    /// The cell of the rail that carries the mark, or nothing while the
    /// slots or the browser's strip hold focus.
    pub fn marked_cell(&self, held: bool) -> Option<usize> {
        match (held, self.focus) {
            (true, Focus::Rail(cell)) => Some(cell),
            _ => None,
        }
    }

    /// The slot the wall stands at: the focused slot, or the first item
    /// of the focused bar while a bar holds focus, so a move along the
    /// bars scrolls the wall to what a select on the bar lands on. The
    /// sort button moves the wall nowhere.
    pub fn standing(&self) -> usize {
        match self.focus {
            Focus::Slots => self.slots.focus,
            Focus::Rail(cell) => match rail::barred_at(cell, &self.slots.query) {
                Some(bar) => self.head(bar),
                None => self.slots.focus,
            },
        }
    }

    // The slot a select on one bar lands on: the first item the bar
    // covers, which is not the first item of its first row where a letter
    // range or a run starts in the middle of a row.
    fn head(&self, bar: usize) -> usize {
        let item = self.bars.get(bar).map(|jump| jump.item).unwrap_or_default();
        item.min(self.slots.items.len().saturating_sub(1))
    }

    // The same wall in the next order its button cycles to. The read
    // answers the new order, the heading counts it again, and the bars
    // are the new order's own.
    fn resort(&mut self, source: &mut dyn Source) {
        self.slots.query = self.slots.query.resorted();
        self.slots.focus = 0;
        self.reread(source);
    }

    /// Fold in the press that leaves a screen. A search wall with text
    /// in its field takes it and clears the text, or removes one
    /// character on backspace. Otherwise the answer is nothing, and the
    /// browser goes back.
    pub fn escape(&mut self, key: &str, source: &mut dyn Source) -> Option<Step> {
        self.cleared(key, source)
    }

    /// Whether a rest of focus on this wall is worth a prefetch. It is
    /// while the posters hold focus, because a select on either kind
    /// opens a page over a backdrop.
    pub fn prefetches(&self) -> bool {
        self.grid().is_none()
    }

    /// The library and the backdrop the focused title's page draws over.
    /// The browser asks the store for it once focus rests. A wall whose
    /// select opens no page over art answers nothing.
    pub fn resting(&self, source: &mut dyn Source) -> Option<(String, String)> {
        if !self.prefetches() {
            return None;
        }
        self.slots.resting(source)
    }

    // The height a layer of the wall's own takes under the band: the
    // keyboard grid on a search wall, and nothing otherwise.
    fn under_band(&self) -> f32 {
        grid_height(self.grid().is_some())
    }

    /// The view: the wall of posters, and the band as a layer over them.
    /// A search wall showing its grid draws a third layer over the band,
    /// because a layer draws every fill before every text and the band
    /// paints its own ground.
    pub fn view<'a, P: Posters>(
        &'a self,
        posters: &'a RefCell<P>,
        held: bool,
    ) -> Element<'a, Infallible, Theme, Renderer> {
        let grid = canvas(Program {
            wall: self,
            posters,
            held,
        })
        .width(Length::Fill)
        .height(Length::Fill)
        .into();
        let band = band::layer(&self.heading);
        let mut layers = vec![grid, band];
        if let Some(keyboard) = self.grid() {
            layers.push(
                canvas(Typing { keyboard })
                    .width(Length::Fill)
                    .height(Length::Fill)
                    .into(),
            );
        }
        iced_widget::Stack::with_children(layers)
            .width(Length::Fill)
            .height(Length::Fill)
            .into()
    }
}

// The wall's drawing under the band: the head and the grid, on one frame.
struct Program<'a, P> {
    wall: &'a Wall,
    posters: &'a RefCell<P>,
    // Whether the wall holds focus, or the browser's strip over it does.
    held: bool,
}

impl<P: Posters> canvas::Program<Infallible, Theme, Renderer> for Program<'_, P> {
    type State = ();

    fn draw(
        &self,
        _state: &Self::State,
        renderer: &Renderer,
        _theme: &Theme,
        bounds: Rectangle,
        _cursor: mouse::Cursor,
    ) -> Vec<canvas::Geometry<Renderer>> {
        let mut frame = canvas::Frame::new(renderer, bounds.size());
        // The rail takes its lane off the right of the region, and the
        // slots keep the rest.
        let whole = region(bounds, self.wall.under_band());
        let region = rail::beside(whole, &self.wall.bars);
        self.wall.slots.draw_at(
            &mut frame,
            &mut *self.posters.borrow_mut(),
            region,
            self.wall.standing(),
            self.wall.marks(self.held),
            card::LINES,
        );
        // The clip reaches past the region by the gap and the whole focus
        // stroke, so the mark on the first bar and on the button draws
        // whole.
        frame.with_clip(clip_marked(whole), |frame| {
            rail::draw(
                frame,
                whole,
                &self.wall.bars,
                &self.wall.slots.query,
                self.wall.marked_cell(self.held),
            );
        });
        vec![frame.into_geometry()]
    }
}

// The part of the frame the grid scrolls in: under the band, and under
// the space that keeps the mark of a focused slot in the first row off
// what is over it.
// `under` is the height a layer of the wall's own takes under the band,
// the keyboard grid's, so the slots start under that layer
// and never behind it.
fn region(bounds: Rectangle, under: f32) -> Rectangle {
    let top = band::HEIGHT + under;
    area(
        0.0,
        top + wall::HEAD,
        bounds.width,
        bounds.height - top - wall::HEAD,
    )
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::sample::Catalog;

    const WIDTH: f32 = 1920.0;
    const HEIGHT: f32 = 1080.0;

    fn frame() -> Rectangle {
        area(0.0, 0.0, WIDTH, HEIGHT)
    }

    #[test]
    fn every_wall_starts_its_grid_under_the_band() {
        let bare = region(frame(), 0.0);
        assert_eq!(bare.y, band::HEIGHT + wall::HEAD);
        assert_eq!(bare.y + bare.height, HEIGHT);

        let under = region(frame(), 200.0);
        assert!(under.y > bare.y, "{under:?}");
        assert_eq!(under.y + under.height, HEIGHT);
    }

    #[test]
    fn a_row_of_posters_fits_under_the_band() {
        let cells = wall::lined(WIDTH, wall::POSTER, wall::COLUMNS, 1);
        assert!(cells.height <= region(frame(), 0.0).height);
    }

    #[test]
    fn a_wall_showing_its_keyboard_grid_prefetches_nothing() {
        let query = Query::Search { text: "a".into() };
        let mut wall = Wall::open(query, &mut Catalog);
        wall.search = Some(search::Search::default());
        wall.show_grid();
        assert!(!wall.prefetches());
        assert_eq!(wall.resting(&mut Catalog), None);
    }
}
