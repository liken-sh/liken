// The home page: the screen the shade lifts to, and the bottom of the
// stack. It draws the band across the top, then the rows top to bottom:
// the banner, what was released, what arrived, the strips the day drew
// from the pool, the libraries as the floor the page never loses, and
// every genre to close the page. A row that holds nothing draws nothing,
// and focus skips it.

pub mod banner;
mod layout;
mod page;
mod recent;

use std::cell::RefCell;
use std::convert::Infallible;

use iced_wgpu::Renderer;
use iced_widget::{Stack, canvas};
use iced_winit::core::{Element, Length, Rectangle, Theme, mouse};

use self::banner::Banner;
use self::layout::Layout;
pub use self::page::{Page, read};
use super::wall::Wall;
use super::{Item, Screen, Step, facts, slots};
use crate::catalog::draw::Date;
use crate::catalog::pool::Candidate;
use crate::catalog::recency::SHOWN;
use crate::catalog::{Fold, GenreEntry, LibraryEntry, Order, Query, Source, library_name};
use crate::focus;
use crate::posters::Posters;
use crate::views::{self, area, band, strip};

// The band's heading on the home page is the word "Home" and not the
// namespace, because a namespace is a cluster's word and the person on
// the couch has one home.
const HEADING: &str = "Home";

// The kind an item of the libraries strip carries, so a select on it
// opens the library's wall and never a page.
const LIBRARY: &str = "library";

// The kind an item of the genres strip carries, so a select on it opens
// the genre's page and never a title's.
const GENRE: &str = "genre";

/// One row of the page as a read: the banner, the slots of one query,
/// the libraries themselves, or the genres themselves.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Row {
    Banner,
    Query(Query),
    Libraries,
    Genres,
}

impl Row {
    // Whether the row is one of the two recency strips. They are told
    // apart because they feed the banner after the drawn strips, in the
    // page's order.
    fn recency(&self) -> bool {
        matches!(
            self,
            Self::Query(Query::Released { .. } | Query::Added { .. })
        )
    }
}

