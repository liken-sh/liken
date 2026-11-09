// The movie page's front layer: a scrolled stack of blocks over the
// backdrop and the scrim the page stacks under it. Every block is measured
// before anything draws, so a movie with no tagline, no set, or no credits
// leaves no hole where they would have been. The measure gives every block
// its place, and the focused block decides how far the stack has scrolled.
//
// The browser's strip draws the clock, the glass, and the people at the
// top right of the frame with no plate under them. A row that scrolled up
// under the strip would put the clock over a headshot, and no shade
// under the strip is dark enough to hide a face and light enough to keep
// the art. So a scrolled page shows its blocks only under the line
// `VIEWPORT_TOP`, and a row leaves the view at that line.

use crate::audience::faces::Faces;
use std::cell::RefCell;
use std::convert::Infallible;

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::{Point, Rectangle, Theme, mouse};

use super::super::Item;
use super::super::franchise::strips::Place;
use super::super::stripes::Face;
use super::{Focus, Movie, alike, row, watch};
use crate::art::Art;
use crate::clock::Time;
use crate::look;
use crate::views::across::Across;
use crate::views::stack::{self, Stack};
use crate::views::{
    area, band, buttons, card, curtain, header, people, ratings, screen, strip, text, watch as line,
};

// The share of the width the column of text takes. The column ends inside
// the part of the scrim that holds its full shade, so every line reads
// over the art whatever the art holds.
const COLUMN: f32 = 0.42;

// The space between two blocks.
const GAP: f32 = 16.0;

// The box a logo draws in, at the proportions the metadata tools write a
// logo file in.
const LOGO_WIDTH: f32 = 460.0;
const LOGO_HEIGHT: f32 = 128.0;

// The lines the plot is cut to.
const PLOT_LINES: usize = 4;

// How much of the block under the focused one the scroll keeps in
// view, as a share of a stripe, so a person sees that there is more
// below.
const TRAIL: f32 = 0.2;

// The space under the last stripe, so its caption lines sit clear of the
// bottom edge of the frame when the page has scrolled to its end.
const FOOT: f32 = 36.0;

// The extra space over a stripe and over the foot, on top of the gap
// between two blocks, so each stands clear of the block over it.
const STRIPE_LEAD: f32 = 16.0;

// The extra space over the files section, so its heading starts a part of
// the page of its own under the studios.
const FILES_LEAD: f32 = 40.0;

/// The top of the part of the frame a scrolled page shows, in logical
/// pixels: the line every wall's rows start under, which leaves the air of
/// a wall between the foot of the clock's line and the first row. The
/// scroll rests the focused block under it.
pub const VIEWPORT_TOP: f32 = band::HEIGHT;

/// The part of the frame the page shows at this scroll down. At rest it
/// is the whole frame, so the logo draws on the top margin beside the
/// clock. Its top edge moves down with the scroll until it reaches
/// `VIEWPORT_TOP`, so the head leaves the view as the page scrolls, and
/// it holds there, so no row draws under the strip.
pub fn shown(bounds: Rectangle, offset: f32) -> Rectangle {
    let top = offset.clamp(0.0, VIEWPORT_TOP).min(bounds.height);
    area(bounds.x, bounds.y + top, bounds.width, bounds.height - top)
}

/// The box the movie's logo draws in at these bounds, scroll included,
/// which is where the loading state starts the logo's move.
pub fn head(movie: &Movie, bounds: Rectangle) -> Rectangle {
    let (blocks, offset) = placed(movie, bounds);
    let at = blocks.title.at(offset);
    area(at.x, at.y, LOGO_WIDTH, LOGO_HEIGHT)
}

/// Every block of the page at these bounds, and how far the page scrolls
/// down to rest with the focused block in view.
pub(super) fn placed(movie: &Movie, bounds: Rectangle) -> (Blocks, f32) {
    let blocks = Blocks::of(movie, bounds);
    let offset = blocks.scroll(movie, bounds.height);
    (blocks, offset)
}

/// The foot of the playback row that starts at `at`, where the status
/// line under it starts.
pub(super) fn under(at: Point) -> Point {
    Point::new(at.x, at.y + buttons::HEIGHT)
}

/// The status line under the playback row, with the bar the width of
/// the text column, so the bar ends where the plot's lines end on every
/// row and the status never runs past the scrim's full shade. `focus` is
/// the mark that holds focus.
pub(super) fn status_line<'a>(
    movie: &Movie,
    status: &'a watch::Status,
    marks: &'a [(crate::views::icon::Icon, &'static str)],
    focus: Option<usize>,
) -> line::Line<'a> {
    line::Line {
        share: watch::share(movie.progress.as_ref()),
        status: &status.words,
        finished: status.finished,
        marks,
        focus,
        halo: false,
    }
}

