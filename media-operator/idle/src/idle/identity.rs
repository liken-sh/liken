//! The identity block, the bottom-left element of the idle screen. It names
//! the unit and lists its parts, so a person reads what the unit is and what
//! it plays through while no film runs.
//!
//! Each part draws at its own brightness. A part with live state, a controller
//! that connects and disconnects, draws dim while it is away and eases back to
//! full when it returns. A part with no live state, a wired screen or its
//! built-in speakers, draws at full brightness always, because a part that
//! cannot be absent must not read as present-for-now.
//!
//! A part that reports a battery level draws it as a small bar in the
//! left margin, on the part's own line. The bar carries no number and no
//! charging state. Every bar ends on one column, left of the focus
//! marker's place, so the names keep their flush-left column and a person
//! reads the charges of two controllers against each other.
//!
//! The block also shows focus. The status marks at most one part focused, the
//! controller whose presses drive this unit, and that line takes a small
//! hexagon in the left margin. The seed carries no focus, so no marker draws
//! before the first status.
//!
//! Every brightness here is a function of the clock and of the seconds in
//! `Part`, so a captured frame is reproducible and a dropped frame is
//! harmless.

use iced_widget::canvas::Path;
use iced_winit::core::{Color, Point, Rectangle};

use super::{Frame, Layout};
use crate::look;
use crate::unit::{Part, Unit};

/// The left edge every line of the block is flush with.
const LEFT: f32 = look::MARGIN_X;

/// The header reads one step larger than the parts, so the unit's name leads
/// the list.
const HEADER_SIZE: f32 = look::LABEL;

/// The line box a part's name draws in, in canvas pixels. `identity.lua` states
/// it as `theme.type.small - 4`, four ASS units under the shared small size, so
/// the parts read a touch lighter without changing that size for every other
/// element.
///
/// That one number does two jobs in the Lua, and this block keeps them apart.
/// `ITEM_BOX` is the layout measure, and every step and offset below is a
/// fraction of it. `ITEM_SIZE` is the type size the same number states.
const ITEM_BOX: f32 = look::line_box(look::SMALL) - 4.0;

/// The size a part's name draws at, which is `ITEM_BOX` through the face
/// metric.
const ITEM_SIZE: f32 = look::type_size(ITEM_BOX);

/// The drop from one part's line to the next: 1.1 line boxes, the leading
/// `identity.lua` gives its list.
const ITEM_STEP: f32 = 1.1 * ITEM_BOX;

/// The drop from the header to the first part, 1.3 line boxes. The gap is wider
/// than `ITEM_STEP`, so the name reads as the title of the list under it.
const HEADER_STEP: f32 = 1.3 * ITEM_BOX;

/// The focus marker's radius, from its center to a vertex. It is a fifth of a
/// line box, so the marker scales with the line and stands 12 canvas pixels
/// tall beside a part's name.
const MARKER_R: f32 = ITEM_BOX / 5.0;

/// The distance from the marker's center to `LEFT`. The marker draws inside
/// the margin, so the names keep one flush-left column with or without a
/// marker.
const MARKER_GAP: f32 = 18.0;

/// The lift from the line's anchor, the bottom of the line box, to the middle
/// of the lowercase letters, where the marker's center draws. It is 0.42 of a
/// line box.
const MARKER_RISE: f32 = 0.42 * ITEM_BOX;

/// The gap between the bar's right end and the marker's place. The bar
/// draws left of every marker, drawn or not, so the two never touch and
/// the bars of two parts end on one column whichever of them holds the
/// focus.
const BAR_GAP: f32 = 12.0;

/// The column every bar ends on, which is the marker's place less the
/// gap.
const BAR_RIGHT: f32 = LEFT - MARKER_GAP - MARKER_R - BAR_GAP;

/// The bar is two characters long and an eighth of a line box tall. It
/// reads as a measure beside the name and not as a second line of the
/// list.
const BAR_LENGTH: f32 = 2.0 * ITEM_SIZE;
const BAR_HEIGHT: f32 = ITEM_BOX / 8.0;

/// Half the height rounds the ends to semicircles, which is the widest
/// radius `look::rounded` holds for a shape this thin.
const BAR_RADIUS: f32 = BAR_HEIGHT / 2.0;

/// The charge the bar fills at, the top of the range a `Peripheral`
/// reports.
const FULL_CHARGE: f32 = 100.0;

