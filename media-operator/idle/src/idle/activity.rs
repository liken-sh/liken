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
/// After that it draws at the energy, so the ramp down that follows an arrival
/// carries the line out with the mark's motion, and a settled screen at energy
/// 0 draws no line at all.
pub fn opacity(unit: &Unit, at: f64) -> f32 {
    match unit.activity {
        Activity::Starting | Activity::Playing => 1.0,
        Activity::Idle => energy::level(unit, at) as f32,
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

    #[test]
    fn a_settled_screen_draws_no_line() {
        let unit = playing(Activity::Idle, 0.0);
        assert_eq!(unit.title.as_deref(), Some("A Film"));
        assert_eq!(opacity(&unit, 5.0), 0.0);
    }
}