/// The rows the page scrolls across, in the page's order: the set strip,
/// the franchise strips, the stripes of people, and the More like this
/// strip, at this scroll down
/// and with focus where `focus` puts it. Each row answers the scroll it
/// rests at, and draws at the scroll it is given.
pub(super) fn rows<'a>(
    movie: &'a Movie,
    blocks: &Blocks,
    bounds: Rectangle,
    offset: f32,
    focus: Option<Focus>,
) -> Vec<Across<'a, Item, Face>> {
    let region = screen::region(bounds);
    let place =
        |block: &Block, height: f32| area(region.x, block.top - offset, region.width, height);
    let mut rows = Vec::new();
    if let (Some(set), Some(block)) = (&movie.set, &blocks.strip) {
        rows.push(Across::Strip(strip::Strip {
            faces: None,
            room: None,
            members: &set.members,
            current: Some(set.current),
            kept: None,
            focus: match focus {
                Some(Focus::Strip(index)) => Some(index),
                _ => None,
            },
            heading: &set.heading,
            library: &movie.library,
            last: None,
            lines: card::LINES,
            headed: false,
            region: place(block, strip::height(card::LINES)),
            scroll: None,
        }));
    }
    let franchises = movie.franchises.bands().iter().zip(&blocks.franchises);
    for (index, (band, block)) in franchises.enumerate() {
        rows.push(Across::Strip(strip::Strip {
            faces: None,
            room: None,
            members: &band.members,
            current: band.current,
            kept: None,
            focus: match focus {
                Some(Focus::Franchise(strip, Place::Member(member))) if strip == index => {
                    Some(member)
                }
                _ => None,
            },
            heading: &band.heading,
            library: &movie.library,
            last: None,
            lines: card::LINES,
            headed: matches!(
                focus,
                Some(Focus::Franchise(strip, Place::Heading)) if strip == index
            ),
            region: place(block, strip::height(card::LINES)),
            scroll: None,
        }));
    }
    let stripes = movie.stripes.bands().iter().zip(&blocks.stripes);
    for (index, (band, block)) in stripes.enumerate() {
        rows.push(Across::Stripe(people::Stripe {
            people: &band.faces,
            focus: match focus {
                Some(Focus::Stripe(stripe, slot)) if stripe == index => Some(slot),
                _ => None,
            },
            heading: band.heading,
            library: &movie.library,
            region: place(block, people::HEIGHT),
            scroll: None,
        }));
    }
    if let (Some(like), Some(block)) = (&movie.like, &blocks.like) {
        rows.push(Across::Strip(strip::Strip {
            faces: None,
            room: None,
            members: &like.members,
            current: None,
            kept: None,
            focus: match focus {
                Some(Focus::Like(index)) => Some(index),
                _ => None,
            },
            heading: alike::HEADING,
            library: &movie.library,
            last: None,
            lines: card::LINES,
            headed: false,
            region: place(block, strip::height(card::LINES)),
            scroll: None,
        }));
    }
    rows
}

/// The box of the focused studio on the foot at this scroll down, or
/// nothing while focus is elsewhere.
pub(super) fn studio(
    movie: &Movie,
    blocks: &Blocks,
    bounds: Rectangle,
    offset: f32,
) -> Option<Rectangle> {
    let Focus::Studio(index) = movie.focus else {
        return None;
    };
    let region = screen::region(bounds);
    let at = Point::new(region.x, blocks.foot.top - offset);
    movie.foot.placed(at, region.width).get(index).copied()
}

/// The page's front layer as one canvas.
pub struct Page<'a, A> {
    /// The movie the page is about.
    pub movie: &'a Movie,
    /// The store the logo and the strip's posters come from.
    pub store: &'a RefCell<A>,
    /// The faces a film's poster draws along its foot.
    pub faces: &'a Faces,
    /// Whether the loading state has lifted the logo off the page, so the
    /// head leaves its box empty.
    pub lifted: bool,
    /// Whether the page holds focus, or the browser's strip over it does.
    pub held: bool,
    /// The second this frame draws, which the scrolls ease on.
    pub at: f64,
    /// The wall clock's reading, which the playback row names its end
    /// time from.
    pub now: Time,
}

impl<A: Art> canvas::Program<Infallible, Theme, Renderer> for Page<'_, A> {
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
        let (blocks, offset) = self.movie.drawn(bounds, self.at);
        frame.with_clip(shown(bounds, offset), |frame| {
            self.blocks(frame, bounds, &blocks, offset);
        });
        vec![frame.into_geometry()]
    }
}

