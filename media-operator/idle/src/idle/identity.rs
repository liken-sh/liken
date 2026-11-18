//! The identity block in the bottom-left corner of the idle screen. It
//! shows the unit's name above a list of its parts (screens, speakers,
//! controllers), so a person can see what the unit plays through while
//! nothing plays.
//!
//! A part that reports whether it is connected, such as a controller,
//! draws dim while it is disconnected and eases back to full brightness
//! when it reconnects. A part that does not report it, such as a wired
//! screen, always draws at full brightness.
//!
//! A part that reports a battery level also draws a small bar in the
//! left margin, with no number. Every bar ends at the same column, so
//! the charge of two controllers can be compared at a glance.
//!
//! The unit's status message marks at most one part as focused: the
//! controller whose presses drive this unit. That line gets a small
//! hexagon in the left margin. Before the first status arrives, the
//! unit has only the names from the environment and no focus, so no
//! marker draws.
//!
//! Every color is computed from the clock and the timestamps in `Part`,
//! so a captured frame is reproducible and a skipped frame changes
//! nothing.

use iced_widget::canvas::Path;
use iced_winit::core::{Color, Point, Rectangle};

use super::{Frame, Layout};
use crate::look;
use crate::unit::{Part, Unit};

/// The left edge every line of the block is flush with.
const LEFT: f32 = look::MARGIN_X;

/// The unit's name is one size larger than the parts, so it reads as the
/// title of the list.
const HEADER_SIZE: f32 = look::LABEL;

/// The line box height of a part's name, in canvas pixels. It is 4 less
/// than `look::SMALL_BOX`, so the parts read slightly lighter than other
/// small text. Most distances below are fractions of this height.
const ITEM_BOX: f32 = look::SMALL_BOX - 4.0;

const ITEM_SIZE: f32 = look::type_size(ITEM_BOX);

const ITEM_STEP: f32 = 1.1 * ITEM_BOX;

/// The distance from the header to the first part. It is wider than
/// `ITEM_STEP`, so the name reads as the title of the list.
const HEADER_STEP: f32 = 1.3 * ITEM_BOX;

/// The focus marker's radius, from its center to a vertex.
const MARKER_R: f32 = ITEM_BOX / 5.0;

/// The distance from the marker's center to `LEFT`. The marker draws in
/// the margin, so the names stay flush left with or without a marker.
const MARKER_GAP: f32 = 18.0;

/// How far above the bottom of the line box the marker's center draws,
/// which puts it at the middle of the lowercase letters.
const MARKER_RISE: f32 = 0.42 * ITEM_BOX;

/// The space between a bar's right end and the marker's left edge. Every
/// bar keeps it, with or without a marker, so all bars end on one column.
const BAR_GAP: f32 = 12.0;

const BAR_RIGHT: f32 = LEFT - MARKER_GAP - MARKER_R - BAR_GAP;

/// The bar is two characters long and an eighth of a line box tall, so it
/// reads as a measure beside the name and not as a second line.
const BAR_LENGTH: f32 = 2.0 * ITEM_SIZE;
const BAR_HEIGHT: f32 = ITEM_BOX / 8.0;

/// Half the height, which rounds the ends to semicircles.
const BAR_RADIUS: f32 = BAR_HEIGHT / 2.0;

/// The battery level, in percent, that fills the bar.
const FULL_CHARGE: f32 = 100.0;

/// How long a part takes to ease between dim and full brightness.
const DIM_SECONDS: f64 = 0.4;

/// A beat is a short white pulse: it rises over `BEAT_RISE_SECONDS` and
/// falls over `BEAT_FALL_SECONDS`. The rise is shorter, so a controller
/// that reconnects shows one bright pulse and not a blink.
const BEAT_RISE_SECONDS: f64 = 0.12;
const BEAT_FALL_SECONDS: f64 = 0.5;

/// The marker's opacity at rest on a line at full brightness.
const MARKER_REST: f32 = look::DIM;

/// The marker's opacity at rest on a dim line. It is the same fraction
/// of the dim name as `MARKER_REST` is of the full name, so the marker
/// never draws brighter than the name it marks. The value matches the
/// ASS alpha byte 0xE1.
const MARKER_DIM: f32 = look::DIM * look::DIM;

