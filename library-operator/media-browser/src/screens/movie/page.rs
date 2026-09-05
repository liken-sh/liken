// The movie page's front layer: a scrolled stack of blocks over the
// backdrop and the scrim the page stacks under it. Every block is measured
// before anything draws, so a movie with no tagline, no set, or no credits
// leaves no hole where they would have been. The measure gives every block
// its place, and the focused block decides how far the stack has scrolled.

use std::cell::RefCell;
use std::convert::Infallible;

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::{Point, Rectangle, Theme, mouse};

use super::super::franchise::strips::Place;
use super::{Focus, Movie};
use crate::look;
use crate::posters::Posters;
use crate::views::stack::{self, Stack};
use crate::views::{area, buttons, header, people, ratings, strip, text};

// The margin at both sides of the page.
const MARGIN: f32 = 120.0;

// The share of the width the column of text takes. The column ends inside
// the part of the scrim that holds its full shade, so every line reads
// over the art whatever the art holds.
const COLUMN: f32 = 0.42;

// The share of the height above the first block.
const TOP: f32 = 0.12;

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

/// The box the movie's logo draws in at these bounds, scroll included,
/// which is where the loading state starts the logo's move.
pub fn head(movie: &Movie, bounds: Rectangle) -> Rectangle {
    let blocks = Blocks::of(
        movie,
        bounds.width * COLUMN,
        bounds.width - 2.0 * MARGIN,
        bounds.height * TOP,
    );
    let offset = blocks.scroll(movie, bounds.height);
    area(MARGIN, blocks.title.top - offset, LOGO_WIDTH, LOGO_HEIGHT)
}

/// The page's front layer as one canvas.
pub struct Page<'a, P> {
    /// The movie the page is about.
    pub movie: &'a Movie,
    /// The store the logo and the strip's posters come from.
    pub posters: &'a RefCell<P>,
    /// Whether the loading state has lifted the logo off the page, so the
    /// head leaves its box empty.
    pub lifted: bool,
}

impl<P: Posters> canvas::Program<Infallible, Theme, Renderer> for Page<'_, P> {
    type State = ();

    fn draw(
        &self,
        _state: &Self::State,
        renderer: &Renderer,
        _theme: &Theme,
        bounds: Rectangle,
        _cursor: mouse::Cursor,
    ) -> Vec<canvas::Geometry<Renderer>> {
        let movie = self.movie;
        let mut frame = canvas::Frame::new(renderer, bounds.size());
        let posters = &mut *self.posters.borrow_mut();

        let column = bounds.width * COLUMN;
        let blocks = Blocks::of(
            movie,
            column,
            bounds.width - 2.0 * MARGIN,
            bounds.height * TOP,
        );
        let offset = blocks.scroll(movie, bounds.height);

        header::title(
            &mut frame,
            posters,
            &header::Title {
                library: &movie.library,
                logo: &movie.logo,
                name: &movie.title,
                at: blocks.title.at(offset),
                logo_box: (LOGO_WIDTH, LOGO_HEIGHT),
                width: column,
                size: look::TITLE,
                lifted: self.lifted,
            },
        );

        for (block, content, size, color) in [
            (blocks.facts, &movie.facts, look::FACTS, look::muted()),
            (blocks.tagline, &movie.tagline, look::TAGLINE, look::text()),
        ] {
            text::line(&mut frame, content, block.at(offset), size, color, column);
        }

        ratings::draw(&mut frame, &movie.ratings, blocks.ratings.at(offset));

        text::block(
            &mut frame,
            &movie.plot,
            blocks.plot.at(offset),
            look::PLOT,
            look::text(),
            column,
            PLOT_LINES,
        );

        buttons::draw(
            &mut frame,
            movie.buttons(),
            blocks.buttons.at(offset),
            match movie.focus {
                Focus::Buttons(index) => Some(index),
                _ => None,
            },
        );

        if let (Some(set), Some(block)) = (&movie.set, blocks.strip) {
            strip::draw(
                &mut frame,
                posters,
                &strip::Strip {
                    members: &set.members,
                    current: Some(set.current),
                    focus: match movie.focus {
                        Focus::Strip(index) => Some(index),
                        _ => None,
                    },
                    heading: &set.heading,
                    library: &movie.library,
                    last: None,
                    lines: 0,
                    headed: false,
                    region: area(
                        MARGIN,
                        block.top - offset,
                        bounds.width - 2.0 * MARGIN,
                        strip::height(0),
                    ),
                },
            );
        }

        for (index, (band, block)) in movie
            .franchises
            .bands()
            .iter()
            .zip(&blocks.franchises)
            .enumerate()
        {
            strip::draw(
                &mut frame,
                posters,
                &strip::Strip {
                    members: &band.members,
                    current: band.current,
                    focus: match movie.focus {
                        Focus::Franchise(strip, Place::Member(member)) if strip == index => {
                            Some(member)
                        }
                        _ => None,
                    },
                    heading: &band.heading,
                    library: &movie.library,
                    last: None,
                    lines: 1,
                    headed: matches!(
                        movie.focus,
                        Focus::Franchise(strip, Place::Heading) if strip == index
                    ),
                    region: area(
                        MARGIN,
                        block.top - offset,
                        bounds.width - 2.0 * MARGIN,
                        strip::height(1),
                    ),
                },
            );
        }

        for (index, (band, block)) in movie
            .stripes
            .bands()
            .iter()
            .zip(&blocks.stripes)
            .enumerate()
        {
            people::draw(
                &mut frame,
                posters,
                &people::Stripe {
                    people: &band.faces,
                    focus: match movie.focus {
                        Focus::Stripe(stripe, slot) if stripe == index => Some(slot),
                        _ => None,
                    },
                    heading: band.heading,
                    library: &movie.library,
                    region: area(
                        MARGIN,
                        block.top - offset,
                        bounds.width - 2.0 * MARGIN,
                        people::HEIGHT,
                    ),
                },
            );
        }

        let mut at = Point::new(MARGIN, blocks.foot.top - offset);
        let width = bounds.width - 2.0 * MARGIN;
        for row in movie.foot.rows() {
            at.y += row.lead;
            let color = match row.faint {
                true => look::faint(),
                false => look::text(),
            };
            text::line(&mut frame, row.prefix, at, row.size, look::faint(), width);
            let after = Point::new(at.x + row.indent(), at.y);
            at.y += text::line(
                &mut frame,
                row.content,
                after,
                row.size,
                color,
                width - row.indent(),
            );
        }

        vec![frame.into_geometry()]
    }
}

