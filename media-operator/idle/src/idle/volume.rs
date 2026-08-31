//! The volume row: a speaker glyph, a short bar, and the number.
//!
//! The row comes and goes on a clock of its own, and it draws alone: a level
//! change brings up the row and nothing else on the screen. The bus holds the
//! level and the muted flag, and the client only reads them, because the idle
//! sidecar owns every change to them.

use iced_widget::canvas::{LineJoin, Path, Stroke, Style};
use iced_winit::core::{Color, Point, Rectangle, Size};

use super::{Frame, Layout};
use crate::look;
use crate::unit::Unit;

/// The bar fills at this level, which is unity. A level above it fills no
/// further.
const FULL: f64 = 100.0;

/// A fade takes this long to reach full and this long to reach clear. The out
/// is longer than the in, so the row leaves more slowly than it arrives.
const FADE_IN: f64 = 0.35;
const FADE_OUT: f64 = 0.6;
/// The row leaves this many seconds after the last press. Each press restarts
/// the wait, so a run of presses holds the row on screen.
const HOLD: f64 = 4.0;

/// The number reserves this much width at the right margin, so the bar and the
/// glyph hold their place as the number moves between one and three digits.
const NUMBER_WIDTH: f32 = 84.0;
/// The bar is short because the number beside it carries the reading, and the
/// bar shows the level at a glance.
const BAR_WIDTH: f32 = 220.0;
const BAR_HEIGHT: f32 = 12.0;
const BAR_RADIUS: f32 = 3.0;
/// The glyph's box, and the gap between the glyph and the bar.
const GLYPH_WIDTH: f32 = 26.0;
const GLYPH_HEIGHT: f32 = 30.0;
const GLYPH_GAP: f32 = 16.0;
/// The number's line box, in canvas pixels. The bar and the glyph centre on
/// the middle of it and the dark surface covers it, so the three parts read as
/// one row whatever the number's own type size.
///
/// `volume.lua` writes this measure as `theme.type.small`, the same `\fs` it
/// draws the number at. The number here draws at `look::SMALL`, which is that
/// `\fs` through the face metric, and the measure is that size's line box,
/// which is the `\fs` again. The two readings meet at 34 canvas pixels.
const NUMBER_BOX: f32 = look::line_box(look::SMALL);
/// The row appears over whatever frame is on screen, with nothing under it, and
/// on a bright frame the glyph and the number would vanish. So the row carries
/// a dark surface of its own, and this is the padding around the three parts.
const PAD_X: f32 = 24.0;
const PAD_Y: f32 = 12.0;
const SURFACE_RADIUS: f32 = 14.0;

/// The speaker, one closed polygon of the driver box and the cone, in the
/// glyph's own box. The image carries no icon font, so the mark is a path.
const SPEAKER: [(f32, f32); 6] = [
    (0.0, 10.0),
    (10.0, 10.0),
    (22.0, 0.0),
    (22.0, 30.0),
    (10.0, 20.0),
    (0.0, 20.0),
];
/// The muted state draws this slash across the speaker, so one element carries
/// both the level and the mute.
const SLASH: [(f32, f32); 4] = [(2.0, 24.0), (24.0, 2.0), (24.0, 8.0), (2.0, 30.0)];
/// The slash draws in the speaker's own colour, and the two would read as one
/// shape without an outline between them. This is the width of that outline
/// outside the slash.
const SLASH_BORDER: f32 = 2.0;

/// The row's own fade, from 0 off screen to 1 full.
///
/// The command sidecar publishes a press after it applies a level from the bus,
/// and never for the retained value a client reads when it first connects. So
/// the row answers a press, and it stays off screen while a pod restores the
/// level it starts with.
pub fn fade(unit: &Unit, at: f64) -> f64 {
    let Some(pressed) = unit.pressed else {
        return 0.0;
    };

    let since = at - pressed;
    let arriving = unit.pressed_from + since / FADE_IN;
    let leaving = 1.0 - (since - HOLD) / FADE_OUT;

    arriving.min(leaving).clamp(0.0, 1.0)
}

