//! The activity line under the clock, which shows the title of the `Play`
//! while it starts or plays. While the playback pod pulls its image and
//! starts, the line tells a person that the press started something.

use iced_winit::core::Point;

use super::energy;
use super::{Frame, Layout};
use crate::look;
use crate::unit::Unit;
use media_screen::status::Activity;

/// The opacity of the line: 1 while the activity is `Starting` or
/// `Playing`. After a move to `Idle` it fades to 0 over
/// [`energy::RAMP_DOWN`], on the same curve as the mark's ramp down, so the
/// line and the mark leave together.
///
/// The fade starts from 1 and not from the mark's energy, because a `Play`
/// that ends while `Starting` leaves the energy partway up, and the line
/// would drop in that frame.
pub fn opacity(unit: &Unit, at: f64) -> f32 {
    match unit.activity {
        Activity::Starting | Activity::Playing => 1.0,
        Activity::Idle => (1.0 - energy::ease((at - unit.ramp.since) / energy::RAMP_DOWN)) as f32,
    }
}

/// The text of the line, with typographic quotation marks and an ellipsis
/// character.
fn line(title: &str) -> String {
    format!("Playing \u{201c}{title}\u{201d}\u{2026}")
}

/// Draws the line one `look::LINE_PITCH` below the clock, aligned to its
/// right edge.
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

    /// A unit that read one status with a `Play` and `activity`.
    fn unit_with_play(activity: Activity, at: f64) -> Unit {
        let mut unit = Unit::default();
        unit.fold(
            Moment::Status(Status {
                activity,
                play: Some(Play {
                    name: "play-1".into(),
                    title: "A Film".into(),
                }),
                ..Status::default()
            }),
            at,
        );
        unit
    }

    #[test]
    fn the_line_shows_the_title_in_quotation_marks() {
        assert_eq!(line("A Film"), "Playing \u{201c}A Film\u{201d}\u{2026}");
    }

    #[test]
    fn the_line_is_opaque_while_a_play_starts_or_runs() {
        assert_eq!(opacity(&unit_with_play(Activity::Starting, 0.0), 0.1), 1.0);
        assert_eq!(opacity(&unit_with_play(Activity::Playing, 0.0), 9.0), 1.0);
    }

    #[test]
    fn the_line_fades_over_the_ramp_down() {
        let mut unit = unit_with_play(Activity::Starting, 0.0);
        unit.fold(Moment::Status(Status::default()), 1.2);

        assert_eq!(opacity(&unit, 1.2), 1.0);
        assert!(opacity(&unit, 2.45) < 1.0);
        assert_eq!(opacity(&unit, 3.7), 0.0);
    }

    /// The frame where the `Play` ends has the same opacity as the frame
    /// before it.
    #[test]
    fn a_play_that_ends_while_starting_fades_the_line_from_full_opacity() {
        let mut unit = unit_with_play(Activity::Starting, 0.0);
        assert_eq!(opacity(&unit, 0.6), 1.0);

        unit.fold(Moment::Status(Status::default()), 0.6);

        assert_eq!(opacity(&unit, 0.6), 1.0);
        assert_eq!(opacity(&unit, 1.85), 0.5);
        assert_eq!(opacity(&unit, 3.1), 0.0);
    }

    #[test]
    fn a_settled_screen_draws_no_line() {
        let unit = unit_with_play(Activity::Idle, 0.0);
        assert_eq!(unit.title.as_deref(), Some("A Film"));
        assert_eq!(opacity(&unit, 5.0), 0.0);
    }
}
