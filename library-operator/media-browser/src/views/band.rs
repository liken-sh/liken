// The band across the top of a wall: the heading at the left, and the
// controls the screen names at the right, in a row that ends a margin to
// the left of the clock. The controls draw dimmed because none of them
// exists yet. The band is the place every wall reserves for them, so a
// person learns one place. It is a layer of its own over the screen,
// because a row that scrolls up under it must not show through, and
// inside one layer the renderer draws every fill, then every image, then
// every text, whatever the order they were drawn in.

use std::convert::Infallible;

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::alignment::Vertical;
use iced_winit::core::text::Alignment;
use iced_winit::core::{Element, Length, Point, Rectangle, Theme, mouse};

use super::{area, clock, extent, label, mark};
use crate::look;

/// The height the band takes off the top of the frame.
pub const HEIGHT: f32 = 84.0;

/// The three controls, in the order left and right move across them.
pub const CONTROLS: [&str; 3] = ["Sort", "Filter", "Search"];

/// The one control the home page draws. Sort and filter act on a query,
/// and the home page answers none, so search is the only one of the
/// three it carries.
pub const SEARCH_ONLY: [&str; 1] = ["Search"];

/// The margin at both ends of the band.
pub const PAD: f32 = 32.0;

// The width of one control's box, and the gap between two of them.
const CONTROL: f32 = 168.0;
const GAP: f32 = 20.0;

// The height of a control's box inside the band.
const BOX: f32 = 48.0;

/// One control's box, placed from the right end of a row of `count`
/// controls, which ends a margin to the left of the clock.
pub fn control(width: f32, count: usize, index: usize) -> Rectangle {
    let row = count as f32 * CONTROL + (count - 1) as f32 * GAP;
    let left = clock::left(width) - PAD - row;
    area(
        left + index as f32 * (CONTROL + GAP),
        (HEIGHT - BOX) / 2.0,
        CONTROL,
        BOX,
    )
}

/// Draw the band. `heading` is what the screen is about. `controls` are
/// the controls this screen carries, in the order left and right move
/// across them. `focus` names the control that holds focus, or nothing
/// while the posters below hold it.
pub fn draw(
    frame: &mut canvas::Frame<Renderer>,
    width: f32,
    heading: &str,
    controls: &[&str],
    focus: Option<usize>,
) {
    // The band paints its own ground, so nothing under its layer shows
    // through it.
    let ground = area(0.0, 0.0, width, HEIGHT);
    frame.fill_rectangle(ground.position(), extent(ground), look::BACKGROUND);
    frame.fill_text(label(
        heading,
        Point::new(PAD, HEIGHT / 2.0),
        look::NAME,
        look::text(),
        Alignment::Left,
        Vertical::Center,
        width / 2.0,
    ));

    for (index, name) in controls.iter().enumerate() {
        let bounds = control(width, controls.len(), index);
        frame.fill_rectangle(bounds.position(), extent(bounds), look::slot());
        frame.fill_text(label(
            name,
            Point::new(bounds.center_x(), bounds.center_y()),
            look::CONTROL,
            look::muted(),
            Alignment::Center,
            Vertical::Center,
            bounds.width,
        ));
        if focus == Some(index) {
            mark(frame, bounds);
        }
    }

    let rule = area(0.0, HEIGHT - 2.0, width, 2.0);
    frame.fill_rectangle(rule.position(), extent(rule), look::slot());
}

/// The band as a layer over a screen: what it says, the controls the
/// screen carries, and the control that holds focus.
pub struct Layer<'a> {
    pub heading: &'a str,
    pub controls: &'a [&'a str],
    pub focus: Option<usize>,
}

impl canvas::Program<Infallible, Theme, Renderer> for Layer<'_> {
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
        draw(
            &mut frame,
            bounds.width,
            self.heading,
            self.controls,
            self.focus,
        );
        vec![frame.into_geometry()]
    }
}

/// The band's layer as an element a screen stacks over its own.
pub fn layer<'a>(
    heading: &'a str,
    controls: &'a [&'a str],
    focus: Option<usize>,
) -> Element<'a, Infallible, Theme, Renderer> {
    canvas(Layer {
        heading,
        controls,
        focus,
    })
    .width(Length::Fill)
    .height(Length::Fill)
    .into()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn the_controls_sit_in_order_against_the_right_edge() {
        let first = control(1920.0, CONTROLS.len(), 0);
        let last = control(1920.0, CONTROLS.len(), CONTROLS.len() - 1);
        assert!(first.x < last.x);
        assert_eq!(last.x + last.width, clock::left(1920.0) - PAD);
        assert_eq!(first.y, last.y);
    }

    #[test]
    fn a_control_fits_inside_the_band() {
        let bounds = control(1920.0, CONTROLS.len(), 1);
        assert!(bounds.y > 0.0);
        assert!(bounds.y + bounds.height < HEIGHT);
    }

    #[test]
    fn one_control_takes_the_place_the_last_of_three_takes() {
        assert_eq!(SEARCH_ONLY.len(), 1);
        assert_eq!(
            control(1920.0, SEARCH_ONLY.len(), 0),
            control(1920.0, CONTROLS.len(), CONTROLS.len() - 1)
        );
    }
}