/// The second the row next changes, for [`super::Idle::next_frame`].
///
/// The row is the one element with a still phase of its own, so it is the one
/// element that names a second ahead of `at` rather than a draw now. It has
/// three phases. The two fades change the row on every frame they cover, and
/// they answer with `at`. Between them the row is up and steady, so it names
/// the second the row starts to leave and the loop sleeps through the four
/// seconds a person reads it. A row that has left states nothing.
///
/// A press moves `pressed` and lifts `pressed_from` from where the row stands,
/// and the harness drops the second it holds on a key, so a press that lands
/// during the hold is drawn at once and schedules its own leaving.
pub fn next_frame(unit: &Unit, at: f64) -> Option<f64> {
    let pressed = unit.pressed?;
    // A row lifted from part way up reaches full sooner, by exactly the part
    // of the fade it started above.
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

/// How much of the bar the level fills, in canvas pixels.
fn filled(level: i64) -> f32 {
    BAR_WIDTH * (level as f64 / FULL).clamp(0.0, 1.0) as f32
}

/// Where the parts of the row stand.
///
/// The three parts hang off the right margin, so the row measures itself when
/// it draws. The canvas width follows the screen, and a row measured once at
/// startup holds the margin of another screen.
struct Row {
    surface: Rectangle,
    bar: Rectangle,
    glyph: Point,
    number: Point,
}

fn row(layout: &Layout) -> Row {
    // The row is the third line of the top-right column, under the clock and
    // the activity line, because the low middle of the screen holds the mark.
    let top = look::MARGIN_Y + 2.0 * look::LINE_PITCH;
    let right = layout.right();
    // The bar and the glyph centre on the middle of the number's line, so the
    // three parts read as one row.
    let middle = top + NUMBER_BOX / 2.0;

    let bar_x = right - NUMBER_WIDTH - BAR_WIDTH;
    let glyph_x = bar_x - GLYPH_GAP - GLYPH_WIDTH;
    let surface_x = glyph_x - PAD_X;

    Row {
        surface: Rectangle {
            x: surface_x,
            y: top - PAD_Y,
            width: right + PAD_X - surface_x,
            // The number's line is the tallest of the three parts, so the
            // surface covers it and the padding.
            height: NUMBER_BOX + 2.0 * PAD_Y,
        },
        bar: Rectangle {
            x: bar_x,
            y: middle - BAR_HEIGHT / 2.0,
            width: BAR_WIDTH,
            height: BAR_HEIGHT,
        },
        glyph: Point::new(glyph_x, middle - GLYPH_HEIGHT / 2.0),
        number: Point::new(right, top),
    }
}

/// One rounded rectangle. A radius wider than half the shape has no meaning, so
/// a bar with a few pixels of fill rounds by what it has.
fn rounded(shape: Rectangle, radius: f32) -> Path {
    let radius = radius.min(shape.width / 2.0).min(shape.height / 2.0);
    Path::rounded_rectangle(
        Point::new(shape.x, shape.y),
        Size::new(shape.width, shape.height),
        radius.into(),
    )
}

/// One closed polygon in the glyph's own box, placed at `at`.
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

/// Draw the element into the frame, in canvas units.
///
/// The glyph alone carries the muted state, so the bar reads the level in both
/// states. Every part draws at the row's own fade, because the row arrives and
/// leaves on a clock no other element reads, and then at the shade's factor
/// with the rest of the screen.
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
        &rounded(row.surface, SURFACE_RADIUS),
        shaded(Color::BLACK, look::SURFACE * fade),
    );
    frame.fill(
        &rounded(row.bar, BAR_RADIUS),
        shaded(look::track(), look::TRACK_OPACITY * fade),
    );

    let filled = filled(unit.volume.level);
    if filled >= 1.0 {
        frame.fill(
            &rounded(
                Rectangle {
                    width: filled,
                    ..row.bar
                },
                BAR_RADIUS,
            ),
            shaded(look::fill(), fade),
        );
    }

    frame.fill(&polygon(&SPEAKER, row.glyph), shaded(ink, fade));
    if unit.volume.muted {
        let slash = polygon(&SLASH, row.glyph);
        // The toolkit centres a stroke on the path it follows, and the outline
        // this glyph needs stands outside the slash, so the stroke is twice the
        // border and the fill covers the half that fell inside. The width is
        // in surface pixels, for the reason the canvas scale is documented on
        // Layout.
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
        unit.volume.level.to_string(),
        row.number,
        look::Anchor::TopRight,
        look::SMALL,
        shaded(look::text(), fade),
    ));
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::bus::Message;
    use crate::bus::volume::Volume;

    /// A unit that read a level and then a press of it.
    fn pressed(at: f64) -> Unit {
        let mut unit = Unit::default();
        let volume = Volume {
            level: 40,
            muted: false,
        };
        unit.fold(
            Message::Level {
                volume,
                pressed: false,
            },
            0.0,
        );
        unit.fold(
            Message::Level {
                volume,
                pressed: true,
            },
            at,
        );
        unit
    }

    #[test]
    fn the_catch_up_shows_no_row() {
        let mut unit = Unit::default();
        unit.fold(
            Message::Level {
                volume: Volume::default(),
                pressed: false,
            },
            1.0,
        );
        assert_eq!(fade(&unit, 1.0), 0.0);
        assert_eq!(fade(&unit, 2.0), 0.0);
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
    fn the_bar_fills_at_unity_and_no_further() {
        assert_eq!(filled(0), 0.0);
        assert_eq!(filled(50), BAR_WIDTH / 2.0);
        assert_eq!(filled(100), BAR_WIDTH);
        assert_eq!(filled(140), BAR_WIDTH);
    }

    #[test]
    fn the_two_fades_ask_for_a_frame_now() {
        let unit = pressed(10.0);
        assert_eq!(next_frame(&unit, 10.0), Some(10.0));
        assert_eq!(next_frame(&unit, 10.34), Some(10.34));
        assert_eq!(next_frame(&unit, 14.3), Some(14.3));
    }

    #[test]
    fn the_row_sleeps_through_its_hold_and_wakes_to_leave() {
        let unit = pressed(10.0);
        // The row is up and steady from the end of the fade in, so every
        // second of the hold names the one moment the row changes again.
        assert_eq!(next_frame(&unit, 10.35), Some(14.0));
        assert_eq!(next_frame(&unit, 13.9), Some(14.0));
    }

    #[test]
    fn a_press_that_lifts_a_leaving_row_shortens_its_fade_in() {
        let mut unit = pressed(10.0);
        // The row is halfway out at 14.3 seconds, and the press lifts it from
        // there, so it reaches full in half of the 350 ms fade.
        unit.fold(
            Message::Level {
                volume: unit.volume,
                pressed: true,
            },
            14.3,
        );

        assert_eq!(next_frame(&unit, 14.47), Some(14.47));
        assert_eq!(next_frame(&unit, 14.48), Some(18.3));
    }

    #[test]
    fn a_row_that_has_left_asks_for_no_frame() {
        assert_eq!(next_frame(&pressed(10.0), 14.61), None);
        assert_eq!(next_frame(&Unit::default(), 600.0), None);
    }

    #[test]
    fn the_row_stands_two_line_pitches_under_the_top_margin() {
        let row = row(&Layout::for_surface(Size::new(1920.0, 1080.0)));
        assert_eq!(row.number.x, 1780.0);
        // The top margin is 90, and a pitch is a 34 pixel line box and 12
        // pixels of air.
        assert!((row.number.y - 182.0).abs() < 0.2, "{}", row.number.y);
        assert_eq!(row.bar.x, 1780.0 - 84.0 - 220.0);
        assert_eq!(row.glyph.x, row.bar.x - 16.0 - 26.0);
    }

    #[test]
    fn the_surface_covers_the_three_parts_and_the_padding() {
        let row = row(&Layout::for_surface(Size::new(1920.0, 1080.0)));
        assert_eq!(row.surface.x, row.glyph.x - PAD_X);
        assert_eq!(row.surface.x + row.surface.width, 1780.0 + PAD_X);
        assert_eq!(row.surface.height, NUMBER_BOX + 2.0 * PAD_Y);
    }

    #[test]
    fn the_bar_and_the_glyph_centre_on_the_number_s_line() {
        let row = row(&Layout::for_surface(Size::new(1920.0, 1080.0)));
        let middle = |shape: Rectangle| shape.y + shape.height / 2.0;
        assert_eq!(middle(row.bar), row.number.y + NUMBER_BOX / 2.0);
        assert_eq!(row.glyph.y + GLYPH_HEIGHT / 2.0, middle(row.bar));
    }
}
