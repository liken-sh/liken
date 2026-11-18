//! The volume row: a speaker icon, a short bar, and the level as a number.
//!
//! The row fades in on a volume change and out after a delay, and no other
//! element moves when it appears. This client only reads the level and the
//! muted flag from the bus. `media-operator` turns each volume key into a
//! request to the unit's `Receiver` or `Sink`, and publishes the level that
//! the device reports, as a fraction from 0.0 to 1.0 of the device's maximum.

use iced_widget::canvas::{LineJoin, Path, Stroke, Style};
use iced_winit::core::{Color, Point, Rectangle};

use super::{Frame, Layout};
use crate::look;
use crate::unit::Unit;

/// The seconds that the fade in and the fade out take.
const FADE_IN: f64 = 0.35;
const FADE_OUT: f64 = 0.6;
/// The seconds from the last volume change until the fade out starts. Each
/// change restarts the wait, so repeated presses keep the row on screen.
const HOLD: f64 = 4.0;

/// The width reserved for the number at the right margin, so the bar and the
/// icon stay in place when the number changes between one and three digits.
const NUMBER_WIDTH: f32 = 84.0;
/// The bar is short because the number beside it gives the exact level.
const BAR_WIDTH: f32 = 220.0;
const BAR_HEIGHT: f32 = 12.0;
const BAR_RADIUS: f32 = 3.0;
/// The size of the icon, and the gap between the icon and the bar.
const ICON_WIDTH: f32 = 26.0;
const ICON_HEIGHT: f32 = 30.0;
const ICON_GAP: f32 = 16.0;
/// The height of the number's line box, in canvas pixels. The bar and the
/// icon are centred on its middle and the background is sized from it, so
/// the parts stay aligned at any type size.
const NUMBER_BOX: f32 = look::line_box(look::SMALL);
/// The row draws over whatever is on the screen, so it has its own dark
/// background to keep the icon and the number readable on a bright frame.
/// These are the background's padding and corner radius.
const PAD_X: f32 = 24.0;
const PAD_Y: f32 = 12.0;
const SURFACE_RADIUS: f32 = 14.0;

/// The speaker icon as one closed polygon, in the icon's own coordinates.
/// The image has no icon font, so the icon is a path.
const SPEAKER: [(f32, f32); 6] = [
    (0.0, 10.0),
    (10.0, 10.0),
    (22.0, 0.0),
    (22.0, 30.0),
    (10.0, 20.0),
    (0.0, 20.0),
];
/// The slash drawn across the speaker icon when the volume is muted.
const SLASH: [(f32, f32); 4] = [(2.0, 24.0), (24.0, 2.0), (24.0, 8.0), (2.0, 30.0)];
/// The width of the dark outline around the slash. The slash has the
/// speaker's color, so without the outline the two read as one shape.
const SLASH_BORDER: f32 = 2.0;

/// The opacity of the row at second `at`, from 0 for hidden to 1 for full.
///
/// Only a volume change shows the row, and only when the receiver does not
/// draw its own overlay. The retained level that a client reads when it
/// connects is not a change, so the row stays hidden when the pod starts.
pub fn fade(unit: &Unit, at: f64) -> f64 {
    let Some(pressed) = unit.pressed else {
        return 0.0;
    };

    let since = at - pressed;
    let arriving = unit.pressed_from + since / FADE_IN;
    let leaving = 1.0 - (since - HOLD) / FADE_OUT;

    arriving.min(leaving).clamp(0.0, 1.0)
}

/// The second at which the row next changes, for [`super::Idle::next_frame`].
///
/// During a fade it returns `at`. While the row is fully shown it returns
/// the second the fade out starts, so the loop sleeps while a person reads
/// the row. Before the first volume change and after the fade out it returns
/// `None`.
///
/// A new press during the hold still draws at once, because the harness
/// discards its scheduled second whenever `pump` applies a message.
pub fn next_frame(unit: &Unit, at: f64) -> Option<f64> {
    let pressed = unit.pressed?;
    // A fade in that starts above zero, from `pressed_from`, reaches full
    // sooner by the part of the fade it skips.
    let full = pressed + (1.0 - unit.pressed_from) * FADE_IN;
    let leaving = pressed + HOLD;

    if at < full {
        Some(at)
    } else if at < leaving {
        Some(leaving)
    } else if at < leaving + FADE_OUT {
        Some(at)
    } else {
        None
    }
}

/// The width of the filled part of the bar, in canvas pixels.
fn filled(level: f64) -> f32 {
    BAR_WIDTH * level.clamp(0.0, 1.0) as f32
}

/// The positions of the parts of the row. They are measured from the right
/// margin, which moves with the canvas width, so `draw` computes them each
/// time.
struct Row {
    surface: Rectangle,
    bar: Rectangle,
    icon: Point,
    number: Point,
}

