// The strip: the browser's own layer across the top of every screen,
// right-aligned. It draws a magnifying glass at the clock's left, then
// the clock. On a search wall the glass expands into the text field,
// which draws leftward from the clock with the glass inside its left
// end. The focus mark goes on the glass, or on the field, while the
// strip holds the browser's focus. It is the browser's layer and not a
// screen's, so every screen carries it in the same place and an up
// press that moves nothing on any screen reaches it.

use std::convert::Infallible;

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::{Color, Point, Rectangle, Theme, mouse};

use super::{left, reading};
use crate::clock::Time;
use crate::look;
use crate::views::field::{self, TextField};
use crate::views::{area, band, mark};

/// The side of the icon's square box. The icon is the size of the
/// reading beside it, so the two read as one strip.
pub const GLASS: f32 = look::CONTROL;

// The circle's radius and the center it turns about, as shares of the
// box, and where the handle ends, so the icon scales with the box.
const RADIUS: f32 = 0.32;
const CENTER: f32 = 0.38;
const HANDLE: f32 = 0.94;

// The width of the circle's stroke and the handle's, thin enough that
// the icon stays lighter than the reading beside it.
const LINE: f32 = 2.0;

/// The icon's box in a frame this wide, where no field expands it: a
/// margin to the left of the clock, on the same middle line.
pub fn glass_at(width: f32) -> Rectangle {
    area(
        left(width) - band::PAD - GLASS,
        (band::HEIGHT - GLASS) / 2.0,
        GLASS,
        GLASS,
    )
}

// The magnifying glass in its box: a circle, and a handle out along the
// diagonal from the circle's edge to the box's corner.
fn glass(frame: &mut canvas::Frame<Renderer>, at: Rectangle, ink: Color) {
    let side = at.width.min(at.height);
    let center = Point::new(at.x + side * CENTER, at.y + side * CENTER);
    let radius = side * RADIUS;
    let stroke = || {
        canvas::Stroke::default()
            .with_color(ink)
            .with_width(LINE)
            .with_line_cap(canvas::LineCap::Round)
    };
    frame.stroke(&canvas::Path::circle(center, radius), stroke());
    let step = radius * std::f32::consts::FRAC_1_SQRT_2;
    frame.stroke(
        &canvas::Path::line(
            Point::new(center.x + step, center.y + step),
            Point::new(at.x + side * HANDLE, at.y + side * HANDLE),
        ),
        stroke(),
    );
}

/// The strip as one frame draws it: the reading, the text of the search
/// wall on top of the stack or nothing where the top screen is not one,
/// and whether the strip holds the browser's focus.
pub struct Strip<'a> {
    pub time: Time,
    pub field: Option<&'a TextField>,
    pub focused: bool,
}

impl canvas::Program<Infallible, Theme, Renderer> for Strip<'_> {
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
        // The mark goes around whichever of the two the strip is: the
        // icon alone, or the field the icon expanded into.
        let marked = match self.field {
            Some(field) => {
                let box_of = field::bounds(bounds.width);
                field::draw(&mut frame, bounds.width, field);
                glass(&mut frame, field::icon(box_of), look::muted());
                box_of
            }
            None => {
                let at = glass_at(bounds.width);
                glass(&mut frame, at, look::muted());
                at
            }
        };
        if self.focused {
            mark(&mut frame, marked);
        }
        reading(&mut frame, bounds, self.time);
        vec![frame.into_geometry()]
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::views::clock::room;

    const WIDTH: f32 = 1920.0;

    #[test]
    fn the_icon_stands_a_margin_to_the_left_of_the_clock() {
        let at = glass_at(WIDTH);
        assert_eq!(at.x + at.width + band::PAD, left(WIDTH));
        assert_eq!(at.width, GLASS);
        assert_eq!(at.height, GLASS);
        assert!(at.y > 0.0);
        assert!(at.y + at.height < band::HEIGHT);
    }

    #[test]
    fn the_field_reaches_the_same_margin_and_holds_the_icon_at_its_left_end() {
        let box_of = field::bounds(WIDTH);
        assert_eq!(box_of.x + box_of.width + band::PAD, left(WIDTH));
        assert_eq!(
            box_of.x + box_of.width + band::PAD + room(),
            WIDTH - band::PAD
        );

        let icon = field::icon(box_of);
        assert!(icon.x > box_of.x);
        assert!(icon.x + icon.width < box_of.x + box_of.width);
        assert_eq!(icon.width, GLASS);
        assert_eq!(icon.center_y(), box_of.center_y());
    }
}
