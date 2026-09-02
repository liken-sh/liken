//! The idle screen: what the client draws while no `Play` runs.
//!
//! The whole screen is one `canvas`, and `draw` states the order the elements
//! go into it.
//!
//! That order holds between shapes, and it does not hold between a shape and
//! text. `iced_wgpu` renders a layer in four passes, quads then meshes then
//! images then text, and a canvas frame is one layer, so a rectangle drawn
//! last still lands under every line of text. An element that has to cover
//! text cannot be drawn over it. So the shade is a factor every element
//! applies to its own colours, and every `draw` below takes it.
//!
//! Every element draws in the display's own space, 1080 rows tall, and the
//! frame scales that space onto the surface. `media-operator`'s
//! `display/theme.lua` holds the same space for the same reason: one layout
//! serves 720, 1080, and 4K with no branch.

pub mod activity;
pub mod clock;
pub mod energy;
pub mod identity;
pub mod mark;
pub mod preview;
pub mod shade;
pub mod volume;

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::{Point, Rectangle, Size, Theme, mouse};

use crate::look;
use crate::unit::Unit;

/// The canvas frame every element draws into.
pub type Frame = canvas::Frame<Renderer>;

/// The canvas the elements measure against.
///
/// The height is always 1080, and the width follows the surface's own ratio.
/// `look::canvas_width` states the rule and the reason.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Layout {
    pub width: f32,
    pub height: f32,
    /// How many surface pixels one canvas pixel takes. The frame carries this
    /// as a transform, so no element multiplies a position by it. A stroke is
    /// the exception, because the toolkit tessellates a stroke at the width it
    /// is given after it has transformed the path, so a stroke width is stated
    /// in surface pixels and every element that strokes multiplies by this.
    pub scale: f32,
}

impl Layout {
    /// The canvas for one surface size.
    pub fn for_surface(size: Size) -> Self {
        Self {
            width: look::canvas_width((size.width as u32, size.height as u32)),
            height: look::CANVAS_HEIGHT,
            scale: size.height / look::CANVAS_HEIGHT,
        }
    }

    /// The x every flush-right element hangs from.
    pub fn right(&self) -> f32 {
        self.width - look::MARGIN_X
    }

    /// The y every element standing on the bottom edge hangs from. It is the
    /// top margin, so the identity block balances the clock across the screen.
    pub fn bottom(&self) -> f32 {
        self.height - look::MARGIN_Y
    }

    /// The middle of the canvas, where the mark rests.
    pub fn center(&self) -> Point {
        Point::new(self.width / 2.0, self.height / 2.0)
    }

    /// The width the mark fills: a third of the canvas height at 16:9, or a
    /// third of the width on a screen narrower than that. The mark then keeps
    /// one size against the height, and a wide screen shows the same mark with
    /// more room beside it.
    pub fn mark_span(&self) -> f32 {
        self.width.min(self.height * 16.0 / 9.0) / 3.0
    }

    /// How one canvas row maps onto the surface.
    fn onto(&self, bounds: Rectangle) -> f32 {
        bounds.height / self.height
    }
}

/// The screen, as the canvas draws it at one moment.
///
/// `at` is the second on the harness's clock, and every element is a function
/// of it and of the moments in `unit`. Nothing here counts frames, so a
/// captured frame is reproducible and a dropped frame is harmless.
#[derive(Debug)]
pub struct Idle<'a> {
    pub unit: &'a Unit,
    pub at: f64,
    /// Whether the preview keys are bound, which only a workstation does. The
    /// legend draws under it, so a cluster's idle screen never carries one.
    pub preview: bool,
}

impl Idle<'_> {
    /// The second this screen next changes, which is the first second any one
    /// element changes at. `at` is what the harness's clock reads now, at or
    /// after the second of the last frame. A second at or before `at` asks the
    /// harness to draw now.
    ///
    /// Each element answers for itself, beside the code that computes its own
    /// drawing, so a change to an ease changes the schedule with it. Most of
    /// them ease or they are still: an ease asks to draw now, and a settled
    /// element states nothing. The clock and the volume row are the two that
    /// name a second ahead.
    ///
    /// Two of the elements `draw` lists answer through another. The activity
    /// line takes its opacity from the energy, and the preview legend draws
    /// one fixed run of text.
    ///
    /// The clock always names one, so this is never `None`. That matters
    /// because the client drains the broker in `tick`, and `tick` runs on a
    /// frame: a screen that answered `None` would sleep until an event that
    /// only a frame could read.
    pub fn next_frame(&self, at: f64) -> Option<f64> {
        [
            Some(clock::next_frame(at)),
            mark::next_frame(self.unit, at),
            energy::next_frame(self.unit, at),
            shade::next_frame(self.unit, at),
            identity::next_frame(self.unit, at),
            volume::next_frame(self.unit, at),
        ]
        .into_iter()
        .flatten()
        .min_by(f64::total_cmp)
    }
}

