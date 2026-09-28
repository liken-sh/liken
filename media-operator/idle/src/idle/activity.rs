//! The activity line: the one line the screen draws while a `Play` starts or
//! runs.
//!
//! The line draws under the clock and names the title, so the seconds a
//! playback pod pulls and starts read as work in progress and not as a screen
//! that ignored the button. The unit holds the last title after the `Play`
//! ends, because the line leaves with the mark's motion rather than vanishing
//! on the frame the activity changes.

use iced_winit::core::Point;

use super::energy;
use super::{Frame, Layout};
use crate::look;
use crate::unit::Unit;
use media_screen::status::Activity;

/// How opaque the line draws, from 0 clear to 1 full.
///
/// While a `Play` starts or runs the line is opaque, whatever the energy is.
/// The move to `Idle` starts its fade, over the same ramp down the mark
/// returns on, so the line leaves with the motion and a settled screen draws
/// no line at all.
///
/// The fade leaves from full and not from the energy the mark holds. The line
/// was full for the whole of the `Play`, and the mark may not be: a `Play`
/// that ends before it played leaves the mark part way up its ramp, and a line
/// that took that level would step down in the frame the `Play` ended.
pub fn opacity(unit: &Unit, at: f64) -> f32 {
    match unit.activity {
        Activity::Starting | Activity::Playing => 1.0,
        Activity::Idle => (1.0 - energy::ease((at - unit.ramp.since) / energy::RAMP_DOWN)) as f32,
    }
}

/// The line the screen draws for one title.
///
/// The title takes real quotation marks and one ellipsis character, because the
/// line is read from across a room.
fn line(title: &str) -> String {
    format!("Playing \u{201c}{title}\u{201d}\u{2026}")
}

/// Draw the element into the frame, in canvas units.
///
/// The line shares the clock's right edge and hangs one line pitch under it, so
/// the two read as one column and neither touches the other.
pub fn draw(frame: &mut Frame, layout: &Layout, unit: &Unit, at: f64, light: f32) {
    let Some(title) = &unit.title else {
        return;
    };
    let opacity = opacity(unit, at);
    if opacity <= 0.0 {
        return;
    }

    frame.fill_text(look::line(
        line(title),
        Point::new(layout.right(), look::MARGIN_Y + look::LINE_PITCH),
        look::Anchor::TopRight,
        look::SMALL,
        look::under(look::faded(look::text(), opacity), light),
    ));
}

#[cfg(test)]
mod tests {
    use super::*;
    use media_screen::Moment;
    use media_screen::status::{Play, Status};

    /// A unit that read one status naming a `Play`.
    fn playing(activity: Activity, at: f64) -> Unit {
        let mut unit = Unit::default();
        unit.fold(
            Moment::Status(Status {
                activity,
                play: Some(Play {
                    name: "den-tv-1".into(),
                    title: "A Film".into(),
                }),
                ..Status::default()
            }),
            at,
        );
        unit
    }

    #[test]
    fn the_line_names_the_title_in_quotation_marks() {
        assert_eq!(line("A Film"), "Playing \u{201c}A Film\u{201d}\u{2026}");
    }

    #[test]
    fn the_line_is_opaque_while_a_play_starts_or_runs() {
        assert_eq!(opacity(&playing(Activity::Starting, 0.0), 0.1), 1.0);
        assert_eq!(opacity(&playing(Activity::Playing, 0.0), 9.0), 1.0);
    }

    #[test]
    fn the_line_leaves_with_the_marks_motion() {
        let mut unit = playing(Activity::Starting, 0.0);
        unit.fold(Moment::Status(Status::default()), 1.2);

        assert_eq!(opacity(&unit, 1.2), 1.0);
        assert!(opacity(&unit, 2.45) < 1.0);
        assert_eq!(opacity(&unit, 3.7), 0.0);
    }

    /// A `Play` that ends before it played leaves the mark part way up its
    /// ramp, and the line was full through the whole of it. So the line
    /// leaves from full here too, and the frame the `Play` ended on reads
    /// the same alpha as the frame before it.
    #[test]
    fn a_play_that_ends_before_it_played_carries_the_line_out_from_full() {
        let mut unit = playing(Activity::Starting, 0.0);
        assert_eq!(opacity(&unit, 0.6), 1.0);

        unit.fold(Moment::Status(Status::default()), 0.6);

        assert_eq!(opacity(&unit, 0.6), 1.0);
        assert_eq!(opacity(&unit, 1.85), 0.5);
        assert_eq!(opacity(&unit, 3.1), 0.0);
    }

    #[test]
    fn a_settled_screen_draws_no_line() {
        let unit = playing(Activity::Idle, 0.0);
        assert_eq!(unit.title.as_deref(), Some("A Film"));
        assert_eq!(opacity(&unit, 5.0), 0.0);
    }
}