// The rows of the page, top to bottom: the banner, the two recency
// strips under the `Airing` fold because the home page shows what is
// new, the strips the day drew in the drawn order, the libraries, and
// the genres to close the page.
fn rows(drawn: Vec<Candidate>) -> Vec<Row> {
    let mut rows = vec![
        Row::Banner,
        Row::Query(Query::Released { fold: Fold::Airing }),
        Row::Query(Query::Added { fold: Fold::Airing }),
    ];
    rows.extend(
        drawn
            .into_iter()
            .map(|candidate| Row::Query(candidate.query)),
    );
    rows.push(Row::Libraries);
    rows.push(Row::Genres);
    rows
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
    // A strip of this row with nothing read yet. Every strip is built empty
    // and read in the page's order, because the added strip reads what the
    // released strip shows.
    fn new(row: Row) -> Self {
        Self {
            heading: String::new(),
            items: Vec::new(),
            focus: 0,
            see_all: matches!(row, Row::Query(_)),
            lines: 2,
            row,
        }
    }

    // Read the strip's row again and keep focus in range. A query strip
    // shows the first `SHOWN` slots and leaves the rest to the wall. The
    // released strip keeps the window of today, and the added strip drops
    // what the released strip shows.
    fn reread(&mut self, source: &mut dyn Source, today: i64, released: &[Item]) {
        match &self.row {
            Row::Query(query) => {
                let answer = source.wall(query);
                self.heading = query.name(&answer.name);
                let slots = match query {
                    Query::Released { .. } => recent::released(answer.slots, today),
                    Query::Added { .. } => recent::added(answer.slots, released),
                    _ => answer.slots.into_iter().take(SHOWN).collect(),
                };
                self.items = slots
                    .into_iter()
                    .map(|slot| Item::of(query, slot))
                    .collect();
            }
            // The genres row reads every genre. It has no "see all", because
            // it is all of them and not a draw.
            Row::Genres => {
                self.heading = "Genres".to_string();
                self.items = source.genres().into_iter().map(genre_item).collect();
            }
            // The libraries row reads the libraries. The banner row is here only
            // because the match is exhaustive: a strip never carries it, because
            // the banner is read off the strips and not from a row of its own.
            Row::Libraries | Row::Banner => {
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

    // What a select opens. "See all" opens the page the strip is about:
    // a person's own page for a person's strip, and the wall of everything
    // the query answers for every other. A library opens its wall, and a
    // title opens its page by its kind.
    fn select(&self, source: &mut dyn Source) -> Step {
        let Some(item) = self.focused() else {
            return match &self.row {
                Row::Query(query) if self.see_all => slots::see_all(query, source),
                _ => Step::Stay,
            };
        };
        if item.kind == LIBRARY {
            let query = Query::Library {
                library: item.library.clone(),
            };
            return Step::Open(Screen::Wall(Wall::open(query, source)));
        }
        if item.kind == GENRE {
            let query = Query::Genre {
                name: item.id.clone(),
                order: Order::Released,
            };
            return slots::see_all(&query, source);
        }
        slots::opened(item, source)
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

// One genre as a slot of the genres strip: the genre as the caption,
// the count of its titles under it, and the poster of its newest-released
// title as the art. Two genres may share one poster.
fn genre_item(entry: GenreEntry) -> Item {
    let titles = match entry.titles {
        1 => "1 title".to_string(),
        titles => format!("{titles} titles"),
    };
    Item {
        id: entry.name.clone(),
        library: entry.library,
        kind: GENRE.to_string(),
        caption: entry.name.clone(),
        line: facts::Line::of(&[&entry.name]),
        name: entry.name,
        under: titles,
        art: entry.art,
        episode: None,
    }
}

/// One row of the page as it is read: the banner, or one strip.
#[derive(Debug)]
pub enum Block {
    Banner(Banner),
    Strip(Strip),
}

impl Block {
    // The empty block one row reads into. Nothing is read here, because the
    // strips are read in the page's order, and the banner off the strips
    // after them.
    fn new(row: Row) -> Self {
        match row {
            Row::Banner => Self::Banner(Banner::default()),
            row => Self::Strip(Strip::new(row)),
        }
    }

    fn row(&self) -> Row {
        match self {
            Self::Banner(_) => Row::Banner,
            Self::Strip(strip) => strip.row.clone(),
        }
    }

    fn is_empty(&self) -> bool {
        match self {
            Self::Banner(banner) => banner.is_empty(),
            Self::Strip(strip) => strip.is_empty(),
        }
    }

    /// The strip this block is, or nothing for the banner.
    pub fn strip(&self) -> Option<&Strip> {
        match self {
            Self::Strip(strip) => Some(strip),
            Self::Banner(_) => None,
        }
    }
}

/// The home page: the heading as the band draws it, the control that
/// holds focus or nothing while a row holds it, the rows in the page's
/// order, and the row that holds focus.
#[derive(Debug)]
pub struct Home {
    pub heading: String,
    pub control: Option<usize>,
    pub blocks: Vec<Block>,
    pub focus: usize,
}

impl Home {
    /// Read every row, with focus on the first row that holds anything.
    pub fn open(source: &mut dyn Source) -> Self {
        let mut home = Self {
            heading: HEADING.to_string(),
            control: None,
            blocks: Vec::new(),
            focus: 0,
        };
        home.apply(read(source, Date::today()));
        home
    }

    /// Read every strip again and keep focus where it was, because a change
    /// can empty the strip that held it. The draw runs again, so a strip the
    /// day no longer draws goes, a new one is read, and a strip that stays
    /// keeps its focus. Focus follows the row it was on where that row
    /// stays.
    pub fn reread(&mut self, source: &mut dyn Source) {
        self.apply(read(source, Date::today()));
    }

    /// Take a page the reader answered. A row the page holds and the
    /// screen already has keeps its focus, focus follows the row it was on
    /// where that row stays, and a strip the day no longer draws goes.
    pub fn apply(&mut self, page: Page) {
        let focused = self.blocks.get(self.focus).map(Block::row);
        let mut banner: Option<Banner> = None;
        let mut kept: Vec<Strip> = Vec::new();
        for block in std::mem::take(&mut self.blocks) {
            match block {
                Block::Banner(held) => banner = Some(held),
                Block::Strip(strip) => kept.push(strip),
            }
        }
        self.blocks = page
            .blocks
            .into_iter()
            .map(|block| match block {
                Block::Banner(fresh) => {
                    let mut held = banner.take().unwrap_or_default();
                    held.reread(fresh.titles);
                    Block::Banner(held)
                }
                Block::Strip(mut fresh) => {
                    if let Some(index) = kept.iter().position(|strip| strip.row == fresh.row) {
                        let held = kept.remove(index);
                        fresh.focus = held.focus.min(fresh.count().saturating_sub(1));
                    }
                    Block::Strip(fresh)
                }
            })
            .collect();
        if let Some(index) =
            focused.and_then(|row| self.blocks.iter().position(|block| block.row() == row))
        {
            self.focus = index;
        }
        self.settle();
    }

    /// The banner the page holds, or nothing where the rows name none.
    pub fn banner(&self) -> Option<&Banner> {
        self.blocks.iter().find_map(|block| match block {
            Block::Banner(banner) => Some(banner),
            Block::Strip(_) => None,
        })
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
        self.blocks
            .get(index)
            .is_some_and(|block| !block.is_empty())
    }

    // The nearest strip above this one that holds anything.
    fn above(&self, index: usize) -> Option<usize> {
        (0..index).rev().find(|index| self.holds(*index))
    }

    // The nearest strip below this one that holds anything.
    fn below(&self, index: usize) -> Option<usize> {
        (index + 1..self.blocks.len()).find(|index| self.holds(*index))
    }

    /// Fold one press in. In the band, left and right move across the
    /// controls, select does nothing, and down returns to the row the page
    /// remembers. On the rows, up and down move between them, up from the
    /// first reaches the band, left and right move inside one, and select
    /// opens what the row names.
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
            _ => match self.blocks.get_mut(self.focus) {
                Some(Block::Banner(banner)) => return banner.key(key, source),
                Some(Block::Strip(strip)) if key == "enter" => return strip.select(source),
                Some(Block::Strip(strip)) => {
                    strip.focus = focus::row(strip.focus, strip.count(), key);
                }
                None => {}
            },
        }
        Step::Stay
    }

    /// Whether a rest of focus on this page is worth a prefetch: while the
    /// banner or a title holds focus, because a select opens a page over a
    /// backdrop.
    pub fn prefetches(&self) -> bool {
        self.control.is_none()
            && match self.blocks.get(self.focus) {
                Some(Block::Banner(banner)) => !banner.is_empty(),
                Some(Block::Strip(strip)) => strip
                    .focused()
                    .is_some_and(|item| item.kind != LIBRARY && item.kind != GENRE),
                None => false,
            }
    }

    /// The library and the backdrop the focused title's page draws over,
    /// so the store decodes it while focus rests.
    pub fn resting(&self, source: &mut dyn Source) -> Option<(String, String)> {
        if !self.prefetches() {
            return None;
        }
        match self.blocks.get(self.focus)? {
            Block::Banner(banner) => banner.resting(),
            Block::Strip(strip) => slots::backdrop(strip.focused()?, source),
        }
    }

    /// The view, in two layers: the banner's backdrop, then the band and the
    /// rows over it. A mesh draws under every image of its layer, so the
    /// banner's scrim needs the backdrop on a layer of its own.
    pub fn view<'a, P: Posters>(
        &'a self,
        posters: &'a RefCell<P>,
    ) -> Element<'a, Infallible, Theme, Renderer> {
        let ground = canvas(Ground {
            home: self,
            posters,
        })
        .width(Length::Fill)
        .height(Length::Fill)
        .into();
        let front = canvas(Program {
            home: self,
            posters,
        })
        .width(Length::Fill)
        .height(Length::Fill)
        .into();
        Stack::with_children(vec![ground, front])
            .width(Length::Fill)
            .height(Length::Fill)
            .into()
    }

    // The layout and the scroll at these bounds, which both layers draw
    // from.
    fn placed(&self, bounds: Rectangle) -> (Layout, f32, Rectangle) {
        let layout = Layout::of(self, bounds.height);
        let viewport = bounds.height - band::HEIGHT;
        let offset = layout.scroll(self, bounds.height, viewport);
        let clip = area(0.0, band::HEIGHT, bounds.width, viewport);
        (layout, offset, clip)
    }
}

// The under layer: the banner's backdrop alone, clipped under the
// band.
struct Ground<'a, P> {
    home: &'a Home,
    posters: &'a RefCell<P>,
}