fn row(layout: &Layout) -> Row {
    // The row is the third line of the top-right column, under the clock and
    // the activity line.
    let top = look::MARGIN_Y + 2.0 * look::LINE_PITCH;
    let right = layout.right();
    let middle = top + NUMBER_BOX / 2.0;

    let bar_x = right - NUMBER_WIDTH - BAR_WIDTH;
    let icon_x = bar_x - ICON_GAP - ICON_WIDTH;
    let surface_x = icon_x - PAD_X;

    Row {
        surface: Rectangle {
            x: surface_x,
            y: top - PAD_Y,
            width: right + PAD_X - surface_x,
            // The number's line box is the tallest part of the row.
            height: NUMBER_BOX + 2.0 * PAD_Y,
        },
        bar: Rectangle {
            x: bar_x,
            y: middle - BAR_HEIGHT / 2.0,
            width: BAR_WIDTH,
            height: BAR_HEIGHT,
        },
        icon: Point::new(icon_x, middle - ICON_HEIGHT / 2.0),
        number: Point::new(right, top),
    }
}

/// A closed polygon from `points`, in icon coordinates, moved to `at`.
fn polygon(points: &[(f32, f32)], at: Point) -> Path {
    Path::new(|path| {
        for (index, (x, y)) in points.iter().enumerate() {
            let point = Point::new(at.x + x, at.y + y);
            match index {
                0 => path.move_to(point),
                _ => path.line_to(point),
            }
        }
        path.close();
    })
}

/// Draws the row into `frame`, in canvas units.
///
/// The bar shows the level even when the volume is muted, and only the icon
/// shows the mute. Every part takes the row's fade and then `light`, the
/// dimming factor that every element of the screen uses.
pub fn draw(frame: &mut Frame, layout: &Layout, unit: &Unit, at: f64, light: f32) {
    let fade = fade(unit, at) as f32;
    if fade <= 0.0 {
        return;
    }

    let row = row(layout);
    let ink = match unit.volume.muted {
        true => look::muted(),
        false => look::text(),
    };
    let shaded = |color, alpha| look::under(look::faded(color, alpha), light);

    frame.fill(
        &look::rounded(row.surface, SURFACE_RADIUS),
        shaded(Color::BLACK, look::SURFACE * fade),
    );
    frame.fill(
        &look::rounded(row.bar, BAR_RADIUS),
        shaded(look::track(), look::TRACK_OPACITY * fade),
    );

    let filled = filled(unit.volume.level);
    if filled >= 1.0 {
        frame.fill(
            &look::rounded(
                Rectangle {
                    width: filled,
                    ..row.bar
                },
                BAR_RADIUS,
            ),
            shaded(look::fill(), fade),
        );
    }

    frame.fill(&polygon(&SPEAKER, row.icon), shaded(ink, fade));
    if unit.volume.muted {
        let slash = polygon(&SLASH, row.icon);
        // iced centres a stroke on the path, so the stroke is twice the
        // border width and the slash's fill then covers the inner half. The
        // width is in surface pixels, as `Layout` explains for the canvas
        // scale.
        frame.stroke(
            &slash,
            Stroke {
                width: 2.0 * SLASH_BORDER * layout.scale,
                style: Style::Solid(shaded(Color::BLACK, fade)),
                line_join: LineJoin::Round,
                ..Stroke::default()
            },
        );
        frame.fill(&slash, shaded(ink, fade));
    }

    frame.fill_text(look::line(
        unit.volume.percent().to_string(),
        row.number,
        look::Anchor::TopRight,
        look::SMALL,
        shaded(look::text(), fade),
    ));
}

#[cfg(test)]
mod tests {
    use super::*;
    use iced_winit::core::Size;
    use media_screen::Moment;
    use media_screen::volume::{Indicator, Volume};

    // The right edge of the row, on a 1920 canvas.
    const RIGHT: f32 = 1920.0 - look::MARGIN_X;

    /// A unit that applied a retained level and then a live volume change at
    /// `at`.
    fn pressed(at: f64) -> Unit {
        let mut unit = Unit::default();
        let volume = Volume {
            level: 0.4,
            muted: false,
            ..Volume::default()
        };
        unit.fold(
            Moment::Level {
                volume,
                pressed: false,
            },
            0.0,
        );
        unit.fold(
            Moment::Level {
                volume,
                pressed: true,
            },
            at,
        );
        unit
    }

    #[test]
    fn a_retained_level_shows_no_row() {
        let mut unit = Unit::default();
        unit.fold(
            Moment::Level {
                volume: Volume::default(),
                pressed: false,
            },
            1.0,
        );
        assert_eq!(fade(&unit, 1.0), 0.0);
        assert_eq!(fade(&unit, 2.0), 0.0);
    }

    /// A level message from the bus.
    fn level(level: f64, indicator: Indicator, pressed: bool) -> Moment {
        Moment::Level {
            volume: Volume {
                level,
                muted: false,
                indicator,
            },
            pressed,
        }
    }

