// The layers a page draws over itself while the loading state runs: the
// item's own backdrop again over everything the page drew, and over that
// the pool of shade, the mark, and the logo on its way to the centre.
//
// The backdrop is drawn a second time rather than the page being faded
// out block by block, because inside one layer the renderer draws every
// mesh, then every image, then every text, so nothing a page draws can
// cover its own text. A layer of its own can. The mark is the same rule
// again: it is a mesh, so a mark drawn beside the backdrop would be
// painted under it, and it takes a layer of its own over the art.

use std::cell::RefCell;
use std::convert::Infallible;

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::alignment::Vertical;
use iced_winit::core::text::Alignment;
use iced_winit::core::{Color, Point, Rectangle, Theme, Vector, mouse};

use liken_iced::mark;

use super::{Tone, area, extent, label, paint};
use crate::look;
use crate::posters::{Art, Posters};

// The box the centred logo draws in, as a share of the frame. The logo
// keeps its own ratio inside it, so a wide logo takes the width and a
// tall one takes the height.
const LOGO_WIDTH: f32 = 0.34;
const LOGO_HEIGHT: f32 = 0.26;

// Where the foot of the logo sits, as a share of the height. The logo and
// the mark under it straddle the middle of the frame.
const LOGO_FOOT: f32 = 0.5;

// The span of the mark, as a share of the width.
const SPAN: f32 = 0.12;

// The space between the foot of the logo and the top of the mark, as a
// share of the height.
const GAP: f32 = 0.05;

// The width the centred name wraps in, as a share of the frame, where the
// item has no logo.
const NAME: f32 = 0.7;

// The pool of shade under the logo and the mark, so both read over a
// bright backdrop: its centre and its two radii as shares of the frame,
// how dark it is at the centre, and how many rings it is built from.
//
// The canvas fills a shape in one colour and has no radial gradient, so
// the pool is a stack of ellipses, each a little smaller and each a
// little darker, and the shade deepens toward the centre in steps too
// small to see. The middle sits a little under the logo's foot, because
// the mark under the logo is taller than the gap over it.
const POOL_CENTRE: f32 = 0.5;
const POOL_WIDTH: f32 = 0.40;
const POOL_HEIGHT: f32 = 0.42;
const POOL_SHADE: f32 = 0.92;
const POOL_RINGS: u32 = 48;

/// The loading state as one frame draws it.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Curtain {
    /// How far the page has gone, from 0 whole to 1 fully away.
    pub away: f32,
    /// The clock the mark pulses on, in seconds.
    pub phase: f64,
}

/// What the curtain reads off the page under it: the box that page draws
/// its logo in at this frame's bounds, which is where the logo starts its
/// move to the centre.
pub trait Head {
    /// The box the page's head draws in now, scroll included.
    fn head(&self, bounds: Rectangle) -> Rectangle;
}

/// The art layer of the loading state over one page.
pub struct Layer<'a, P> {
    /// The library the art paths resolve against.
    pub library: &'a str,
    /// The path of the backdrop file, empty where the item has none.
    pub art: &'a str,
    /// The path of the logo file, empty where the item has none.
    pub logo: &'a str,
    /// The name a person reads, which the state centres where the item
    /// has no logo.
    pub name: &'a str,
    /// The store the backdrop and the logo come from.
    pub posters: &'a RefCell<P>,
    /// The page under this layer, which says where its logo sits now.
    pub head: &'a dyn Head,
    /// What this frame draws.
    pub curtain: Curtain,
}

impl<P> Clone for Layer<'_, P> {
    fn clone(&self) -> Self {
        *self
    }
}

impl<P> Copy for Layer<'_, P> {}

// The layer over the art: the pool of shade, the mark, the logo on its way
// to the centre, and the name where the item has no logo. The pool and the
// mark are meshes and the logo is an image, so the logo draws over both,
// and the mark sits under the logo where the two never overlap.
pub struct Front<'a, P>(pub Layer<'a, P>);

impl<P: Posters> canvas::Program<Infallible, Theme, Renderer> for Layer<'_, P> {
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
        let away = self.away();
        if away <= 0.0 {
            return vec![frame.into_geometry()];
        }
        let posters = &mut *self.posters.borrow_mut();

        // The art again, at the state's own opacity. It covers the page
        // and it clears the shade in the one move, because the shade is a
        // layer under this one.
        let backdrop = (!self.art.is_empty())
            .then(|| {
                posters.poster(
                    self.library,
                    self.art,
                    bounds.width as u32,
                    bounds.height as u32,
                )
            })
            .flatten();
        match backdrop {
            Some(image) => paint(&mut frame, &image, bounds, Tone::At(away)),
            None => frame.fill_rectangle(
                bounds.position(),
                extent(bounds),
                Color {
                    a: away,
                    ..look::BACKGROUND
                },
            ),
        }

        vec![frame.into_geometry()]
    }
}

