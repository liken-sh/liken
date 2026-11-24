//! The glide of the track panel's focus underline and of its scale's mark,
//! by the brand's rule for the focus outline in `brand/motion.md`: when
//! the focus moves, the underline glides from where it is drawn to the
//! row that takes the focus, over a short time, with no overshoot. The
//! mark glides the same way to a new delay.
//!
//! A glide is a pure function of its start, its length, the place it
//! started from, the place it goes to, and the clock. The clock counts the
//! frame ticks of `theme::FADE_TICK`, the ticks that run the display's
//! fades, so a glide asks for frames only while it moves, and a test
//! advances it by sending ticks.

/// How many ticks a glide takes: 7 ticks of 16.7 ms, the 110 ms `GLIDE`
/// of the media browser's look rounded up to a whole tick.
pub const GLIDE: u64 = 7;

/// The fewest ticks a glide takes while a key is held. A remote repeats a
/// held key faster than a glide, so each glide shortens to the time since
/// the last one started, and the underline never falls behind the remote.
/// It stops at a third of a glide, because a shorter motion is a jump.
pub const HELD_FLOOR: u64 = 3;

/// A value that a glide moves.
pub trait Glides: Copy {
    fn toward(self, to: Self, share: f32) -> Self;
}

impl Glides for f32 {
    fn toward(self, to: Self, share: f32) -> Self {
        self + (to - self) * share
    }
}

impl Glides for iced::Rectangle {
    fn toward(self, to: Self, share: f32) -> Self {
        iced::Rectangle {
            x: self.x.toward(to.x, share),
            y: self.y.toward(to.y, share),
            width: self.width.toward(to.width, share),
            height: self.height.toward(to.height, share),
        }
    }
}

/// One glide: where it starts, on which tick, and for how many ticks.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Glide<T> {
    from: T,
    start: u64,
    length: u64,
}

impl<T: Glides> Glide<T> {
    /// A glide that starts on `tick` from `drawn`, the place drawn on that
    /// tick. A glide that starts while `previous` still runs is as long as
    /// the time since `previous` started, between `HELD_FLOOR` and `GLIDE`.
    pub fn new(previous: Option<&Glide<T>>, drawn: T, tick: u64) -> Self {
        let length = match previous {
            Some(previous) if previous.running(tick) => {
                tick.saturating_sub(previous.start).clamp(HELD_FLOOR, GLIDE)
            }
            _ => GLIDE,
        };
        Self {
            from: drawn,
            start: tick,
            length,
        }
    }

    /// Where the glide toward `to` is drawn on `tick`. It eases on a
    /// smoothstep, so it starts and settles without a hard edge.
    pub fn at(&self, to: T, tick: u64) -> T {
        let share = tick.saturating_sub(self.start) as f32 / self.length as f32;
        let share = share.clamp(0.0, 1.0);
        self.from.toward(to, share * share * (3.0 - 2.0 * share))
    }

    pub fn running(&self, tick: u64) -> bool {
        tick < self.start + self.length
    }
}

/// Where a value that may be gliding is drawn on `tick`: at `to` when no
/// glide runs.
pub fn drawn<T: Glides>(glide: Option<&Glide<T>>, to: T, tick: u64) -> T {
    glide.map_or(to, |glide| glide.at(to, tick))
}

#[cfg(test)]
mod tests {
    use super::*;

    /// A glide starts where the value was drawn, eases, and lands on its
    /// target after `GLIDE` ticks with no overshoot.
    #[test]
    fn a_glide_eases_from_where_it_was_drawn_to_its_target() {
        let glide = Glide::new(None, 0.0_f32, 10);
        let path: Vec<f32> = (10..=18).map(|tick| glide.at(100.0, tick)).collect();
        assert_eq!(path[0], 0.0);
        assert_eq!(path[7], 100.0);
        assert_eq!(path[8], 100.0);
        assert!(
            path.windows(2).all(|pair| pair[0] <= pair[1]),
            "the path {path:?} goes back, want it to move one way"
        );
        assert!(glide.running(16));
        assert!(!glide.running(17));
    }

    /// A press that comes while a glide runs starts from where the value
    /// is drawn then, and its glide is as long as the time since the last
    /// one started, so a held key never leaves the value behind.
    #[test]
    fn a_held_key_shortens_each_glide_and_starts_from_where_it_is_drawn() {
        let first = Glide::new(None, 0.0_f32, 0);
        let drawn = first.at(100.0, 2);
        let second = Glide::new(Some(&first), drawn, 2);
        assert_eq!(second.at(200.0, 2), drawn);
        assert_eq!(second.length, HELD_FLOOR);
        let fourth_tick = Glide::new(Some(&second), second.at(200.0, 4), 4);
        assert_eq!(fourth_tick.length, HELD_FLOOR);
        let late = Glide::new(Some(&first), 100.0, 40);
        assert_eq!(late.length, GLIDE);
    }

    #[test]
    fn a_value_with_no_glide_is_drawn_at_its_target() {
        assert_eq!(drawn::<f32>(None, 5.0, 3), 5.0);
        let rectangle = iced::Rectangle::new(iced::Point::ORIGIN, iced::Size::new(10.0, 4.0));
        let wide = iced::Rectangle::new(iced::Point::new(20.0, 0.0), iced::Size::new(30.0, 4.0));
        let glide = Glide::new(None, rectangle, 0);
        assert_eq!(glide.at(wide, 7), wide);
        assert_eq!(drawn(Some(&glide), wide, 0), rectangle);
    }
}