/// Draws the block into `frame`, in canvas units. The last part is on
/// the bottom margin and the rest stack upward from it.
///
/// It draws nothing while the unit has no name, so a client that has
/// neither a name from the environment nor a status shows only the
/// clock.
pub fn draw(frame: &mut Frame, layout: &Layout, unit: &Unit, at: f64, light: f32) {
    if unit.name.is_empty() {
        return;
    }

    let bottom = layout.bottom();
    for (above, part) in unit.parts.iter().rev().enumerate() {
        let y = item_y(bottom, above);
        let drawn = line(part, y, at);

        text(
            frame,
            y,
            &part.name,
            ITEM_SIZE,
            look::under(drawn.color, light),
        );
        if let Some(bar) = bar(part, y, at) {
            // The track takes the line's color, not the green of the
            // volume row, so it reads as the empty part of this bar.
            frame.fill(
                &look::rounded(bar.track, BAR_RADIUS),
                look::under(bar.color, look::TRACK_OPACITY * light),
            );
            if bar.fill.width >= 1.0 {
                frame.fill(
                    &look::rounded(bar.fill, BAR_RADIUS),
                    look::under(bar.color, light),
                );
            }
        }
        if let Some(marker) = drawn.marker {
            hexagon(
                frame,
                marker.center,
                MARKER_R,
                look::under(marker.color, light),
            );
        }
    }

    text(
        frame,
        header_y(bottom, unit.parts.len()),
        &unit.name,
        HEADER_SIZE,
        look::under(look::text(), light),
    );
}

/// How one part's line draws at one moment.
#[derive(Debug, Clone, Copy, PartialEq)]
struct Line {
    color: Color,
    marker: Option<Marker>,
}

#[derive(Debug, Clone, Copy, PartialEq)]
struct Marker {
    center: Point,
    color: Color,
}

/// The marker draws for the focused part, and also for a part whose
/// focus beat is running. The client can receive the focus event before
/// the status that marks the part focused, and the beat shows either way.
fn line(part: &Part, y: f32, at: f64) -> Line {
    let level = brightness(part, at);
    let flash = flash(part, at);
    let focus = focus(part, at);

    Line {
        color: look::faded(flash_color(flash), name_opacity(level)),
        marker: (part.focused || focus > 0.0).then(|| Marker {
            center: Point::new(LEFT - MARKER_GAP, y - MARKER_RISE),
            // A controller that reconnects flashes its marker with its
            // name.
            color: look::faded(flash_color(flash.max(focus)), marker_opacity(level, focus)),
        }),
    }
}

/// The battery bar of one line. The track spans 0 to 100 percent and the
/// fill spans the charge.
#[derive(Debug, Clone, Copy, PartialEq)]
struct Bar {
    track: Rectangle,
    fill: Rectangle,
    color: Color,
}

/// The bar takes the color of the line, so it dims and flashes with the
/// name. A part that reports no battery level has no bar.
fn bar(part: &Part, y: f32, at: f64) -> Option<Bar> {
    let level = part.battery?;
    let track = Rectangle {
        x: BAR_RIGHT - BAR_LENGTH,
        y: y - MARKER_RISE - BAR_HEIGHT / 2.0,
        width: BAR_LENGTH,
        height: BAR_HEIGHT,
    };

    Some(Bar {
        track,
        fill: Rectangle {
            width: filled(level),
            ..track
        },
        color: line(part, y, at).color,
    })
}

/// The width of the fill, in canvas pixels. A level outside 0 to 100 is
/// clamped to the nearest end of the bar.
fn filled(level: i64) -> f32 {
    BAR_LENGTH * (level as f32 / FULL_CHARGE).clamp(0.0, 1.0)
}

/// Draws one line of text with its bottom-left corner at `(LEFT, y)`, so
/// a line's `y` is its bottom edge and the list stacks upward from the
/// bottom margin.
fn text(frame: &mut Frame, y: f32, content: &str, size: f32, color: Color) {
    frame.fill_text(look::line(
        content.to_owned(),
        Point::new(LEFT, y),
        look::Anchor::BottomLeft,
        size,
        color,
    ))
}

fn hexagon(frame: &mut Frame, center: Point, r: f32, color: Color) {
    let points = vertices(center, r);
    let path = Path::new(|builder| {
        builder.move_to(points[0]);
        for point in &points[1..] {
            builder.line_to(*point);
        }
        builder.close();
    });

    frame.fill(&path, color);
}