impl<Message> canvas::Program<Message, Theme, Renderer> for Idle<'_> {
    type State = ();

    /// The elements. The mark draws first, so the corner text draws over it
    /// if the two ever meet. The shade is read first and drawn by none of
    /// them: each element scales its own colours by it.
    fn draw(
        &self,
        _state: &Self::State,
        renderer: &Renderer,
        _theme: &Theme,
        bounds: Rectangle,
        _cursor: mouse::Cursor,
    ) -> Vec<canvas::Geometry<Renderer>> {
        let layout = Layout::for_surface(bounds.size());
        let mut frame = Frame::new(renderer, bounds.size());
        frame.scale(layout.onto(bounds));

        let light = shade::fade(self.unit, self.at);

        mark::draw(&mut frame, &layout, self.unit, self.at, light);
        clock::draw(&mut frame, &layout, self.unit, self.at, light);
        activity::draw(&mut frame, &layout, self.unit, self.at, light);
        identity::draw(&mut frame, &layout, self.unit, self.at, light);
        volume::draw(&mut frame, &layout, self.unit, self.at, light);

        if self.preview {
            preview::legend(&mut frame, &layout, light);
        }

        vec![frame.into_geometry()]
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use media_screen::Moment;
    use media_screen::volume::Volume;

    #[test]
    fn a_16_by_9_surface_gives_the_width_the_display_has_always_held() {
        let layout = Layout::for_surface(Size::new(1920.0, 1080.0));
        assert_eq!((layout.width, layout.height), (1920.0, 1080.0));
    }

    #[test]
    fn a_wider_surface_widens_the_canvas_and_leaves_the_mark_alone() {
        let wide = Layout::for_surface(Size::new(2560.0, 1080.0));
        let square = Layout::for_surface(Size::new(1920.0, 1080.0));
        assert!(wide.width > square.width);
        assert_eq!(wide.mark_span(), square.mark_span());
    }

    #[test]
    fn a_narrower_surface_keeps_the_mark_inside_the_margins() {
        let tall = Layout::for_surface(Size::new(1440.0, 1080.0));
        assert_eq!(tall.mark_span(), tall.width / 3.0);
    }

    /// The screen one unit draws, with no preview keys bound.
    fn screen(unit: &Unit) -> Idle<'_> {
        Idle {
            unit,
            at: 0.0,
            preview: false,
        }
    }

    #[test]
    fn a_settled_screen_draws_once_a_second_for_the_clock() {
        let unit = Unit::default();
        assert_eq!(screen(&unit).next_frame(0.0), Some(1.0));
        assert_eq!(screen(&unit).next_frame(11.75), Some(12.0));
    }

    #[test]
    fn an_element_in_motion_takes_the_screen_ahead_of_the_clock() {
        let mut unit = Unit::default();
        unit.fold(Moment::Sleep, 10.0);

        // The shade eases for four seconds, and a second at or before `at`
        // asks the harness to draw now.
        assert_eq!(screen(&unit).next_frame(10.5), Some(10.5));
        assert_eq!(screen(&unit).next_frame(14.5), Some(15.0));
    }

    #[test]
    fn the_volume_rows_hold_never_outruns_the_clock() {
        let mut unit = Unit::default();
        unit.fold(
            Moment::Level {
                volume: Volume {
                    level: 40,
                    muted: false,
                },
                pressed: true,
            },
            10.0,
        );

        // The row names 14.0, the second it starts to leave, and the clock
        // names the second after `at`. The screen takes the nearer of the two.
        assert_eq!(screen(&unit).next_frame(11.5), Some(12.0));
    }

    #[test]
    fn the_margins_balance_across_the_screen() {
        let layout = Layout::for_surface(Size::new(1920.0, 1080.0));
        assert_eq!(layout.width - layout.right(), look::MARGIN_X);
        assert_eq!(layout.height - layout.bottom(), look::MARGIN_Y);
    }
}
