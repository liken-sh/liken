// The strip: the browser's own layer across the top of every screen,
// right-aligned. It draws the circles of the room, a magnifying glass at
// the clock's left, then the clock. On a search wall the glass
// expands into the text field, which draws leftward from the clock with
// the glass inside its left end. The focus mark goes on whichever of the
// strip's two targets holds focus: the glass, or the field it expanded
// into, or the circles. It is the browser's layer and not a screen's, so
// every screen carries it in the same place and an up press that moves
// nothing on any screen reaches it.

use std::convert::Infallible;

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::{Color, Point, Rectangle, Theme, mouse};

use super::{left, reading};
use crate::clock::Time;
use crate::look;
use crate::views::field::{self, TextField};
use crate::views::{area, band, mark, text};

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

/// What the strip's focus stands on: one of two targets, not one per
/// person.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub enum Target {
    /// The glass, or the field it expanded into.
    #[default]
    Glass,
    /// The circles of the room, as one target.
    Circles,
}

/// The strip as one frame draws it: the reading, the text of the search
/// wall on top of the stack or nothing where the top screen is not one,
/// the room, and which target holds the browser's focus.
pub struct Strip<'a> {
    pub time: Time,
    pub field: Option<&'a TextField>,
    /// The target the focus mark goes on, or nothing while the strip does
    /// not hold the browser's focus.
    pub focus: Option<Target>,
    /// The first letter of each person in the room, in the order of the
    /// answer; one "?" where nobody is watching and the picker is not
    /// up; and none where the browser holds no answer.
    pub letters: Vec<String>,
    // Whether the letters are the one "?" that stands for nobody, which
    // draws fainter than a person's circle.
    pub asking: bool,
}

/// The side of one circle of the room: the glass's own size, so the
/// circles and the glass read as one strip.
pub const CIRCLE: f32 = GLASS;

// The gap between two circles, narrow enough that the row reads as one
// thing.
const CIRCLE_GAP: f32 = 6.0;

// The width a row of this many circles takes, and nothing at all where
// the room is empty.
pub fn circles_width(count: usize) -> f32 {
    match count {
        0 => 0.0,
        _ => count as f32 * CIRCLE + (count - 1) as f32 * CIRCLE_GAP,
    }
}

/// The box this many circles take in a frame this wide: a row that ends a
/// margin before the glass, and nothing at all where the room is empty.
pub fn circles_at(width: f32, count: usize) -> Rectangle {
    let taken = circles_width(count);
    let right = glass_at(width).x - band::PAD;
    area(right - taken, (band::HEIGHT - CIRCLE) / 2.0, taken, CIRCLE)
}

/// The box one circle of the row draws in.
pub fn circle_at(width: f32, count: usize, index: usize) -> Rectangle {
    circle_in(circles_at(width, count), index)
}

// The box one circle of a row draws in, by its index, for a row that
// starts at this box.
pub fn circle_in(row: Rectangle, index: usize) -> Rectangle {
    area(
        row.x + index as f32 * (CIRCLE + CIRCLE_GAP),
        row.y,
        CIRCLE,
        CIRCLE,
    )
}

// One circle of the room: a muted disc with the letter on it, the way the
// strip and the continue-watching row's heading both draw a person.
pub fn circle(frame: &mut canvas::Frame<Renderer>, at: Rectangle, letter: &str) {
    disc(frame, at, letter, look::muted());
}

// A disc of this fill with the letter on it. The "?" of nobody draws in
// the faint fill, and a person's circle in the muted one.
fn disc(frame: &mut canvas::Frame<Renderer>, at: Rectangle, letter: &str, fill: Color) {
    frame.fill(
        &canvas::Path::circle(Point::new(at.center_x(), at.center_y()), at.width / 2.0),
        fill,
    );
    text::shown(
        frame,
        letter,
        area(
            at.x,
            at.center_y() - text::height(1, look::FACE) / 2.0,
            at.width,
            text::height(1, look::FACE),
        ),
        look::FACE,
        look::text(),
    );
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
        if self.focus == Some(Target::Glass) {
            mark(&mut frame, marked);
        }
        // The field draws leftward from the clock over the same band, so
        // the circles stand down while a search wall is on top.
        if !self.letters.is_empty() && self.field.is_none() {
            let count = self.letters.len();
            let fill = match self.asking {
                true => look::faint(),
                false => look::muted(),
            };
            for (index, letter) in self.letters.iter().enumerate() {
                disc(
                    &mut frame,
                    circle_at(bounds.width, count, index),
                    letter,
                    fill,
                );
            }
            if self.focus == Some(Target::Circles) {
                mark(&mut frame, circles_at(bounds.width, count));
            }
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
    fn the_circles_of_the_room_end_a_margin_before_the_glass() {
        let row = circles_at(WIDTH, 3);

        assert_eq!(row.x + row.width + band::PAD, glass_at(WIDTH).x);
        assert_eq!(row.width, 3.0 * CIRCLE + 2.0 * CIRCLE_GAP);
        assert_eq!(row.height, CIRCLE);
        assert_eq!(row.center_y(), glass_at(WIDTH).center_y());
    }

    #[test]
    fn a_room_of_nobody_takes_no_room() {
        let row = circles_at(WIDTH, 0);

        assert_eq!(row.width, 0.0);
        assert_eq!(row.x + band::PAD, glass_at(WIDTH).x);
    }

    #[test]
    fn one_circle_sits_beside_the_next_and_the_last_ends_the_row() {
        let row = circles_at(WIDTH, 2);
        let first = circle_at(WIDTH, 2, 0);
        let last = circle_at(WIDTH, 2, 1);

        assert_eq!(first.x, row.x);
        assert_eq!(first.width, CIRCLE);
        assert_eq!(last.x, first.x + CIRCLE + CIRCLE_GAP);
        assert_eq!(last.x + last.width, row.x + row.width);
        assert_eq!(last.y, first.y);
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