impl<P: Posters> canvas::Program<Infallible, Theme, Renderer> for Front<'_, P> {
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
        let layer = self.0;
        let away = layer.away();

        let posters = &mut *layer.posters.borrow_mut();
        pool(&mut frame, bounds, away);

        // The mark swells at the state's own energy, so it is at full
        // swing while the state holds and it stills over the exit.
        let box_ = mark::bounds();
        let span = bounds.width * SPAN;
        let height = span * box_.height / box_.width;
        let logo = layer.logo(posters, bounds);
        let foot = logo
            .as_ref()
            .map(|(_, at)| at.y + at.height)
            .unwrap_or_else(|| layer.centre(bounds));
        mark::draw(
            &mut frame,
            Point::new(bounds.center_x(), foot + bounds.height * GAP + height / 2.0),
            span,
            f64::from(away),
            layer.curtain.phase,
            away,
        );

        // The logo draws whole, and not at the state's opacity, because
        // the page under these layers leaves its logo's box empty while
        // the state runs: there is one logo on the screen, and it moves.
        match logo {
            Some((image, at)) => paint(&mut frame, &image, at, Tone::Full),
            None => frame.fill_text(label(
                layer.name,
                Point::new(bounds.center_x(), layer.centre(bounds)),
                look::TITLE,
                Color {
                    a: away,
                    ..look::text()
                },
                Alignment::Center,
                Vertical::Bottom,
                bounds.width * NAME,
            )),
        }

        vec![frame.into_geometry()]
    }
}

impl<P: Posters> Layer<'_, P> {
    // How far the page has gone, from 0 whole to 1 fully away.
    fn away(&self) -> f32 {
        self.curtain.away.clamp(0.0, 1.0)
    }

    // Where the foot of the logo, or the base line of the name, sits.
    fn centre(&self, bounds: Rectangle) -> f32 {
        bounds.y + bounds.height * LOGO_FOOT
    }

    // The logo and the box it draws in at this point of the motion, and
    // nothing where the item has no logo or the decode has not landed.
    //
    // The box comes from the shares of the frame and not from the size
    // the store decoded at, so a small logo file draws at the same share
    // of the width as a large one. The store fits a decode inside the box
    // it is asked for and never scales one up, and this state is one
    // image over a still frame, so the scale costs nothing to draw.
    fn logo(&self, posters: &mut P, bounds: Rectangle) -> Option<(Art, Rectangle)> {
        if self.logo.is_empty() {
            return None;
        }
        let page = self.head.head(bounds);
        let image = posters.fitted(
            self.library,
            self.logo,
            (bounds.width * LOGO_WIDTH) as u32,
            (bounds.height * LOGO_HEIGHT) as u32,
        )?;
        let (width, height) = image.size();
        let ratio = height as f32 / width as f32;

        let to = fitted(
            bounds.width * LOGO_WIDTH,
            bounds.height * LOGO_HEIGHT,
            ratio,
        );
        let from = fitted(page.width, page.height, ratio);
        let to = area(
            bounds.center_x() - to.0 / 2.0,
            self.centre(bounds) - to.1,
            to.0,
            to.1,
        );
        let from = area(page.x, page.y, from.0, from.1);
        Some((image, between(from, to, self.away())))
    }
}

// The pool of shade, at this share of its full depth.
fn pool(frame: &mut canvas::Frame<Renderer>, bounds: Rectangle, away: f32) {
    if away <= 0.0 {
        return;
    }
    let centre = Point::new(bounds.center_x(), bounds.y + bounds.height * POOL_CENTRE);
    let radii = Vector::new(bounds.width * POOL_WIDTH, bounds.height * POOL_HEIGHT);
    // Each ring adds the same share, so the shade at the centre, where
    // every ring lies, is the whole of it.
    let ring = Color {
        a: 1.0 - (1.0 - POOL_SHADE * away).powf(1.0 / POOL_RINGS as f32),
        ..look::BACKGROUND
    };
    let unit = canvas::Path::circle(Point::ORIGIN, 1.0);
    for step in 0..POOL_RINGS {
        let share = 1.0 - step as f32 / POOL_RINGS as f32;
        frame.with_save(|frame| {
            frame.translate(Vector::new(centre.x, centre.y));
            frame.scale_nonuniform(Vector::new(radii.x * share, radii.y * share));
            frame.fill(&unit, ring);
        });
    }
}

// The largest width and height at this ratio that fit inside a box.
fn fitted(width: f32, height: f32, ratio: f32) -> (f32, f32) {
    match width * ratio <= height {
        true => (width, width * ratio),
        false => (height / ratio, height),
    }
}

// One rectangle on its way to another.
fn between(from: Rectangle, to: Rectangle, share: f32) -> Rectangle {
    let step = |from: f32, to: f32| from + (to - from) * share;
    area(
        step(from.x, to.x),
        step(from.y, to.y),
        step(from.width, to.width),
        step(from.height, to.height),
    )
}

#[cfg(test)]
mod tests {
    use super::*;

    fn from() -> Rectangle {
        area(120.0, 130.0, 400.0, 100.0)
    }

    fn to() -> Rectangle {
        area(760.0, 340.0, 640.0, 160.0)
    }

    #[test]
    fn the_start_of_the_move_is_where_the_page_draws_it() {
        assert_eq!(between(from(), to(), 0.0), from());
    }

    #[test]
    fn the_end_of_the_move_is_the_centre() {
        assert_eq!(between(from(), to(), 1.0), to());
    }

    #[test]
    fn the_middle_of_the_move_is_between_the_two() {
        let at = between(from(), to(), 0.5);
        assert_eq!(at, area(440.0, 235.0, 520.0, 130.0));
    }

    #[test]
    fn a_wide_logo_takes_the_width_of_its_box() {
        assert_eq!(fitted(640.0, 216.0, 0.25), (640.0, 160.0));
    }

    #[test]
    fn a_tall_logo_takes_the_height_of_its_box() {
        assert_eq!(fitted(640.0, 216.0, 1.0), (216.0, 216.0));
    }

    #[test]
    fn a_logo_smaller_than_its_box_still_fills_it() {
        assert_eq!(fitted(1280.0, 432.0, 0.25), (1280.0, 320.0));
    }
}
