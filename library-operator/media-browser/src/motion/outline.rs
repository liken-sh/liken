// The focus outline in motion. Each of its four edges is a value of its
// own, so the edge on the side a press points to can arrive first and the
// edge behind it can follow. For a moment the outline spans both slots,
// then it closes on the new one. Nothing overshoots, so the outline never
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
}

impl Outline {
    /// An outline at rest around this rectangle.
    pub fn still(around: Rectangle) -> Self {
        Self {
            left: Glide::still(around.x),
            top: Glide::still(around.y),
            right: Glide::still(around.x + around.width),
            bottom: Glide::still(around.y + around.height),
        }
    }

    /// The rectangle the outline moves to, which is where it rests once
    /// the motion ends.
    pub fn to(&self) -> Rectangle {
        edges(
            self.left.to(),
            self.top.to(),
            self.right.to(),
            self.bottom.to(),
        )
    }

    /// The rectangle the outline draws at this second.
    pub fn at(&self, clock: f64) -> Rectangle {
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

    /// Travel to this rectangle from the one drawn at this second. On each
    /// axis the edge on the side of travel leads and the opposite edge
    /// follows; an axis the outline does not cross changes size on the
    /// lead's length. `pace` scales every length, and is below 1 while a
    /// key is held.
    pub fn travel(&mut self, to: Rectangle, clock: f64, pace: f64) {
        let now = self.at(clock);
        let lead = look::LEAD * pace;
        let follow = look::FOLLOW * pace;
        let (left, right) = lengths(to.center_x() - now.center_x(), lead, follow);
        let (top, bottom) = lengths(to.center_y() - now.center_y(), lead, follow);
        self.left.go(to.x, clock, left);
        self.right.go(to.x + to.width, clock, right);
        self.top.go(to.y, clock, top);
        self.bottom.go(to.y + to.height, clock, bottom);
    }
}

// The lengths of the near and the far edge of one axis, for a move of this
// many pixels along it toward the far edge's side.
fn lengths(toward_far: f32, lead: f64, follow: f64) -> (f64, f64) {
    match toward_far {
        moved if moved > 0.0 => (follow, lead),
        moved if moved < 0.0 => (lead, follow),
        _ => (lead, lead),
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
    fn a_right_press_moves_the_right_edge_first_and_the_left_edge_after() {
        let outline = moved(slot(200.0, 0.0));
        let half = outline.at(look::LEAD / 2.0);
        assert_eq!(half.x + half.width, 200.0);
        assert!(half.x < 100.0, "{half:?}");

        let led = outline.at(look::LEAD);
        assert_eq!(led.x + led.width, 300.0);
        assert!(led.x < 200.0, "{led:?}");
        assert_eq!(led.y, 0.0);
        assert_eq!(led.height, 150.0);
    }

    #[test]
    fn the_outline_lands_on_the_new_slot_when_the_follow_ends() {
        for to in [slot(200.0, 0.0), slot(-200.0, 0.0), slot(0.0, 300.0)] {
            let outline = moved(to);
            assert_eq!(outline.at(look::FOLLOW), to);
            assert_eq!(outline.ends(), look::FOLLOW);
        }
    }

    #[test]
    fn a_left_press_leads_with_the_left_edge() {
        let outline = moved(slot(-200.0, 0.0));
        let led = outline.at(look::LEAD);
        assert_eq!(led.x, -200.0);
        assert!(led.x + led.width > -100.0, "{led:?}");
    }

    #[test]
    fn a_down_press_leads_with_the_bottom_edge() {
        let outline = moved(slot(0.0, 300.0));
        let led = outline.at(look::LEAD);
        assert_eq!(led.y + led.height, 450.0);
        assert!(led.y < 300.0, "{led:?}");
        assert_eq!(led.x, 0.0);
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
        assert_eq!(outline.at(0.1 + look::FOLLOW), slot(400.0, 0.0));
    }

    #[test]
    fn a_held_key_shortens_every_length_by_its_pace() {
        let mut outline = Outline::still(slot(0.0, 0.0));
        outline.travel(slot(200.0, 0.0), 0.0, 0.5);
        assert_eq!(outline.ends(), look::FOLLOW * 0.5);
        assert_eq!(outline.at(look::FOLLOW * 0.5), slot(200.0, 0.0));
    }
}
