// The movie page's drawing. Every block reports the height it took, so
// the next block stacks under it, and a movie with no tagline, no set, or
// no credits leaves no hole where they would have been.

use std::cell::RefCell;
use std::convert::Infallible;

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::{Point, Rectangle, Theme, mouse};

use super::Movie;
use crate::look;
use crate::posters::Posters;
use crate::views::{area, buttons, header, strip, text};

// The margin at both sides of the page.
const MARGIN: f32 = 120.0;

// The share of the width the column of text takes. The text sits in the
// dimmed half of the backdrop, and the art shows through the rest.
const COLUMN: f32 = 0.46;

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

/// The page as one canvas over the backdrop.
pub struct Page<'a, P> {
    /// The movie the page is about.
    pub movie: &'a Movie,
    /// The store the backdrop, the logo, and the strip's posters come
    /// from.
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

        header::backdrop(&mut frame, posters, &movie.library, &movie.backdrop, bounds);

        let column = bounds.width * COLUMN;
        let mut stack = Stack {
            x: MARGIN,
            y: bounds.height * TOP,
        };

        let taken = header::title(
            &mut frame,
            posters,
            &header::Title {
                library: &movie.library,
                logo: &movie.logo,
                name: &movie.title,
                at: stack.at(),
                logo_box: (LOGO_WIDTH, LOGO_HEIGHT),
                width: column,
            },
        );
        stack.add(taken);

        let taken = text::line(
            &mut frame,
            &movie.facts,
            stack.at(),
            look::FACTS,
            look::muted(),
            column,
        );
        stack.add(taken);

        let taken = text::line(
            &mut frame,
            &movie.tagline,
            stack.at(),
            look::TAGLINE,
            look::text(),
            column,
        );
        stack.add(taken);

        let taken = text::block(
            &mut frame,
            &movie.plot,
            stack.at(),
            look::PLOT,
            look::text(),
            column,
            PLOT_LINES,
        );
        stack.add(taken);

        let focus = match movie.focus {
            super::Focus::Buttons(index) => Some(index),
            super::Focus::Strip(_) => None,
        };
        let taken = buttons::draw(&mut frame, movie.buttons(), stack.at(), focus);
        stack.add(taken);

        if let Some(set) = &movie.set {
            strip::draw(
                &mut frame,
                posters,
                &strip::Strip {
                    members: &set.members,
                    current: set.current,
                    focus: match movie.focus {
                        super::Focus::Strip(index) => Some(index),
                        super::Focus::Buttons(_) => None,
                    },
                    heading: &set.heading,
                    library: &movie.library,
                    region: area(stack.x, stack.y, bounds.width - 2.0 * MARGIN, strip::HEIGHT),
                },
            );
            stack.add(strip::HEIGHT);
        }

        for credit in [&movie.directed, &movie.written] {
            let taken = text::line(
                &mut frame,
                credit,
                stack.at(),
                look::CREDITS,
                look::muted(),
                column,
            );
            stack.add(taken);
        }
        let taken = text::block(
            &mut frame,
            &movie.cast,
            stack.at(),
            look::CREDITS,
            look::muted(),
            column,
            CAST_LINES,
        );
        stack.add(taken);

        vec![frame.into_geometry()]
    }
}

// Where the next block of the page starts. A block that drew nothing
// moves it nowhere and takes no gap.
struct Stack {
    x: f32,
    y: f32,
}

impl Stack {
    fn at(&self) -> Point {
        Point::new(self.x, self.y)
    }

    fn add(&mut self, taken: f32) {
        if taken > 0.0 {
            self.y += taken + GAP;
        }
    }
}