/// The y of one part's line. `above` counts lines up from the last part,
/// which is on the bottom margin.
fn item_y(bottom: f32, above: usize) -> f32 {
    bottom - above as f32 * ITEM_STEP
}

/// The y of the unit's name above `parts` lines of parts. A unit with no
/// parts has its name on the bottom margin.
fn header_y(bottom: f32, parts: usize) -> f32 {
    let first = item_y(bottom, parts.saturating_sub(1));
    if parts == 0 {
        first
    } else {
        first - HEADER_STEP
    }
}

/// The vertices of a regular pointy-top hexagon around `center`, where
/// `r` is the distance from the center to a vertex. 0.8660254 is
/// cos(30 degrees), the half-width in units of `r`.
///
/// The marker does not reuse `liken_iced::mark::hexagons`, the hexagons
/// of the `liken` mark. Those draw with a stroke that rounds their
/// corners, and the marker has sharp corners and takes its size from the
/// line it marks.
fn vertices(center: Point, r: f32) -> [Point; 6] {
    let half = 0.866_025_4 * r;
    [
        Point::new(center.x, center.y - r),
        Point::new(center.x + half, center.y - 0.5 * r),
        Point::new(center.x + half, center.y + 0.5 * r),
        Point::new(center.x, center.y + r),
        Point::new(center.x - half, center.y + 0.5 * r),
        Point::new(center.x - half, center.y - 0.5 * r),
    ]
}

/// The brightness of one part, from 0 (drawn at `look::DIM`) to 1 (full).
///
/// A part that does not report whether it is connected is always at 1. A
/// part that has been disconnected since the first status is at 0, with
/// no ease, because no disconnect time was recorded.
///
/// When a part connects or disconnects, it eases over `DIM_SECONDS` from
/// `Part::brightness_from`, its brightness at that moment. `Unit` records
/// that value by calling this function, so the ease is defined only here.
pub fn brightness(part: &Part, at: f64) -> f32 {
    match (part.connected, part.returned, part.left) {
        (None, _, _) => 1.0,
        (Some(true), None, _) => 1.0,
        (Some(true), Some(returned), _) => between(
            part.brightness_from,
            1.0,
            progress(at - returned, DIM_SECONDS),
        ),
        (Some(false), _, None) => 0.0,
        (Some(false), _, Some(left)) => {
            between(part.brightness_from, 0.0, progress(at - left, DIM_SECONDS))
        }
    }
}

/// The beat on the name of a controller that reconnected.
pub fn flash(part: &Part, at: f64) -> f32 {
    beat(part.returned, part.flash_from, at)
}

/// The beat on the marker of a controller that took focus.
pub fn focus(part: &Part, at: f64) -> f32 {
    beat(part.marked, part.focus_from, at)
}

/// A beat's level at `at`, 0 at rest and 1 at its peak, for a beat that
/// started at `moment`.
///
/// `from` is the level of the previous beat at `moment`. A beat that
/// starts during an earlier one climbs from that level instead of
/// dropping to 0 first.
fn beat(moment: Option<f64>, from: f32, at: f64) -> f32 {
    let Some(moment) = moment else {
        return 0.0;
    };

    let elapsed = at - moment;
    if elapsed < BEAT_RISE_SECONDS {
        between(from, 1.0, progress(elapsed, BEAT_RISE_SECONDS))
    } else {
        1.0 - progress(elapsed - BEAT_RISE_SECONDS, BEAT_FALL_SECONDS)
    }
}

/// How far a linear animation of `over` seconds has run after `elapsed`
/// seconds, from 0 to 1. A negative `elapsed`, for a start time that the
/// clock has not reached, gives 0.
fn progress(elapsed: f64, over: f64) -> f32 {
    (elapsed / over).clamp(0.0, 1.0) as f32
}

fn name_opacity(brightness: f32) -> f32 {
    between(look::DIM, 1.0, brightness)
}

/// At rest the marker dims with its line, from `MARKER_DIM` to
/// `MARKER_REST`. A focus beat raises it to opaque.
fn marker_opacity(brightness: f32, beat: f32) -> f32 {
    let rest = between(MARKER_DIM, MARKER_REST, brightness);

    between(rest, 1.0, beat)
}

/// Linear interpolation from `at_zero` (at `t` = 0) to `at_one` (at `t` = 1).
fn between(at_zero: f32, at_one: f32, t: f32) -> f32 {
    at_zero + (at_one - at_zero) * t
}

