// The home page: the screen the shade lifts to, and the bottom of the
// stack. It draws the band across the top, then one strip per row of the
// page, top to bottom: what was released, what arrived, and the libraries
// as the floor the page never loses. A strip whose read answers nothing
// draws nothing, and focus skips it. The day's draw and the banner take
// their places in the rows when their plans land.

use std::cell::RefCell;
use std::convert::Infallible;

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::{Element, Length, Rectangle, Theme, mouse};

use super::wall::Wall;
use super::{Item, Screen, Step, facts, slots};
use crate::catalog::recency::SHOWN;
use crate::catalog::{Fold, LibraryEntry, Query, Source, library_name};
use crate::focus;
use crate::posters::Posters;
use crate::views::{area, band, stack, strip, wall};

// The band's heading on the home page is the word "Home" and not the
// namespace, because a namespace is a cluster's word and the person on
// the couch has one home.
const HEADING: &str = "Home";

// The kind an item of the libraries strip carries, so a select on it
// opens the library's wall and never a page.
const LIBRARY: &str = "library";

// The margin at both sides of the strips, which is the band's own inset,
// so the headings line up.
const MARGIN: f32 = 32.0;

// The space between two strips.
const GAP: f32 = 28.0;

// How much of the strip under the focused one the scroll keeps in
// view, so a person sees that there is more below.
const TRAIL: f32 = 70.0;

/// One row of the page as a read: the slots of one query, or the
/// libraries themselves. The day's draw adds query rows here, and the
/// banner is a row of its own when its plan lands.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Row {
    Query(Query),
    Libraries,
}

// The rows of the page, top to bottom. The two recency strips use the
// `Airing` fold because the home page shows what is new, and the
// libraries close the page.
fn rows() -> Vec<Row> {
    vec![
        Row::Query(Query::Released { fold: Fold::Airing }),
        Row::Query(Query::Added { fold: Fold::Airing }),
        Row::Libraries,
    ]
}

/// One strip of the page: the row it reads, the heading over it, the
/// items in the read's order, the focused index, whether a "see all"
/// slot ends it, and the caption lines under each slot.
#[derive(Debug)]
pub struct Strip {
    pub row: Row,
    pub heading: String,
    pub items: Vec<Item>,
    pub focus: usize,
    pub see_all: bool,
    pub lines: usize,
}

impl Strip {
    fn read(row: Row, source: &mut dyn Source) -> Self {
        let mut strip = Self {
            heading: String::new(),
            items: Vec::new(),
            focus: 0,
            see_all: matches!(row, Row::Query(_)),
            lines: match row {
                Row::Query(_) => 1,
                Row::Libraries => 2,
            },
            row,
        };
        strip.reread(source);
        strip
    }

    // Read the strip's row again and keep focus in range. A query strip
    // shows the first `SHOWN` slots and leaves the rest to the wall behind
    // "see all".
    fn reread(&mut self, source: &mut dyn Source) {
        match &self.row {
            Row::Query(query) => {
                let answer = source.wall(query);
                self.heading = query.name(&answer.name);
                self.items = answer
                    .slots
                    .into_iter()
                    .take(SHOWN)
                    .map(|slot| Item::of(query, slot))
                    .collect();
            }
            Row::Libraries => {
                self.heading = "Libraries".to_string();
                self.items = source.libraries().into_iter().map(library_item).collect();
            }
        }
        self.focus = self.focus.min(self.count().saturating_sub(1));
    }

    // The slots a press can reach: the items, and the "see all" slot
    // after them.
    fn count(&self) -> usize {
        self.items.len() + usize::from(self.see_all)
    }

    fn is_empty(&self) -> bool {
        self.items.is_empty()
    }

    // The item that holds focus, or nothing while "see all" holds it.
    fn focused(&self) -> Option<&Item> {
        self.items.get(self.focus)
    }
}

// One library as a slot of the libraries strip: its name as the
// caption, its kind and count under it, and the poster of its
// newest-added title as the art.
fn library_item(entry: LibraryEntry) -> Item {
    let name = library_name(&entry.library).to_string();
    Item {
        id: entry.library.clone(),
        library: entry.library,
        kind: LIBRARY.to_string(),
        caption: name.clone(),
        line: facts::Line::of(&[&name]),
        name,
        under: format!("{} · {}", entry.kind, entry.items),
        art: entry.art,
        episode: None,
    }
}

