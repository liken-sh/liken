// The focus outline in motion. Each of its four edges is a value of its
// own, so an outline that moves between slots of two sizes changes size on
// the way. Every edge moves over the same length on the same curve, so the
// outline glides as one piece. Nothing overshoots, so the outline never
// draws past the slot it lands on.

use iced_winit::core::Rectangle;

use super::Glide;
use crate::look;

/// The rectangle the focus stroke follows, edge by edge.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Outline {
    left: Glide,
    top: Glide,
    right: Glide,
    bottom: Glide,
    // The rectangle the outline moves to, as it was given. The browser
    // compares it with where focus is now to tell a press from a resize,
    // and a rectangle rebuilt from the edges can differ from the one
    // given in the last bits of its width, which would read as a move.
    to: Rectangle,
}

impl Outline {
    /// An outline at rest around this rectangle.
    pub fn still(around: Rectangle) -> Self {
        Self {
            left: Glide::still(around.x),
            top: Glide::still(around.y),
            right: Glide::still(around.x + around.width),
            bottom: Glide::still(around.y + around.height),
            to: around,
        }
    }

    /// The rectangle the outline moves to, which is where it rests once
    /// the motion ends.
    pub fn to(&self) -> Rectangle {
        self.to
    }

    /// The rectangle the outline draws at this second, which is the one it
    /// was sent to once every edge has arrived.
    pub fn at(&self, clock: f64) -> Rectangle {
        if clock >= self.ends() {
            return self.to;
        }
        edges(
            self.left.at(clock),
            self.top.at(clock),
            self.right.at(clock),
            self.bottom.at(clock),
        )
    }

    /// The second the slowest edge arrives.
    pub fn ends(&self) -> f64 {
        [self.left, self.top, self.right, self.bottom]
            .iter()
            .map(Glide::ends)
            .fold(f64::NEG_INFINITY, f64::max)
    }

    /// Glide to this rectangle from the one drawn at this second, every
    /// edge over the same length. `pace` scales the length, and is below 1
    /// while a key is held.
    pub fn travel(&mut self, to: Rectangle, clock: f64, pace: f64) {
        let length = look::GLIDE * pace;
        self.left.go(to.x, clock, length);
        self.right.go(to.x + to.width, clock, length);
        self.top.go(to.y, clock, length);
        self.bottom.go(to.y + to.height, clock, length);
        self.to = to;
    }
}

fn edges(left: f32, top: f32, right: f32, bottom: f32) -> Rectangle {
    Rectangle {
        x: left,
        y: top,
        width: right - left,
        height: bottom - top,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn slot(x: f32, y: f32) -> Rectangle {
        Rectangle {
            x,
            y,
            width: 100.0,
            height: 150.0,
        }
    }

    // The outline after one press from the slot at 0 to this one.
    fn moved(to: Rectangle) -> Outline {
        let mut outline = Outline::still(slot(0.0, 0.0));
        outline.travel(to, 0.0, 1.0);
        outline
    }

    #[test]
    fn every_edge_moves_together_so_the_outline_keeps_its_size() {
        for to in [slot(200.0, 0.0), slot(-200.0, 0.0), slot(0.0, 300.0)] {
            let outline = moved(to);
            for step in 1..10 {
                let drawn = outline.at(look::GLIDE * f64::from(step) / 10.0);
                assert!((drawn.width - 100.0).abs() < 1e-3, "{drawn:?}");
                assert!((drawn.height - 150.0).abs() < 1e-3, "{drawn:?}");
            }
        }
    }

    #[test]
    fn between_slots_of_two_sizes_the_outline_changes_size_on_the_way() {
        let to = Rectangle {
            width: 300.0,
            ..slot(200.0, 0.0)
        };
        let half = moved(to).at(look::GLIDE / 2.0);
        assert!(half.width > 100.0 && half.width < 300.0, "{half:?}");
    }

    #[test]
    fn the_outline_lands_on_the_new_slot_when_the_glide_ends() {
        for to in [slot(200.0, 0.0), slot(-200.0, 0.0), slot(0.0, 300.0)] {
            let outline = moved(to);
            assert_eq!(outline.at(look::GLIDE), to);
            assert_eq!(outline.ends(), look::GLIDE);
        }
    }

    #[test]
    fn the_outline_never_draws_past_the_two_slots() {
        let outline = moved(slot(200.0, 0.0));
        for step in 0..=24 {
            let drawn = outline.at(f64::from(step) / 100.0);
            assert!(drawn.x >= 0.0 && drawn.x <= 200.0, "{drawn:?}");
            assert!(drawn.x + drawn.width <= 300.0, "{drawn:?}");
        }
    }

    #[test]
    fn a_press_mid_travel_starts_from_the_rectangle_drawn_then() {
        let mut outline = moved(slot(200.0, 0.0));
        let drawn = outline.at(0.1);
        outline.travel(slot(400.0, 0.0), 0.1, 1.0);
        assert_eq!(outline.at(0.1), drawn);
        assert_eq!(outline.at(0.1 + look::GLIDE), slot(400.0, 0.0));
    }

    // A slot whose left edge and width are no sum of powers of two, as a
    // wall of six columns places them. Its right edge, less its left
    // edge, is not its width in the last bits of a float.
    fn uneven() -> Rectangle {
        Rectangle {
            x: 385.89044,
            y: 406.0,
            width: 260.54794,
            height: 384.8219,
        }
    }

    #[test]
    fn the_outline_moves_to_the_very_rectangle_it_was_sent_to() {
        assert_eq!(Outline::still(uneven()).to(), uneven());
        assert_eq!(moved(uneven()).to(), uneven());
        assert_eq!(moved(uneven()).at(look::GLIDE), uneven());
    }

    #[test]
    fn a_held_key_shortens_every_length_by_its_pace() {
        let mut outline = Outline::still(slot(0.0, 0.0));
        outline.travel(slot(200.0, 0.0), 0.0, 0.5);
        assert_eq!(outline.ends(), look::GLIDE * 0.5);
        assert_eq!(outline.at(look::GLIDE * 0.5), slot(200.0, 0.0));
    }
}