impl<A: Art> Page<'_, A> {
    // Every block of the page at this scroll down.
    fn blocks(
        &self,
        frame: &mut canvas::Frame<Renderer>,
        bounds: Rectangle,
        blocks: &Blocks,
        offset: f32,
    ) {
        let movie = self.movie;
        let store = &mut *self.store.borrow_mut();
        let region = screen::region(bounds);
        let column = column(bounds);

        header::title(
            frame,
            store,
            &header::Title {
                library: &movie.library,
                logo: &movie.logo,
                name: &movie.title,
                at: blocks.title.at(offset),
                logo_box: (LOGO_WIDTH, LOGO_HEIGHT),
                decode: curtain::logo_decode(bounds),
                width: column,
                size: look::TITLE,
                lifted: self.lifted,
            },
        );

        // The tagline is the film's own words, so it draws in the italic,
        // as a card's tagline does.
        // The facts line is one line, cut with an ellipsis where a long
        // list of genres runs past the column, so it never ends on a comma.
        let facts = text::measured_cut(&movie.facts, look::FACTS, column);
        for (block, content, face, color) in [
            (
                blocks.facts,
                &facts,
                (look::FACTS, iced_winit::core::Font::with_name(look::FONT)),
                look::muted(),
            ),
            (
                blocks.tagline,
                &movie.tagline,
                (look::TAGLINE, look::ITALIC),
                look::text(),
            ),
        ] {
            text::line_in(frame, content, block.at(offset), face, color, column);
        }

        ratings::draw(frame, &movie.ratings, blocks.ratings.at(offset));

        text::block(
            frame,
            &movie.plot,
            blocks.plot.at(offset),
            look::PLOT,
            look::text(),
            column,
            PLOT_LINES,
        );

        // The page's focus while the page holds it, and none while the
        // browser's strip does, so a focused mark and a focused member
        // draw as focused only while the outline is on the page.
        let focus = self.held.then_some(movie.focus);

        let words = row::Button::drawn(&movie.buttons());
        let at = blocks.buttons.at(offset);
        buttons::draw(frame, &words, at);
        let names: Vec<&str> = words.iter().map(|(_, name)| *name).collect();
        buttons::draw_aside(
            frame,
            &names,
            at,
            &movie.ends(self.now),
            region.x + column,
            false,
        );
        let status = movie.status();
        let marks = watch::Mark::drawn(&movie.marks());
        let marked = match focus {
            Some(Focus::Marks(index)) => Some(index),
            _ => None,
        };
        line::draw(
            frame,
            &status_line(movie, &status, &marks, marked),
            under(at),
            column,
        );

        // Each row draws at the scroll its own ease has reached at this
        // second, toward the place this frame's focus rests it at.
        for (index, row) in rows(movie, blocks, bounds, offset, focus)
            .into_iter()
            .enumerate()
        {
            let scroll = movie.rows.at(index, row.scrolled(), self.at);
            row.at(scroll).faced(self.faces).draw(frame, store);
        }

        super::super::files::draw(
            frame,
            &movie.files,
            Point::new(region.x, blocks.files.top - offset),
            region.width,
        );

        movie.foot.draw(
            frame,
            Point::new(region.x, blocks.foot.top - offset),
            region.width,
        );
    }
}

// One block of the page: where it starts in the stack's own space, and
// how tall it is.
#[derive(Debug, Clone, Copy, PartialEq)]
pub(super) struct Block {
    left: f32,
    top: f32,
    height: f32,
}

impl Block {
    // Where the block draws at this scroll.
    pub(super) fn at(&self, offset: f32) -> Point {
        Point::new(self.left, self.top - offset)
    }

    fn bottom(&self) -> f32 {
        self.top + self.height
    }
}

// Every block of the page, measured before anything draws. A movie with
// no logo takes the height of its title text. A movie with a logo takes
// the box the logo draws in, so the blocks under it stand still while the
// decode lands.
pub(super) struct Blocks {
    title: Block,
    facts: Block,
    ratings: Block,
    tagline: Block,
    plot: Block,
    pub(super) buttons: Block,
    strip: Option<Block>,
    franchises: Vec<Block>,
    stripes: Vec<Block>,
    like: Option<Block>,
    foot: Block,
    files: Block,
    content: f32,
}

