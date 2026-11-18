//! The idle screen: what the client draws while no `Play` runs.
//!
//! The whole screen is one `canvas` frame. Every element draws in a canvas
//! 1080 rows tall, and the frame scales it to the surface, so one layout
//! serves 720p, 1080p, and 4K.
//!
//! Each element's `draw` takes the shade's brightness factor `light` and
//! applies it to its own colours. `look::under` explains why the shade is not
//! a rectangle drawn over the screen.

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

pub type Frame = canvas::Frame<Renderer>;

/// The canvas the elements measure against.
///
/// The height is always 1080, and the width follows the surface's aspect
/// ratio. See `look::canvas_width`.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Layout {
    pub width: f32,
    pub height: f32,
    /// Surface pixels per canvas pixel. The frame applies it to positions.
    /// It does not apply it to a stroke width, because the toolkit
    /// transforms the path and then tessellates the stroke at the width it
    /// was given. So an element that strokes multiplies the width by `scale`.
    pub scale: f32,
}

impl Layout {
    pub fn for_surface(size: Size) -> Self {
        Self {
            width: look::canvas_width((size.width as u32, size.height as u32)),
            height: look::CANVAS_HEIGHT,
            scale: size.height / look::CANVAS_HEIGHT,
        }
    }

    pub fn right(&self) -> f32 {
        self.width - look::MARGIN_X
    }

    /// The bottom margin is the same as the top margin, so the identity
    /// block at the bottom balances the clock at the top.
    pub fn bottom(&self) -> f32 {
        self.height - look::MARGIN_Y
    }

    pub fn center(&self) -> Point {
        Point::new(self.width / 2.0, self.height / 2.0)
    }

    /// The width of the mark: a third of the canvas width, up to a third of
    /// the width of a 16:9 canvas. So the mark keeps its size on a surface
    /// wider than 16:9, and shrinks to fit on a narrower one.
    pub fn mark_span(&self) -> f32 {
        self.width.min(self.height * 16.0 / 9.0) / 3.0
    }

    fn onto(&self, bounds: Rectangle) -> f32 {
        bounds.height / self.height
    }
}

/// The screen at second `at` on the harness's clock.
///
/// Every element is a function of `at` and of the moments folded into `unit`,
/// and nothing counts frames. So a captured frame is reproducible, and a
/// dropped frame changes nothing.
#[derive(Debug)]
pub struct Idle<'a> {
    pub unit: &'a Unit,
    pub at: f64,
    /// Whether the preview keys are bound. Only then does the screen draw
    /// their legend.
    pub preview: bool,
    /// Seconds the wall clock is into its current minute at second `at`.
    pub into_minute: f64,
}

impl Idle<'_> {
    /// The next second at which the picture changes, the earliest that any
    /// element returns. A result at or before `at` means draw now.
    ///
    /// `at` is the second of the last frame drawn, not the current time, so
    /// an ease that ended during that frame still gets a frame that shows its
    /// end. Each element computes its next frame beside its `draw`, so a
    /// change to an ease changes the schedule with it. The activity line has
    /// no entry because it takes its opacity from the energy, and the legend
    /// has none because it does not change. The clock always returns a
    /// second, so the result is never `None`.
    pub fn next_frame(&self, at: f64) -> Option<f64> {
        [
            Some(clock::next_frame(at, self.into_minute)),
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

    /// The mark draws first, so the corner elements draw over it where they
    /// overlap.
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
    fn a_16_by_9_surface_gives_a_1920_by_1080_canvas() {
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

    /// The screen for `unit` with no preview keys bound, when the wall clock
    /// is `into_minute` seconds into its minute.
    fn screen(unit: &Unit, into_minute: f64) -> Idle<'_> {
        Idle {
            unit,
            at: 0.0,
            preview: false,
            into_minute,
        }
    }

    #[test]
    fn a_settled_screen_next_changes_when_the_minute_turns() {
        let unit = Unit::default();
        assert_eq!(screen(&unit, 30.0).next_frame(0.0), Some(30.0));
        assert_eq!(screen(&unit, 59.5).next_frame(11.75), Some(12.25));
    }

    #[test]
    fn an_easing_element_is_drawn_before_the_clocks_next_minute() {
        let mut unit = Unit::default();
        unit.fold(Moment::Sleep, 10.0);

        // The fade to black lasts four seconds. After it, the next change is
        // the clock's minute.
        assert_eq!(screen(&unit, 0.0).next_frame(10.5), Some(10.5));
        assert_eq!(screen(&unit, 50.0).next_frame(14.5), Some(24.5));
    }

    #[test]
    fn next_frame_is_the_earlier_of_the_volume_row_and_the_clock() {
        let mut unit = Unit::default();
        unit.fold(
            Moment::Level {
                volume: Volume {
                    level: 0.4,
                    muted: false,
                    ..Volume::default()
                },
                pressed: true,
            },
            10.0,
        );

        // The volume row starts to fade out at 14.0.
        assert_eq!(screen(&unit, 59.0).next_frame(11.5), Some(12.5));
        assert_eq!(screen(&unit, 10.0).next_frame(11.5), Some(14.0));
    }

    #[test]
    fn the_margins_balance_across_the_screen() {
        let layout = Layout::for_surface(Size::new(1920.0, 1080.0));
        assert_eq!(layout.width - layout.right(), look::MARGIN_X);
        assert_eq!(layout.height - layout.bottom(), look::MARGIN_Y);
    }
}