// One block of the page: where it starts in the stack's own space, and
// how tall it is.
#[derive(Debug, Clone, Copy, PartialEq)]
struct Block {
    top: f32,
    height: f32,
}

impl Block {
    // Where the block draws at this scroll.
    fn at(&self, offset: f32) -> Point {
        Point::new(MARGIN, self.top - offset)
    }

    fn bottom(&self) -> f32 {
        self.top + self.height
    }

    fn region(&self) -> Rectangle {
        area(0.0, self.top, 0.0, self.height)
    }
}

// Every block of the page, measured before anything draws. A movie with
// no logo takes the height of its title text. A movie with a logo takes
// the box the logo draws in, so the blocks under it stand still while the
// decode lands.
struct Blocks {
    title: Block,
    facts: Block,
    ratings: Block,
    tagline: Block,
    plot: Block,
    buttons: Block,
    strip: Option<Block>,
    franchises: Vec<Block>,
    stripes: Vec<Block>,
    foot: Block,
    content: f32,
}

impl Blocks {
    fn of(movie: &Movie, column: f32, width: f32, top: f32) -> Self {
        let mut cursor = Stack::new(Point::new(MARGIN, top), GAP);
        let mut place = |lead: f32, height: f32| {
            cursor.skip(lead);
            let block = Block {
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
        let facts = place(0.0, lines(&movie.facts, look::FACTS, column, 0));
        let ratings = place(
            0.0,
            match movie.ratings.is_empty() {
                true => 0.0,
                false => ratings::HEIGHT,
            },
        );
        let tagline = place(0.0, lines(&movie.tagline, look::TAGLINE, column, 0));
        let plot = place(0.0, lines(&movie.plot, look::PLOT, column, PLOT_LINES));
        let buttons = place(0.0, buttons::HEIGHT);
        let strip = movie.set.as_ref().map(|_| place(0.0, strip::height(0)));
        let franchises: Vec<Block> = movie
            .franchises
            .bands()
            .iter()
            .map(|_| place(STRIPE_LEAD, strip::height(1)))
            .collect();
        let stripes: Vec<Block> = movie
            .stripes
            .bands()
            .iter()
            .map(|_| place(STRIPE_LEAD, people::HEIGHT))
            .collect();

        let foot = place(STRIPE_LEAD, movie.foot.height(width));
        let last = stripes
            .last()
            .or(franchises.last())
            .copied()
            .or(strip)
            .unwrap_or(buttons);
        let content = match foot.height > 0.0 {
            true => foot.bottom() + FOOT,
            false => match stripes.is_empty() && franchises.is_empty() {
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
            foot,
            content,
        }
    }

    // How far the page has scrolled: enough to hold the focused
    // block and the head of the block under it, so a person sees that
    // there is more below.
    fn scroll(&self, movie: &Movie, height: f32) -> f32 {
        let block = match movie.focus {
            Focus::Stripe(stripe, _) => self.stripes.get(stripe).copied(),
            Focus::Franchise(strip, _) => self.franchises.get(strip).copied(),
            Focus::Strip(_) => self.strip,
            Focus::Buttons(_) => None,
        }
        .unwrap_or(self.buttons);
        let tail = (self.after(block) - block.bottom() + TRAIL * people::HEIGHT)
            .min(self.content - block.bottom());
        stack::offset(block.region(), tail, self.content, height)
    }

    // The top of the first block under this one, and the foot of
    // the page where nothing follows it.
    fn after(&self, block: Block) -> f32 {
        self.strip
            .into_iter()
            .chain(self.franchises.iter().copied())
            .chain(self.stripes.iter().copied())
            .map(|under| under.top)
            .find(|top| *top > block.top)
            .unwrap_or(self.content)
    }
}

// The height a block of text takes at this size and width, cut to `cap`
// lines where the caller cuts it, and zero where the movie carries no
// such line.
fn lines(content: &str, size: f32, width: f32, cap: usize) -> f32 {
    let taken = text::lines(content, size, width);
    match cap {
        0 => text::height(taken, size),
        cap => text::height(taken.min(cap), size),
    }
}

#[cfg(test)]
mod tests;
