// The movie page's front layer: a scrolled stack of blocks over the
// backdrop and the scrim the page stacks under it. Every block is measured
// before anything draws, so a movie with no tagline, no set, or no credits
// leaves no hole where they would have been. The measure gives every block
// its place, the focused block decides how far the stack has scrolled, and
// the credits under the strip come into view with it.

use std::cell::RefCell;
use std::convert::Infallible;

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::{Point, Rectangle, Theme, mouse};

use super::{Focus, Movie};
use crate::look;
use crate::posters::Posters;
use crate::views::stack::{self, Stack};
use crate::views::{area, buttons, header, strip, text};

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

// The lines the plot is cut to, and the lines the cast is cut to.
const PLOT_LINES: usize = 4;
const CAST_LINES: usize = 2;

/// The page's front layer as one canvas.
pub struct Page<'a, P> {
    /// The movie the page is about.
    pub movie: &'a Movie,
    /// The store the logo and the strip's posters come from.
    pub posters: &'a RefCell<P>,
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
        let blocks = Blocks::of(movie, column, bounds.height * TOP);
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
            },
        );

        for (block, content, size, color) in [
            (blocks.facts, &movie.facts, look::FACTS, look::muted()),
            (blocks.tagline, &movie.tagline, look::TAGLINE, look::text()),
        ] {
            text::line(&mut frame, content, block.at(offset), size, color, column);
        }

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
                Focus::Strip(_) => None,
            },
        );

        if let (Some(set), Some(block)) = (&movie.set, blocks.strip) {
            strip::draw(
                &mut frame,
                posters,
                &strip::Strip {
                    members: &set.members,
                    current: set.current,
                    focus: match movie.focus {
                        Focus::Strip(index) => Some(index),
                        Focus::Buttons(_) => None,
                    },
                    heading: &set.heading,
                    library: &movie.library,
                    region: area(
                        MARGIN,
                        block.top - offset,
                        bounds.width - 2.0 * MARGIN,
                        strip::HEIGHT,
                    ),
                },
            );
        }

        for (block, content) in [
            (blocks.directed, &movie.directed),
            (blocks.written, &movie.written),
        ] {
            text::line(
                &mut frame,
                content,
                block.at(offset),
                look::CREDITS,
                look::muted(),
                column,
            );
        }
        text::block(
            &mut frame,
            &movie.cast,
            blocks.cast.at(offset),
            look::CREDITS,
            look::muted(),
            column,
            CAST_LINES,
        );

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
    tagline: Block,
    plot: Block,
    buttons: Block,
    strip: Option<Block>,
    directed: Block,
    written: Block,
    cast: Block,
    content: f32,
}

impl Blocks {
    fn of(movie: &Movie, column: f32, top: f32) -> Self {
        let mut cursor = Stack::new(Point::new(MARGIN, top), GAP);
        let mut place = |height: f32| {
            let block = Block {
                top: cursor.at().y,
                height,
            };
            cursor.add(height);
            block
        };

        let title = place(match movie.logo.is_empty() {
            true => lines(&movie.title, look::TITLE, column, 0),
            false => LOGO_HEIGHT,
        });
        let facts = place(lines(&movie.facts, look::FACTS, column, 0));
        let tagline = place(lines(&movie.tagline, look::TAGLINE, column, 0));
        let plot = place(lines(&movie.plot, look::PLOT, column, PLOT_LINES));
        let buttons = place(buttons::HEIGHT);
        let strip = movie.set.as_ref().map(|_| place(strip::HEIGHT));
        let directed = place(lines(&movie.directed, look::CREDITS, column, 0));
        let written = place(lines(&movie.written, look::CREDITS, column, 0));
        let cast = place(lines(&movie.cast, look::CREDITS, column, CAST_LINES));

        Self {
            title,
            facts,
            tagline,
            plot,
            buttons,
            strip,
            directed,
            written,
            cast,
            content: cast.bottom(),
        }
    }