impl<P: Posters> canvas::Program<Infallible, Theme, Renderer> for Ground<'_, P> {
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
        let (layout, offset, clip) = home.placed(bounds);
        frame.with_clip(clip, |frame| {
            let posters = &mut *self.posters.borrow_mut();
            for (index, block) in home.blocks.iter().enumerate() {
                let Block::Banner(banner) = block else {
                    continue;
                };
                let Some(title) = banner.focused() else {
                    continue;
                };
                let Some(region) = layout.region(home, index, offset, bounds.width, bounds.height)
                else {
                    continue;
                };
                views::banner::backdrop(
                    frame,
                    posters,
                    &title.item.library,
                    &title.backdrop,
                    region,
                );
            }
        });
        vec![frame.into_geometry()]
    }
}

// The over layer: the band over the rows, on one frame.
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

        let (layout, offset, clip) = home.placed(bounds);
        frame.with_clip(clip, |frame| {
            let posters = &mut *self.posters.borrow_mut();
            for (index, block) in home.blocks.iter().enumerate() {
                let Some(region) = layout.region(home, index, offset, bounds.width, bounds.height)
                else {
                    continue;
                };
                if region.y + region.height < band::HEIGHT || region.y > bounds.height {
                    continue;
                }
                let focused = home.control.is_none() && home.focus == index;
                match block {
                    Block::Banner(banner) => {
                        let Some(title) = banner.focused() else {
                            continue;
                        };
                        views::banner::draw(
                            frame,
                            posters,
                            &views::banner::Banner {
                                library: &title.item.library,
                                logo: &title.logo,
                                name: &title.name,
                                facts: &title.facts,
                                genres: &title.genres,
                                ratings: &title.ratings,
                                tagline: &title.tagline,
                                count: banner.titles.len(),
                                current: banner.focus,
                                focused,
                                region,
                            },
                        );
                    }
                    Block::Strip(strip) => strip::draw(
                        frame,
                        posters,
                        &strip::Strip {
                            members: &strip.items,
                            current: None,
                            focus: focused.then_some(strip.focus),
                            heading: &strip.heading,
                            library: "",
                            see_all: strip.see_all,
                            lines: strip.lines,
                            region,
                        },
                    ),
                }
            }
        });
        vec![frame.into_geometry()]
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn the_page_reads_the_banner_the_recency_rows_the_draw_the_libraries_then_the_genres() {
        let western = Query::Genre {
            name: "Western".into(),
            order: crate::catalog::Order::Released,
        };
        let drawn = vec![Candidate {
            query: western.clone(),
            name: "Western".into(),
            weight: 7,
        }];
        assert_eq!(
            rows(drawn),
            [
                Row::Banner,
                Row::Query(Query::Released { fold: Fold::Airing }),
                Row::Query(Query::Added { fold: Fold::Airing }),
                Row::Query(western),
                Row::Libraries,
                Row::Genres,
            ]
        );
        assert_eq!(rows(Vec::new()).len(), 5);
    }

    #[test]
    fn only_the_two_recency_rows_are_recency() {
        assert!(Row::Query(Query::Released { fold: Fold::Airing }).recency());
        assert!(Row::Query(Query::Added { fold: Fold::Titles }).recency());
        assert!(!Row::Banner.recency());
        assert!(!Row::Libraries.recency());
        assert!(!Row::Genres.recency());
        assert!(
            !Row::Query(Query::Library {
                library: "sample/features".into()
            })
            .recency()
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
    fn a_genre_is_a_slot_with_its_name_its_count_and_its_newest_poster() {
        let item = genre_item(GenreEntry {
            name: "Western".into(),
            titles: 42,
            library: "screening/features".into(),
            art: "posters/newest.jpg".into(),
        });
        assert_eq!(item.kind, GENRE);
        assert_eq!(item.id, "Western");
        assert_eq!(item.library, "screening/features");
        assert_eq!(item.name, "Western");
        assert_eq!(item.caption, "Western");
        assert_eq!(item.line.words(), "Western");
        assert_eq!(item.under, "42 titles");
        assert_eq!(item.art, "posters/newest.jpg");
        assert_eq!(item.episode, None);
    }

    #[test]
    fn a_genre_one_title_carries_counts_it_in_the_singular() {
        let item = genre_item(GenreEntry {
            name: "Silent".into(),
            titles: 1,
            library: String::new(),
            art: String::new(),
        });
        assert_eq!(item.under, "1 title");
        assert_eq!(item.art, "");
    }
}