/// The time one part takes to move between dim and full.
const DIM_SECONDS: f64 = 0.4;

/// A beat rises in this time and falls in `BEAT_FALL_SECONDS`. The rise is the
/// shorter of the two, so a controller that returns reads as one bright beat
/// and not as a blink.
const BEAT_RISE_SECONDS: f64 = 0.12;
const BEAT_FALL_SECONDS: f64 = 0.5;

/// The marker's resting opacity on a lit line, one step under its own name.
const MARKER_REST: f32 = look::DIM;

/// The marker's resting opacity on a dim line, which is `look::DIM` of
/// `look::DIM`. The marker keeps the same fraction of a dim line that it keeps
/// of a lit one, so it never reads brighter than the part it marks.
/// `identity.lua` states the same value as the ASS alpha byte 0xE1.
const MARKER_DIM: f32 = look::DIM * look::DIM;

/// Draw the element into the frame, in canvas units.
///
/// The block draws nothing while nothing names the unit, so a client that has
/// no seed and no status shows the clock alone. The last part stands on the
/// bottom margin, and the block builds upward from there.
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
            // The track reads as the room the charge has left, so it draws in the
            // line's own colour at the track opacity rather than in the volume
            // row's darker green.
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

/// One part's line at one moment: the color its name draws in, and its marker
/// when the marker draws.
#[derive(Debug, Clone, Copy, PartialEq)]
struct Line {
    color: Color,
    marker: Option<Marker>,
}

/// The focus marker of one line.
#[derive(Debug, Clone, Copy, PartialEq)]
struct Marker {
    center: Point,
    color: Color,
}

/// One part's line, from the part and the clock alone.
///
/// The marker draws for the focused part, and for a part mid-beat whose status
/// has not landed yet. A focus moment reaches the client before the status that
/// marks the part focused, and the beat shows either way.
fn line(part: &Part, y: f32, at: f64) -> Line {
    let level = brightness(part, at);
    let flash = flash(part, at);
    let focus = focus(part, at);

    Line {
        color: look::faded(lit(flash), name_opacity(level)),
        marker: (part.focused || focus > 0.0).then(|| Marker {
            center: Point::new(LEFT - MARKER_GAP, y - MARKER_RISE),
            // The marker draws in the color its name draws in, so a controller
            // that returns flashes both.
            color: look::faded(lit(flash.max(focus)), marker_opacity(level, focus)),
        }),
    }
}

/// The charge bar of one line: the track that carries the whole length,
/// the fill the charge covers, and the colour both draw in. Every part
/// that reports a charge draws one, and a part that reports none draws
/// nothing.
#[derive(Debug, Clone, Copy, PartialEq)]
struct Bar {
    track: Rectangle,
    fill: Rectangle,
    color: Color,
}

/// One part's charge bar, from the part and the clock alone. It takes the
/// colour of the line, so the bar of a controller that is away dims with
/// its name and the bar of one that returns flashes with it.
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

/// How much of the bar one charge fills, in canvas pixels. A reading
/// outside 0 to 100 fills to one end and no further.
fn filled(level: i64) -> f32 {
    BAR_LENGTH * (level as f32 / FULL_CHARGE).clamp(0.0, 1.0)
}

/// One run of text, anchored at its bottom left.
///
/// ASS `\an1` anchors a run at the bottom left, so a line's position is its own
/// bottom and the stack builds from the bottom up. The line box is one type
/// size tall, so the leadings above are the gaps between the anchors and
/// nothing more.
fn text(frame: &mut Frame, y: f32, content: &str, size: f32, color: Color) {
    frame.fill_text(look::line(
        content.to_owned(),
        Point::new(LEFT, y),
        look::Anchor::BottomLeft,
        size,
        color,
    ))
}

/// One filled hexagon about `center`, with the vertices `vertices` gives.
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

/// The y one part's line hangs from. `above` counts up the list from the last
/// part, which is the part on the bottom margin.
fn item_y(bottom: f32, above: usize) -> f32 {
    bottom - above as f32 * ITEM_STEP
}

/// The y the unit's name hangs from, over `parts` lines of parts. A unit with
/// no parts is a name on the bottom margin.
fn header_y(bottom: f32, parts: usize) -> f32 {
    let first = item_y(bottom, parts.saturating_sub(1));
    if parts == 0 {
        first
    } else {
        first - HEADER_STEP
    }
}