/// The home page: the heading as the band draws it, the control that
/// holds focus or nothing while a strip holds it, the strips in the page's
/// order, and the strip that holds focus.
#[derive(Debug)]
pub struct Home {
    pub heading: String,
    pub control: Option<usize>,
    pub strips: Vec<Strip>,
    pub focus: usize,
}

impl Home {
    /// Read every row, with focus on the first strip that holds anything.
    pub fn open(source: &mut dyn Source) -> Self {
        let mut home = Self {
            heading: HEADING.to_string(),
            control: None,
            strips: rows()
                .into_iter()
                .map(|row| Strip::read(row, source))
                .collect(),
            focus: 0,
        };
        home.settle();
        home
    }

    /// Read every strip again and keep focus where it was, because a
    /// change can empty the strip that held it.
    pub fn reread(&mut self, source: &mut dyn Source) {
        for strip in &mut self.strips {
            strip.reread(source);
        }
        self.settle();
    }

    // Where focus lands after a read: the strip it was on, or the nearest
    // strip below or above it that holds anything, or the band where no
    // strip does.
    fn settle(&mut self) {
        if self.holds(self.focus) {
            return;
        }
        match self.below(self.focus).or_else(|| self.above(self.focus)) {
            Some(index) => self.focus = index,
            None => self.control = Some(0),
        }
    }

    fn holds(&self, index: usize) -> bool {
        self.strips
            .get(index)
            .is_some_and(|strip| !strip.is_empty())
    }

    // The nearest strip above this one that holds anything.
    fn above(&self, index: usize) -> Option<usize> {
        (0..index).rev().find(|index| self.holds(*index))
    }

    // The nearest strip below this one that holds anything.
    fn below(&self, index: usize) -> Option<usize> {
        (index + 1..self.strips.len()).find(|index| self.holds(*index))
    }

    /// Fold one press in. In the band, left and right move across the
    /// controls, select does nothing because none of the three exists
    /// yet, and down returns focus to the strip the page remembers. On
    /// the strips, up and down move between them, up from the first
    /// reaches the band, left and right move inside one, and select opens
    /// what the slot names.
    pub fn key(&mut self, key: &str, source: &mut dyn Source) -> Step {
        if let Some(control) = self.control {
            match key {
                "down" if self.holds(self.focus) => self.control = None,
                "down" | "enter" => {}
                _ => self.control = Some(focus::row(control, band::CONTROLS.len(), key)),
            }
            return Step::Stay;
        }
        match key {
            "up" => match self.above(self.focus) {
                Some(index) => self.focus = index,
                None => self.control = Some(0),
            },
            "down" => {
                if let Some(index) = self.below(self.focus) {
                    self.focus = index;
                }
            }
            "enter" => return self.select(source),
            _ => {
                if let Some(strip) = self.strips.get_mut(self.focus) {
                    strip.focus = focus::row(strip.focus, strip.count(), key);
                }
            }
        }
        Step::Stay
    }

    // What a select opens. "See all" opens the wall of the strip's query
    // with every episode folded to its series, a library opens its wall, and
    // a title opens its page by its kind.
    fn select(&self, source: &mut dyn Source) -> Step {
        let Some(strip) = self.strips.get(self.focus) else {
            return Step::Stay;
        };
        let Some(item) = strip.focused() else {
            return match &strip.row {
                Row::Query(query) if strip.see_all => {
                    Step::Open(Screen::Wall(Wall::open(query.all_titles(), source)))
                }
                _ => Step::Stay,
            };
        };
        if item.kind == LIBRARY {
            let query = Query::Library {
                library: item.library.clone(),
            };
            return Step::Open(Screen::Wall(Wall::open(query, source)));
        }
        slots::opened(item, source)
    }

    /// Whether a rest of focus on this page is worth a prefetch: while a
    /// title holds focus, because a select on it opens a page over a
    /// backdrop.
    pub fn prefetches(&self) -> bool {
        self.control.is_none()
            && self
                .strips
                .get(self.focus)
                .and_then(Strip::focused)
                .is_some_and(|item| item.kind != LIBRARY)
    }

    /// The library and the backdrop the focused title's page draws over,
    /// so the store decodes it while focus rests.
    pub fn resting(&self, source: &mut dyn Source) -> Option<(String, String)> {
        if !self.prefetches() {
            return None;
        }
        slots::backdrop(self.strips.get(self.focus)?.focused()?, source)
    }