const BEAT_SECONDS: f64 = BEAT_RISE_SECONDS + BEAT_FALL_SECONDS;

/// The time the block next needs a frame, for [`super::Idle::next_frame`].
///
/// An animation changes a color on every frame, so while any part is
/// animating this returns `at` and the harness draws again at once. When
/// every part has settled it returns `None`.
pub fn next_frame(unit: &Unit, at: f64) -> Option<f64> {
    unit.parts
        .iter()
        .any(|part| is_moving(part, at))
        .then_some(at)
}

/// Whether the part's ease, its name's flash, or its marker's focus beat
/// is running at `at`. The ease starts at `returned` for a connected part
/// and at `left` for a disconnected one, as in [`brightness`].
fn is_moving(part: &Part, at: f64) -> bool {
    let dimming = match part.connected {
        Some(true) => is_running(part.returned, DIM_SECONDS, at),
        Some(false) => is_running(part.left, DIM_SECONDS, at),
        None => false,
    };

    dimming
        || is_running(part.returned, BEAT_SECONDS, at)
        || is_running(part.marked, BEAT_SECONDS, at)
}

fn is_running(moment: Option<f64>, over: f64, at: f64) -> bool {
    moment.is_some_and(|moment| at < moment + over)
}

/// The color of a part's name during a flash: `look::text()` at 0, and
/// white at 1.
fn flash_color(beat: f32) -> Color {
    let text = look::text();
    let toward_white = |channel: f32| between(channel, 1.0, beat);

    Color {
        r: toward_white(text.r),
        g: toward_white(text.g),
        b: toward_white(text.b),
        a: text.a,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn part(connected: Option<bool>) -> Part {
        Part {
            name: "A remote".into(),
            kind: "remote".into(),
            connected,
            ..Part::default()
        }
    }

    #[track_caller]
    fn assert_close(measured: f32, expected: f32, tolerance: f32) {
        assert!(
            (measured - expected).abs() < tolerance,
            "the value is {measured}, want {expected} within {tolerance}"
        );
    }

    fn charged(level: i64) -> Part {
        Part {
            battery: Some(level),
            ..part(Some(true))
        }
    }

    #[test]
    fn a_charge_fills_that_fraction_of_the_bar() {
        assert_eq!(
            bar(&charged(50), 990.0, 10.0).expect("no bar").fill.width,
            BAR_LENGTH / 2.0
        );
        assert_eq!(
            bar(&charged(0), 990.0, 10.0).expect("no bar").fill.width,
            0.0
        );
        assert_eq!(
            bar(&charged(100), 990.0, 10.0).expect("no bar").fill.width,
            BAR_LENGTH
        );
    }

    #[test]
    fn a_charge_outside_the_range_fills_the_bar_no_further_than_its_ends() {
        assert_eq!(
            bar(&charged(140), 990.0, 10.0).expect("no bar").fill.width,
            BAR_LENGTH
        );
        assert_eq!(
            bar(&charged(-5), 990.0, 10.0).expect("no bar").fill.width,
            0.0
        );
    }

    #[test]
    fn a_part_that_reports_no_charge_draws_no_bar() {
        assert_eq!(bar(&part(Some(true)), 990.0, 10.0), None);
    }

    #[test]
    fn the_track_spans_the_whole_bar_and_the_fill_starts_at_its_left_end() {
        let drawn = bar(&charged(50), 990.0, 10.0).expect("no bar");

        assert_eq!(drawn.track.width, BAR_LENGTH);
        assert_eq!(drawn.fill.x, drawn.track.x);
        assert_eq!(drawn.fill.y, drawn.track.y);
        assert_eq!(drawn.fill.height, drawn.track.height);
    }

    #[test]
    fn every_bar_ends_on_one_column_left_of_the_marker_and_inside_the_screen() {
        let drawn = bar(&charged(50), 990.0, 10.0).expect("no bar");
        let right = drawn.track.x + drawn.track.width;
        // `vertices` puts the marker's left vertices this far left of its
        // center, and the center is `MARKER_GAP` left of `LEFT`.
        let marker_left = LEFT - MARKER_GAP - 0.866_025_4 * MARKER_R;

        assert_eq!(right, LEFT - MARKER_GAP - MARKER_R - BAR_GAP);
        assert!(
            right < marker_left,
            "the bar ends at {right}, which touches the marker at {marker_left}"
        );
        assert!(
            drawn.track.x > 0.0,
            "the bar starts at {}, off the screen",
            drawn.track.x
        );
        assert_eq!(
            bar(&charged(100), 924.0, 10.0).expect("no bar").track.x,
            drawn.track.x
        );
    }

    #[test]
    fn the_bar_is_centered_at_the_height_of_the_marker() {
        let drawn = bar(&charged(50), 990.0, 10.0).expect("no bar");

        assert_close(
            drawn.track.y + drawn.track.height / 2.0,
            990.0 - MARKER_RISE,
            0.001,
        );
    }

    #[test]
    fn the_bar_draws_in_the_color_of_its_line() {
        let away = Part {
            battery: Some(50),
            ..part(Some(false))
        };

        assert_eq!(
            bar(&charged(50), 990.0, 10.0).expect("no bar").color,
            line(&charged(50), 990.0, 10.0).color
        );
        assert!(
            bar(&away, 990.0, 10.0).expect("no bar").color.a
                < bar(&charged(50), 990.0, 10.0).expect("no bar").color.a
        );
    }

    #[test]
    fn a_returning_parts_bar_flashes_white_with_its_name() {
        let returning = Part {
            returned: Some(10.0),
            ..charged(50)
        };

        assert_eq!(bar(&returning, 990.0, 10.12).expect("no bar").color.r, 1.0);
    }

    #[test]
    fn the_markers_two_resting_opacities_are_the_ass_alphas() {
        // The ASS alpha bytes 0xA8 and 0xE1 leave 87 and 30 of 255 steps
        // of light. Half a step is below what a panel can show.
        assert_close(MARKER_REST, 87.0 / 255.0, 0.5 / 255.0);
        assert_close(MARKER_DIM, 30.0 / 255.0, 0.5 / 255.0);
    }

    #[test]
    fn the_block_measures_in_line_boxes_and_draws_at_the_size_that_box_states() {
        // The line box is 30 pixels, which is a 22.6 pixel font size.
        // The marker radius is a fifth of the box, and its center is 0.42
        // of the box above the line's anchor.
        assert_close(ITEM_BOX, 30.0, 0.1);
        assert_close(ITEM_SIZE, 22.6, 0.1);
        assert_close(MARKER_R, 6.0, 0.1);
        assert_close(MARKER_RISE, 12.6, 0.1);
        // The bar is two characters (2 x 22.6 pixels) long and an eighth
        // of the line box tall.
        assert_close(BAR_LENGTH, 45.2, 0.2);
        assert_close(BAR_HEIGHT, 3.75, 0.01);
    }

    #[test]
    fn a_part_with_no_live_state_draws_at_full_brightness_always() {
        let part = part(None);
        assert_eq!(brightness(&part, 0.0), 1.0);
        assert_eq!(brightness(&part, 1_000.0), 1.0);
        assert_eq!(name_opacity(brightness(&part, 1_000.0)), 1.0);
    }

    #[test]
    fn a_disconnected_part_rests_at_the_dim_alpha() {
        let part = part(Some(false));
        assert_eq!(brightness(&part, 12.0), 0.0);
        assert_eq!(name_opacity(0.0), look::DIM);
    }

    #[test]
    fn a_part_that_never_left_draws_full_with_no_ease() {
        assert_eq!(brightness(&part(Some(true)), 12.0), 1.0);
    }

    #[test]
    fn a_part_that_disconnected_eases_down_over_400_ms() {
        let part = Part {
            left: Some(10.0),
            brightness_from: 1.0,
            ..part(Some(false))
        };

        assert_eq!(brightness(&part, 10.0), 1.0);
        assert_eq!(brightness(&part, 10.2), 0.5);
        assert_eq!(brightness(&part, 10.4), 0.0);
        assert_eq!(brightness(&part, 30.0), 0.0);
    }

    #[test]
    fn a_part_disconnected_since_the_first_status_rests_at_dim() {
        assert_eq!(brightness(&part(Some(false)), 12.0), 0.0);
    }

    #[test]
    fn a_part_that_returned_eases_to_full_over_400_ms() {
        let part = Part {
            returned: Some(10.0),
            ..part(Some(true))
        };

        assert_eq!(brightness(&part, 10.0), 0.0);
        assert_eq!(brightness(&part, 10.2), 0.5);
        assert_eq!(brightness(&part, 10.4), 1.0);
        assert_eq!(brightness(&part, 30.0), 1.0);
    }

    #[test]
    fn a_part_that_returned_mid_fade_eases_up_from_the_level_it_was_at() {
        // The part left at 10.0 and returned at 10.2, halfway through the
        // 400 ms fade down, so the ease up starts from half brightness.
        let part = Part {
            returned: Some(10.2),
            left: Some(10.0),
            brightness_from: 0.5,
            ..part(Some(true))
        };

        assert_eq!(brightness(&part, 10.2), 0.5);
        assert_eq!(brightness(&part, 10.4), 0.75);
        assert_eq!(brightness(&part, 10.6), 1.0);
    }

    #[test]
    fn a_part_that_disconnected_mid_ease_fades_down_from_the_level_it_was_at() {
        let part = Part {
            returned: Some(10.0),
            left: Some(10.2),
            brightness_from: 0.5,
            ..part(Some(false))
        };

        assert_eq!(brightness(&part, 10.2), 0.5);
        assert_eq!(brightness(&part, 10.4), 0.25);
        assert_eq!(brightness(&part, 10.6), 0.0);
    }

    #[test]
    fn a_beat_peaks_at_120_ms_and_falls_over_500_ms() {
        let moment = Some(10.0);

        assert_eq!(beat(moment, 0.0, 10.0), 0.0);
        assert_eq!(beat(moment, 0.0, 10.06), 0.5);
        assert_eq!(beat(moment, 0.0, 10.12), 1.0);
        assert_eq!(beat(moment, 0.0, 10.37), 0.5);
        assert_eq!(beat(moment, 0.0, 10.62), 0.0);
        assert_eq!(beat(moment, 0.0, 30.0), 0.0);
    }

    #[test]
    fn a_beat_that_has_not_started_is_at_rest() {
        assert_eq!(beat(None, 0.0, 10.0), 0.0);
        assert_eq!(beat(Some(12.0), 0.0, 10.0), 0.0);
    }

    #[test]
    fn a_second_focus_beat_inside_the_first_climbs_from_the_level_it_was_at() {
        // The first beat was at 0.5 on its way down when the second
        // focus moment arrived at 10.37, so the marker climbs from 0.5 to
        // 1 over the 120 ms rise and does not drop to 0 first.
        let part = Part {
            marked: Some(10.37),
            focus_from: 0.5,
            ..part(Some(true))
        };

        assert_eq!(focus(&part, 10.37), 0.5);
        assert_eq!(focus(&part, 10.43), 0.75);
        assert_eq!(focus(&part, 10.49), 1.0);
    }

    #[test]
    fn a_second_flash_inside_the_first_climbs_from_the_level_it_was_at() {
        let part = Part {
            returned: Some(10.37),
            flash_from: 0.5,
            ..part(Some(true))
        };

        assert_eq!(flash(&part, 10.37), 0.5);
        assert_eq!(flash(&part, 10.49), 1.0);
    }

    #[test]
    fn a_flash_reaches_white_and_settles_on_the_text_color() {
        assert_eq!(flash_color(0.0), look::text());
        assert_eq!(flash_color(1.0), Color::from_rgb(1.0, 1.0, 1.0));

        let half = flash_color(0.5);
        assert!(half.r > look::text().r && half.r < 1.0);
    }

    #[test]
    fn the_marker_rests_one_step_under_the_line_it_marks() {
        assert_eq!(marker_opacity(1.0, 0.0), look::DIM);
        assert_eq!(marker_opacity(0.0, 0.0), MARKER_DIM);
        assert!(marker_opacity(0.0, 0.0) < marker_opacity(1.0, 0.0));
    }

    #[test]
    fn a_focus_beat_lifts_the_marker_to_opaque_and_back() {
        let moment = Some(10.0);

        assert_eq!(marker_opacity(1.0, beat(moment, 0.0, 10.12)), 1.0);
        assert_eq!(marker_opacity(0.0, beat(moment, 0.0, 10.12)), 1.0);
        assert!(marker_opacity(1.0, beat(moment, 0.0, 10.37)) > look::DIM);
        assert_eq!(marker_opacity(1.0, beat(moment, 0.0, 10.62)), look::DIM);
    }

    #[test]
    fn a_part_the_status_marks_focused_draws_the_marker() {
        let part = Part {
            focused: true,
            ..part(Some(true))
        };
        let marker = line(&part, 990.0, 10.0).marker.expect("no marker");

        assert_eq!(marker.center, Point::new(78.0, 990.0 - MARKER_RISE));
        assert_eq!(marker.color.a, look::DIM);
    }

    #[test]
    fn a_part_mid_beat_draws_the_marker_before_its_status_arrives() {
        let part = Part {
            marked: Some(10.0),
            ..part(Some(true))
        };

        assert!(line(&part, 990.0, 10.12).marker.is_some());
        assert!(line(&part, 990.0, 30.0).marker.is_none());
    }

    #[test]
    fn a_part_with_no_focus_and_no_beat_draws_no_marker() {
        assert_eq!(line(&part(Some(true)), 990.0, 10.0).marker, None);
    }

    #[test]
    fn a_returning_part_flashes_its_name_and_its_marker_white() {
        let part = Part {
            focused: true,
            returned: Some(10.0),
            ..part(Some(true))
        };
        let drawn = line(&part, 990.0, 10.12);

        assert_eq!(drawn.color.r, 1.0);
        assert_eq!(drawn.marker.expect("no marker").color.r, 1.0);
    }

    #[test]
    fn the_parts_stack_upward_from_the_bottom_margin() {
        // One step is 1.1 of the 30 pixel line box, which is 33.
        assert_close(item_y(990.0, 0), 990.0, 0.2);
        assert_close(item_y(990.0, 1), 957.0, 0.2);
        assert_close(item_y(990.0, 2), 924.0, 0.2);
    }

    #[test]
    fn the_header_is_a_wider_step_above_the_first_part() {
        // The header step is 1.3 of the line box, which is 39 pixels and
        // 6 more than a step between two parts.
        assert_close(header_y(990.0, 3), 924.0 - 39.0, 0.2);
        assert_close(header_y(990.0, 1), 990.0 - 39.0, 0.2);
        assert_close(header_y(990.0, 0), 990.0, 0.2);
    }

    fn unit(part: Part) -> Unit {
        Unit {
            name: "Unit A".into(),
            parts: vec![part],
            ..Unit::default()
        }
    }

    #[test]
    fn next_frame_is_now_for_400_ms_after_a_part_disconnects() {
        let unit = unit(Part {
            left: Some(10.0),
            ..part(Some(false))
        });

        assert_eq!(next_frame(&unit, 10.39), Some(10.39));
        assert_eq!(next_frame(&unit, 10.41), None);
    }

    #[test]
    fn next_frame_is_now_for_the_whole_620_ms_after_a_part_returns() {
        // The name eases up over 400 ms and the flash takes 120 ms up and
        // 500 ms down, so the flash outlasts the ease and sets the time.
        let unit = unit(Part {
            returned: Some(10.0),
            ..part(Some(true))
        });

        assert_eq!(next_frame(&unit, 10.61), Some(10.61));
        assert_eq!(next_frame(&unit, 10.63), None);
    }

    #[test]
    fn next_frame_is_now_while_a_focus_beat_runs() {
        let unit = unit(Part {
            focused: true,
            marked: Some(10.0),
            ..part(Some(true))
        });

        assert_eq!(next_frame(&unit, 10.61), Some(10.61));
        assert_eq!(next_frame(&unit, 10.63), None);
    }

    #[test]
    fn next_frame_is_none_when_all_parts_have_settled() {
        assert_eq!(next_frame(&unit(part(None)), 600.0), None);
        assert_eq!(next_frame(&unit(part(Some(true))), 600.0), None);
        assert_eq!(next_frame(&unit(part(Some(false))), 600.0), None);
        assert_eq!(next_frame(&Unit::default(), 600.0), None);
    }

    #[test]
    fn the_marker_is_a_regular_pointy_top_hexagon() {
        let center = Point::new(122.0, 900.0);
        let points = vertices(center, MARKER_R);

        for point in points {
            let reach = ((point.x - center.x).powi(2) + (point.y - center.y).powi(2)).sqrt();
            assert!(
                (reach - MARKER_R).abs() < 0.001,
                "a vertex is {reach} from the center, want {MARKER_R}"
            );
        }

        let width = points[1].x - points[5].x;
        let height = points[3].y - points[0].y;
        assert!(
            (height - 2.0 * MARKER_R).abs() < 0.001,
            "the marker is {height} high, want {}",
            2.0 * MARKER_R
        );
        assert!((width / height - 0.866).abs() < 0.001);
    }
}