/// The six vertices of a pointy-top regular hexagon about `center`. `r` is the
/// distance from the center to a vertex, and 0.8660254 is cos(30 degrees), the
/// half-width of a pointy-top hexagon.
///
/// The marker is this formula, and not one of the fourteen hexagons that
/// `liken_iced::mark::hexagons` reads out of `liken.svg`. Those fourteen are
/// the same regular pointy-top shape, and each one draws with a stroke in its
/// own fill that rounds its corners. The marker's corners are sharp, and its
/// size comes from the line it marks, so the formula draws what the display
/// draws.
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

/// The brightness one part draws at, from 0 at `look::DIM` to 1 at full.
///
/// A part that reports no presence draws at full brightness always. A part that
/// has been away since the first status rests at dim, because the client saw no
/// moment to ease from.
///
/// A part that reports presence eases over `DIM_SECONDS` in both
/// directions, from the level `Part::brightness_from` recorded at the
/// moment it turned toward the end its presence names. `Unit` reads the
/// level through this function, so the curve is stated once.
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

/// The white flash on the name of a controller that returned: the beat
/// `returned` and `flash_from` state.
pub fn flash(part: &Part, at: f64) -> f32 {
    beat(part.returned, part.flash_from, at)
}

/// The beat on the marker of a controller that took focus: the beat
/// `marked` and `focus_from` state.
pub fn focus(part: &Part, at: f64) -> f32 {
    beat(part.marked, part.focus_from, at)
}

/// One white beat, 0 at rest and 1 at its peak. It rises from `moment` over
/// `BEAT_RISE_SECONDS` and falls over `BEAT_FALL_SECONDS`. Two beats run on one
/// part, the flash on the name of a controller that returned and the beat on
/// the marker of a controller that took focus, and both read their moment from
/// `Part`.
///
/// `from` is the level the beat stood at when the moment landed, so a
/// beat that lands inside another climbs from there instead of dropping
/// to rest first.
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

/// How far a move of `over` seconds has run at `elapsed` seconds, from 0 to 1.
/// The move is linear, which is the step the display takes on its timer. A
/// moment the clock has not reached yet gives 0.
fn progress(elapsed: f64, over: f64) -> f32 {
    (elapsed / over).clamp(0.0, 1.0) as f32
}

/// The opacity one part's name draws at: opaque at brightness 1, and
/// `look::DIM` at 0.
fn name_opacity(brightness: f32) -> f32 {
    between(look::DIM, 1.0, brightness)
}

/// The opacity one part's marker draws at. At rest it runs with the line's own
/// brightness, from `MARKER_DIM` on a disconnected line to `MARKER_REST` on a
/// lit one, so the marker reports the presence the name reports. The beat then
/// lifts it to opaque, and that lift is what a person sees when focus arrives.
fn marker_opacity(brightness: f32, beat: f32) -> f32 {
    let rest = between(MARKER_DIM, MARKER_REST, brightness);

    between(rest, 1.0, beat)
}

/// One value between `at_zero` and `at_one`, in step with `t` from 0 to 1.
fn between(at_zero: f32, at_one: f32, t: f32) -> f32 {
    at_zero + (at_one - at_zero) * t
}

/// The whole of one beat, the rise and the fall together.
const BEAT_SECONDS: f64 = BEAT_RISE_SECONDS + BEAT_FALL_SECONDS;

/// The second the block next changes, for [`super::Idle::next_frame`].
///
/// Every move here changes a colour on every frame it covers, so the block
/// answers with `at` and the harness draws now. A block whose parts have all
/// settled states nothing, which is every second of an idle screen except the
/// few after a controller arrives, leaves, or takes focus.
pub fn next_frame(unit: &Unit, at: f64) -> Option<f64> {
    unit.parts.iter().any(|part| moving(part, at)).then_some(at)
}

/// Whether any of one part's three moves is still running at `at`: the change
/// between dim and full, the flash on a name that returned, and the beat on a
/// marker that took focus.
///
/// The presence decides which moment the brightness eases from, and this match
/// reads the same one [`brightness`] reads. A part that reports no presence
/// holds one brightness, and so does a part that has no moment to ease from.
fn moving(part: &Part, at: f64) -> bool {
    let dimming = match part.connected {
        Some(true) => running(part.returned, DIM_SECONDS, at),
        Some(false) => running(part.left, DIM_SECONDS, at),
        None => false,
    };

    dimming || running(part.returned, BEAT_SECONDS, at) || running(part.marked, BEAT_SECONDS, at)
}