    /// When a receiver draws its own overlay on the TV, the row stays hidden
    /// while the unit still tracks the level. A message that changes only the
    /// indicator does not show the row either.
    #[test]
    fn a_level_the_receiver_draws_shows_no_row() {
        let mut unit = Unit::default();
        unit.fold(level(0.4, Indicator::Player, false), 0.0);
        unit.fold(level(0.45, Indicator::Receiver, true), 1.0);
        assert_eq!(fade(&unit, 2.0), 0.0);
        assert_eq!(unit.volume.level, 0.45);

        unit.fold(level(0.45, Indicator::Player, true), 3.0);
        assert_eq!(fade(&unit, 4.0), 0.0);

        unit.fold(level(0.5, Indicator::Player, true), 5.0);
        assert_eq!(fade(&unit, 6.0), 1.0);
    }

    #[test]
    fn a_press_brings_the_row_in_over_350_ms() {
        let unit = pressed(1.0);
        assert_eq!(fade(&unit, 1.0), 0.0);
        assert!((fade(&unit, 1.175) - 0.5).abs() < 1e-9);
        assert_eq!(fade(&unit, 1.35), 1.0);
        assert_eq!(fade(&unit, 4.0), 1.0);
    }

    #[test]
    fn the_row_leaves_four_seconds_after_the_last_press() {
        let unit = pressed(1.0);
        assert_eq!(fade(&unit, 5.0), 1.0);
        assert!((fade(&unit, 5.3) - 0.5).abs() < 1e-9);
        assert!(fade(&unit, 5.6) < 1e-9);
        assert_eq!(fade(&unit, 60.0), 0.0);
    }

    #[test]
    fn the_bar_fill_is_clamped_to_the_bar_width() {
        assert_eq!(filled(0.0), 0.0);
        assert_eq!(filled(0.5), BAR_WIDTH / 2.0);
        assert_eq!(filled(1.0), BAR_WIDTH);
        assert_eq!(filled(1.4), BAR_WIDTH);
    }

    #[test]
    fn during_a_fade_the_next_frame_is_now() {
        let unit = pressed(10.0);
        assert_eq!(next_frame(&unit, 10.0), Some(10.0));
        assert_eq!(next_frame(&unit, 10.34), Some(10.34));
        assert_eq!(next_frame(&unit, 14.3), Some(14.3));
    }

    #[test]
    fn during_the_hold_the_next_frame_is_the_start_of_the_fade_out() {
        let unit = pressed(10.0);
        assert_eq!(next_frame(&unit, 10.35), Some(14.0));
        assert_eq!(next_frame(&unit, 13.9), Some(14.0));
    }

    #[test]
    fn a_press_during_the_fade_out_shortens_the_fade_in() {
        let mut unit = pressed(10.0);
        // The row is halfway faded out at 14.3 seconds, and the press fades it
        // in from there, so it reaches full in half of the 350 ms.
        unit.fold(
            Moment::Level {
                volume: unit.volume,
                pressed: true,
            },
            14.3,
        );

        assert_eq!(next_frame(&unit, 14.47), Some(14.47));
        assert_eq!(next_frame(&unit, 14.48), Some(18.3));
    }

    #[test]
    fn a_row_that_has_faded_out_needs_no_frame() {
        assert_eq!(next_frame(&pressed(10.0), 14.61), None);
        assert_eq!(next_frame(&Unit::default(), 600.0), None);
    }

    #[test]
    fn the_row_is_two_line_pitches_below_the_top_margin() {
        let row = row(&Layout::for_surface(Size::new(1920.0, 1080.0)));
        assert_eq!(row.number.x, RIGHT);
        // The top margin is 90, and `look::LINE_PITCH` is 46.
        assert!((row.number.y - 182.0).abs() < 0.2, "{}", row.number.y);
        assert_eq!(row.bar.x, RIGHT - 84.0 - 220.0);
        assert_eq!(row.icon.x, row.bar.x - 16.0 - 26.0);
    }

    #[test]
    fn the_background_covers_the_three_parts_and_the_padding() {
        let row = row(&Layout::for_surface(Size::new(1920.0, 1080.0)));
        assert_eq!(row.surface.x, row.icon.x - PAD_X);
        assert_eq!(row.surface.x + row.surface.width, RIGHT + PAD_X);
        assert_eq!(row.surface.height, NUMBER_BOX + 2.0 * PAD_Y);
    }

    #[test]
    fn the_bar_and_the_icon_are_centred_on_the_number_line() {
        let row = row(&Layout::for_surface(Size::new(1920.0, 1080.0)));
        let middle = |shape: Rectangle| shape.y + shape.height / 2.0;
        assert_eq!(middle(row.bar), row.number.y + NUMBER_BOX / 2.0);
        assert_eq!(row.icon.y + ICON_HEIGHT / 2.0, middle(row.bar));
    }
}