    // How far the page has scrolled. The credits take no focus, so they
    // are the tail of the last block a press can reach.
    fn scroll(&self, movie: &Movie, height: f32) -> f32 {
        let (block, next) = match (movie.focus, self.strip) {
            (Focus::Strip(_), Some(strip)) => (strip, self.content),
            (_, Some(strip)) => (self.buttons, strip.top),
            _ => (self.buttons, self.content),
        };
        stack::offset(block.region(), next - block.bottom(), self.content, height)
    }

    // The blocks from the strip down: the last row a press can reach and
    // the credits under it, which come into view with it.
    #[cfg(test)]
    fn strip_and_under(&self) -> Vec<Block> {
        let mut blocks = vec![self.directed, self.written, self.cast];
        blocks.extend(self.strip);
        blocks
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
mod tests {
    use super::*;
    use crate::screens::movie::Set;
    use crate::screens::{Item, facts};

    const WIDTH: f32 = 1920.0;
    const HEIGHT: f32 = 1080.0;

    // A 720p frame, which a crowded page is taller than, so the scroll
    // has something to do.
    const SHORT: f32 = 720.0;

    // A page with everything on it: a title that wraps onto a second
    // line, a tagline, a plot of four lines, a set strip, a writers
    // credit that wraps, and a cast of two lines.
    fn crowded(focus: Focus) -> Movie {
        Movie {
            library: "screening/films".into(),
            id: "one".into(),
            title: "A Long Title That Wraps Onto Two".into(),
            logo: String::new(),
            backdrop: "backdrop.jpg".into(),
            trailer: true,
            facts: "1994 · 1h 52m · PG · Drama, Mystery".into(),
            tagline: "One line of it, and a few more words after that.".into(),
            plot: "word ".repeat(120),
            directed: "Directed by A Director".into(),
            written: "Written by A Writer, Another Writer, A Third Writer, and A Fourth".into(),
            cast: "A Player as The Part, ".repeat(12),
            set: Some(Set {
                heading: "The Set".into(),
                members: vec![Item {
                    id: "one".into(),
                    name: "Film one".into(),
                    line: facts::Line::of(&["Film one"]),
                    art: String::new(),
                }],
                current: 0,
            }),
            focus,
        }
    }

    fn blocks(movie: &Movie) -> Blocks {
        Blocks::of(movie, WIDTH * COLUMN, HEIGHT * TOP)
    }

    #[test]
    fn a_crowded_page_is_longer_than_a_short_frame() {
        assert!(blocks(&crowded(Focus::Buttons(0))).content > SHORT);
    }

    #[test]
    fn the_strip_and_the_credits_are_in_view_while_the_strip_has_focus() {
        let movie = crowded(Focus::Strip(0));
        let blocks = blocks(&movie);
        let offset = blocks.scroll(&movie, SHORT);
        assert!(offset > 0.0);
        for block in blocks.strip_and_under() {
            assert!(block.top - offset >= 0.0, "{block:?} at {offset}");
            assert!(block.bottom() - offset <= SHORT, "{block:?} at {offset}");
        }
    }

    #[test]
    fn the_buttons_show_the_page_from_its_top() {
        let movie = crowded(Focus::Buttons(0));
        assert_eq!(blocks(&movie).scroll(&movie, HEIGHT), 0.0);
    }

    #[test]
    fn a_page_with_no_set_brings_its_credits_up_with_the_buttons() {
        let mut movie = crowded(Focus::Buttons(0));
        movie.set = None;
        let blocks = blocks(&movie);
        assert!(blocks.strip.is_none());
        let offset = blocks.scroll(&movie, HEIGHT);
        assert!(blocks.cast.bottom() - offset <= HEIGHT);
    }

    #[test]
    fn a_movie_with_a_logo_reserves_the_box_it_draws_in() {
        let mut movie = crowded(Focus::Buttons(0));
        movie.logo = "logo.png".into();
        assert_eq!(blocks(&movie).title.height, LOGO_HEIGHT);
    }

    #[test]
    fn a_line_the_movie_does_not_carry_takes_no_height_and_no_gap() {
        let mut movie = crowded(Focus::Buttons(0));
        movie.tagline = String::new();
        let blocks = blocks(&movie);
        assert_eq!(blocks.tagline.height, 0.0);
        assert_eq!(blocks.tagline.top, blocks.plot.top);
    }
}