/// Whether a move of `over` seconds from `moment` is still running at `at`. A
/// part that read no such moment never moves on it.
fn running(moment: Option<f64>, over: f64, at: f64) -> bool {
    moment.is_some_and(|moment| at < moment + over)
}

/// The color one part draws in. At beat 0 it is `look::text()`, and at 1 every
/// channel reaches white, the bright beat of a controller that returned.
fn lit(beat: f32) -> Color {
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

    /// Two readings of one measure, where `expected` is the number
    /// `identity.lua` rounds to and the port lands `tolerance` away from it.
    #[track_caller]
    fn assert_close(measured: f32, expected: f32, tolerance: f32) {
        assert!(
            (measured - expected).abs() < tolerance,
            "{measured} is not {expected}"
        );
    }

    /// A part that reports `level`, for the bar the charge draws.
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
    fn the_track_carries_the_whole_bar_and_the_fill_starts_where_it_starts() {
        let drawn = bar(&charged(50), 990.0, 10.0).expect("no bar");

        assert_eq!(drawn.track.width, BAR_LENGTH);
        assert_eq!(drawn.fill.x, drawn.track.x);
        assert_eq!(drawn.fill.y, drawn.track.y);
        assert_eq!(drawn.fill.height, drawn.track.height);
    }

    #[test]
    fn every_bar_ends_on_one_column_clear_of_the_marker_and_inside_the_margin() {
        let drawn = bar(&charged(50), 990.0, 10.0).expect("no bar");
        let right = drawn.track.x + drawn.track.width;
        // `vertices` puts the marker's left vertices this far left of its
        // centre, and the centre stands `MARKER_GAP` inside `LEFT`.
        let marker_left = LEFT - MARKER_GAP - 0.866_025_4 * MARKER_R;

        assert_eq!(right, LEFT - MARKER_GAP - MARKER_R - BAR_GAP);
        assert!(right < marker_left, "{right} touches the marker");
        assert!(drawn.track.x > 0.0, "{} is off the screen", drawn.track.x);
        assert_eq!(
            bar(&charged(100), 924.0, 10.0).expect("no bar").track.x,
            drawn.track.x
        );
    }

    #[test]
    fn the_bar_centres_on_the_rise_the_marker_takes() {
        let drawn = bar(&charged(50), 990.0, 10.0).expect("no bar");

        assert_close(
            drawn.track.y + drawn.track.height / 2.0,
            990.0 - MARKER_RISE,
            0.001,
        );
    }

    #[test]
    fn the_bar_draws_in_the_colour_of_the_line_it_stands_on() {
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
    fn the_markers_two_resting_opacities_are_the_lua_alphas() {
        // `identity.lua` states them as the ASS alpha bytes 0xA8 and 0xE1,
        // which leave 87 and 30 of the 255 steps of light. Half a step is
        // under what a panel draws.
        assert_close(MARKER_REST, 87.0 / 255.0, 0.5 / 255.0);
        assert_close(MARKER_DIM, 30.0 / 255.0, 0.5 / 255.0);
    }

    #[test]
    fn the_block_measures_in_line_boxes_and_draws_at_the_size_that_box_states() {
        // `identity.lua` lists its parts in a 30 pixel line box, which is 22
        // canvas pixels of type, and it places the marker a fifth of that box
        // wide and 0.42 of it over the line.
        assert_close(ITEM_BOX, 30.0, 0.1);
        assert_close(ITEM_SIZE, 22.0, 0.1);
        assert_close(MARKER_R, 6.0, 0.1);
        assert_close(MARKER_RISE, 12.6, 0.1);
        // The charge bar is two of those 22 pixel characters long and an
        // eighth of the line box tall.
        assert_close(BAR_LENGTH, 44.0, 0.2);
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
    fn a_part_that_went_away_eases_down_over_400_ms() {
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
    fn a_part_that_has_been_away_since_the_first_status_rests_at_dim() {
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
    fn a_part_that_came_back_mid_fade_eases_up_from_the_level_it_stood_at() {
        // The part left at 10.0 and came back at 10.2, halfway down the
        // 400 ms fade, so the ease up leaves from half brightness.
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
    fn a_part_that_went_away_mid_ease_fades_down_from_the_level_it_stood_at() {
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
    fn a_beat_that_has_not_landed_is_at_rest() {
        assert_eq!(beat(None, 0.0, 10.0), 0.0);
        assert_eq!(beat(Some(12.0), 0.0, 10.0), 0.0);
    }

    #[test]
    fn a_second_focus_beat_inside_the_first_climbs_from_the_level_it_held() {
        // The first beat stood at half on its way down when the second
        // focus moment landed at 10.37, so the marker climbs from 0.5 to
        // 1 over the 120 ms rise rather than dropping to rest first.
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
    fn a_second_flash_inside_the_first_climbs_from_the_level_it_held() {
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
        assert_eq!(lit(0.0), look::text());
        assert_eq!(lit(1.0), Color::from_rgb(1.0, 1.0, 1.0));

        let half = lit(0.5);
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
    fn a_part_the_status_marks_focused_takes_the_marker() {
        let part = Part {
            focused: true,
            ..part(Some(true))
        };
        let marker = line(&part, 990.0, 10.0).marker.expect("no marker");

        assert_eq!(marker.center, Point::new(122.0, 990.0 - MARKER_RISE));
        assert_eq!(marker.color.a, look::DIM);
    }

    #[test]
    fn a_part_mid_beat_takes_the_marker_before_its_status_lands() {
        let part = Part {
            marked: Some(10.0),
            ..part(Some(true))
        };

        assert!(line(&part, 990.0, 10.12).marker.is_some());
        assert!(line(&part, 990.0, 30.0).marker.is_none());
    }

    #[test]
    fn a_part_with_no_focus_and_no_beat_takes_no_marker() {
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
        // One step is 1.1 of the 30 canvas pixels `identity.lua` gives a
        // part's line box, and that file rounds each step to 33.
        assert_close(item_y(990.0, 0), 990.0, 0.2);
        assert_close(item_y(990.0, 1), 957.0, 0.2);
        assert_close(item_y(990.0, 2), 924.0, 0.2);
    }

    #[test]
    fn the_header_reads_a_wider_gap_over_the_first_part() {
        // The header's step is 1.3 of the same line box, which `identity.lua`
        // rounds to 39, six pixels wider than a step between two parts.
        assert_close(header_y(990.0, 3), 924.0 - 39.0, 0.2);
        assert_close(header_y(990.0, 1), 990.0 - 39.0, 0.2);
        assert_close(header_y(990.0, 0), 990.0, 0.2);
    }

    /// A unit whose one part is `part`.
    fn unit(part: Part) -> Unit {
        Unit {
            name: "The Den".into(),
            parts: vec![part],
            ..Unit::default()
        }
    }

    #[test]
    fn a_part_that_went_away_asks_for_a_frame_for_400_ms() {
        let unit = unit(Part {
            left: Some(10.0),
            ..part(Some(false))
        });

        assert_eq!(next_frame(&unit, 10.39), Some(10.39));
        assert_eq!(next_frame(&unit, 10.41), None);
    }

    #[test]
    fn a_part_that_returned_asks_for_a_frame_for_the_whole_620_ms_beat() {
        // The name eases up over 400 ms and the flash runs 120 ms up and 500
        // down, so the beat outlasts the ease and sets the schedule.
        let unit = unit(Part {
            returned: Some(10.0),
            ..part(Some(true))
        });

        assert_eq!(next_frame(&unit, 10.61), Some(10.61));
        assert_eq!(next_frame(&unit, 10.63), None);
    }

    #[test]
    fn a_marker_that_took_focus_asks_for_a_frame_while_it_beats() {
        let unit = unit(Part {
            focused: true,
            marked: Some(10.0),
            ..part(Some(true))
        });

        assert_eq!(next_frame(&unit, 10.61), Some(10.61));
        assert_eq!(next_frame(&unit, 10.63), None);
    }

    #[test]
    fn a_block_whose_parts_have_settled_asks_for_no_frame() {
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
                "{reach} is not {MARKER_R}"
            );
        }

        let width = points[1].x - points[5].x;
        let height = points[3].y - points[0].y;
        assert!((height - 2.0 * MARKER_R).abs() < 0.001, "{height}");
        assert!((width / height - 0.866).abs() < 0.001);
    }
}