impl Blocks {
    fn of(movie: &Movie, bounds: Rectangle) -> Self {
        let region = screen::region(bounds);
        let column = column(bounds);
        let mut cursor = Stack::new(Point::new(region.x, screen::MARGIN_Y), GAP);
        let mut place = |lead: f32, height: f32| {
            cursor.skip(lead);
            let block = Block {
                left: region.x,
                top: cursor.at().y,
                height,
            };
            cursor.add(height);
            block
        };

        let title = place(
            0.0,
            match movie.logo.is_empty() {
                true => lines(&movie.title, look::TITLE, column, 0),
                false => LOGO_HEIGHT,
            },
        );
        let facts = place(0.0, text::height(1, look::FACTS));
        let ratings = place(
            0.0,
            match movie.ratings.is_empty() {
                true => 0.0,
                false => ratings::HEIGHT,
            },
        );
        let tagline = place(0.0, lines(&movie.tagline, look::TAGLINE, column, 0));
        let plot = place(0.0, lines(&movie.plot, look::PLOT, column, PLOT_LINES));
        let buttons = place(0.0, buttons::HEIGHT + line::height());
        let strip = movie
            .set
            .as_ref()
            .map(|_| place(0.0, strip::height(card::LINES)));
        let franchises: Vec<Block> = movie
            .franchises
            .bands()
            .iter()
            .map(|_| place(STRIPE_LEAD, strip::height(card::LINES)))
            .collect();
        let stripes: Vec<Block> = movie
            .stripes
            .bands()
            .iter()
            .map(|_| place(STRIPE_LEAD, people::HEIGHT))
            .collect();
        let like = movie
            .like
            .as_ref()
            .map(|_| place(STRIPE_LEAD, strip::height(card::LINES)));

        let foot = place(STRIPE_LEAD, movie.foot.height(region.width));
        let files = place(FILES_LEAD, movie.files.height());
        let last = like
            .or(stripes.last().copied())
            .or(franchises.last().copied())
            .or(strip)
            .unwrap_or(buttons);
        let end = [files, foot].into_iter().find(|block| block.height > 0.0);
        let content = match end {
            Some(block) => block.bottom() + FOOT,
            None => match stripes.is_empty() && franchises.is_empty() && like.is_none() {
                true => last.bottom(),
                false => last.bottom() + FOOT,
            },
        };
        Self {
            title,
            facts,
            ratings,
            tagline,
            plot,
            buttons,
            strip,
            franchises,
            stripes,
            like,
            foot,
            files,
            content,
        }
    }

    // How far the page has scrolled: enough to hold the focused
    // block and the head of the block under it, so a person sees that
    // there is more below. The viewport the rule keeps them in starts at
    // `VIEWPORT_TOP`, so the focused block never rests under the strip.
    fn scroll(&self, movie: &Movie, height: f32) -> f32 {
        let block = match movie.focus {
            Focus::Stripe(stripe, _) => self.stripes.get(stripe).copied(),
            Focus::Franchise(strip, _) => self.franchises.get(strip).copied(),
            Focus::Strip(_) => self.strip,
            Focus::Like(_) => self.like,
            Focus::Studio(_) => Some(self.foot),
            Focus::Buttons(_) | Focus::Marks(_) => None,
        }
        .unwrap_or(self.buttons);
        let tail = (self.after(block) - block.bottom() + TRAIL * people::HEIGHT)
            .min(self.content - block.bottom());
        let under = area(0.0, block.top - VIEWPORT_TOP, 0.0, block.height);
        stack::offset(
            under,
            tail,
            self.content - VIEWPORT_TOP,
            height - VIEWPORT_TOP,
        )
    }

    // The top of the first block under this one, and the foot of
    // the page where nothing follows it.
    fn after(&self, block: Block) -> f32 {
        self.strip
            .into_iter()
            .chain(self.franchises.iter().copied())
            .chain(self.stripes.iter().copied())
            .chain(self.like)
            .map(|under| under.top)
            .find(|top| *top > block.top)
            .unwrap_or(self.content)
    }
}

// The column of text is a share of the whole frame's width, and not of
// the content region's, so it ends inside the scrim's full shade.
fn column(bounds: Rectangle) -> f32 {
    bounds.width * COLUMN
}

// The height a block of text takes at this size and width, cut to `cap`
// lines where the caller cuts it, and zero where the movie carries no
// such line. A cut block is counted by the lines `text::block` draws,
// so the row under the plot starts where the plot's last line ends.
fn lines(content: &str, size: f32, width: f32, cap: usize) -> f32 {
    match cap {
        0 => text::height(text::lines(content, size, width), size),
        cap => text::block_height(content, size, width, cap),
    }
}

#[cfg(test)]
mod tests;
