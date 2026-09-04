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
mod rows;

use std::cell::RefCell;
use std::convert::Infallible;

use iced_wgpu::Renderer;
use iced_widget::{Stack, canvas};
use iced_winit::core::{Element, Length, Rectangle, Theme, mouse};

use self::banner::Banner;
use self::layout::Layout;
pub use self::page::{Page, read};
use self::rows::{GENRE, LIBRARY, rows};
pub use self::rows::{Row, Strip};
use super::{Step, slots};
use crate::catalog::Source;
use crate::catalog::draw::Date;
use crate::focus;
use crate::posters::Posters;
use crate::views::{self, area, band, strip};

// The band's heading on the home page is the word "Home" and not the
// namespace, because a namespace is a cluster's word and the person on
// the couch has one home.
const HEADING: &str = "Home";

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
                _ => self.control = Some(focus::row(control, band::SEARCH_ONLY.len(), key)),
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

    /// The view, in three layers: the banner's backdrop, the rows over it,
    /// and the band over both. A mesh draws under every image of its layer,
    /// so the banner's scrim needs the backdrop on a layer of its own, and
    /// the band needs a layer of its own so a row that scrolled up under
    /// it never shows through.
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
        let band = band::layer(&self.heading, &band::SEARCH_ONLY, self.control);
        Stack::with_children(vec![ground, front, band])
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

// The middle layer: the rows, on one frame.
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
                            last: strip.last.as_deref(),
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