    /// The view: the band, and the strips under it.
    pub fn view<'a, P: Posters>(
        &'a self,
        posters: &'a RefCell<P>,
    ) -> Element<'a, Infallible, Theme, Renderer> {
        canvas(Program {
            home: self,
            posters,
        })
        .width(Length::Fill)
        .height(Length::Fill)
        .into()
    }
}

// Where every strip that holds anything lands under the band, in the
// page's own space before the scroll, and how tall the page is.
struct Blocks {
    tops: Vec<Option<f32>>,
    content: f32,
}

impl Blocks {
    fn of(home: &Home) -> Self {
        let mut at = wall::HEAD;
        let tops = home
            .strips
            .iter()
            .map(|strip| {
                if strip.is_empty() {
                    return None;
                }
                let top = at;
                at += strip::height(strip.lines) + GAP;
                Some(top)
            })
            .collect();
        Self {
            tops,
            content: at - GAP + wall::HEAD,
        }
    }

    // How far the page has scrolled: enough to hold the focused strip and
    // the head of the strip under it, and none while the band holds
    // focus.
    fn scroll(&self, home: &Home, height: f32) -> f32 {
        if home.control.is_some() {
            return 0.0;
        }
        let Some(top) = self.tops.get(home.focus).copied().flatten() else {
            return 0.0;
        };
        let lines = home.strips[home.focus].lines;
        let block = area(0.0, top, 0.0, strip::height(lines));
        let tail = TRAIL.min(self.content - block.y - block.height);
        stack::offset(block, tail, self.content, height)
    }
}

// The page's drawing: the band over the strips, on one frame.
struct Program<'a, P> {
    home: &'a Home,
    posters: &'a RefCell<P>,
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
        let home = self.home;
        let mut frame = canvas::Frame::new(renderer, bounds.size());
        band::draw(&mut frame, bounds.width, &home.heading, home.control);

        let blocks = Blocks::of(home);
        let viewport = bounds.height - band::HEIGHT;
        let offset = blocks.scroll(home, viewport);
        let clip = area(0.0, band::HEIGHT, bounds.width, viewport);
        frame.with_clip(clip, |frame| {
            let posters = &mut *self.posters.borrow_mut();
            for (index, strip) in home.strips.iter().enumerate() {
                let Some(top) = blocks.tops[index] else {
                    continue;
                };
                let region = area(
                    MARGIN,
                    band::HEIGHT + top - offset,
                    bounds.width - 2.0 * MARGIN,
                    strip::height(strip.lines),
                );
                if region.y + region.height < band::HEIGHT || region.y > bounds.height {
                    continue;
                }
                strip::draw(
                    frame,
                    posters,
                    &strip::Strip {
                        members: &strip.items,
                        current: None,
                        focus: (home.control.is_none() && home.focus == index)
                            .then_some(strip.focus),
                        heading: &strip.heading,
                        library: "",
                        see_all: strip.see_all,
                        lines: strip.lines,
                        region,
                    },
                );
            }
        });
        vec![frame.into_geometry()]
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn the_page_reads_the_two_recency_rows_and_then_the_libraries() {
        assert_eq!(
            rows(),
            [
                Row::Query(Query::Released { fold: Fold::Airing }),
                Row::Query(Query::Added { fold: Fold::Airing }),
                Row::Libraries,
            ]
        );
    }

    #[test]
    fn a_library_is_a_slot_with_its_name_its_count_and_its_newest_poster() {
        let item = library_item(LibraryEntry {
            library: "screening/features".into(),
            kind: "movies".into(),
            items: 42,
            art: "posters/newest.jpg".into(),
        });
        assert_eq!(item.kind, LIBRARY);
        assert_eq!(item.id, "screening/features");
        assert_eq!(item.library, "screening/features");
        assert_eq!(item.name, "features");
        assert_eq!(item.caption, "features");
        assert_eq!(item.line.words(), "features");
        assert_eq!(item.under, "movies · 42");
        assert_eq!(item.art, "posters/newest.jpg");
        assert_eq!(item.episode, None);
    }

    #[test]
    fn the_trail_keeps_the_next_strips_heading_in_view() {
        const { assert!(TRAIL > 46.0) };
        const { assert!(TRAIL < strip::POSTER) };
    }
}
