//! The next work in the right half of the screen, which the display draws in
//! place of the corner card while the film stands stepped back on the left.
//!
//! The column takes the card's lines and the card's picture at a larger
//! size: the art over the reason, the title in the title type, the detail,
//! the time the next work would end, and a line that names the two keys. It
//! draws in canvas pixels, so it scales with the canvas the way every other
//! part of the display does, and it ends on the right margin.

use iced::{Color, Point, Rectangle, Size};
use jiff::Zoned;

use super::{Art, Offer, ends_at};
use crate::canvas::{Anchor, Brush, Canvas, Line, clip};
use crate::theme;

/// The art box, 704 by 396, a 16:9 picture as wide as the column.
pub(super) const ART: Size = Size::new(704.0, 396.0);
/// The column is placed from its foot. Near the credits the scrubber's
/// current time stands under the column's right end, from 846 down to the
/// bar, so the line of keys and the focus panel stay where they are, and a
/// line added to the column moves the art and the lines above it up. The
/// art starts below the line box of the corner clock's end time, which ends
/// at 170.
const ART_Y: f32 = 176.0;
const _: () = assert!(ART_Y >= theme::MARGIN_Y + theme::LINE_PITCH + theme::type_scale::SMALL);
/// The top of each line under the art. The end time drops one line pitch
/// from the detail, and the line of keys one more.
const REASON_Y: f32 = 596.0;
const TITLE_Y: f32 = 636.0;
const DETAIL_Y: f32 = 716.0;
const ENDS_Y: f32 = DETAIL_Y + theme::LINE_PITCH;
const HINT_Y: f32 = 808.0;
/// How far the focus panel reaches past the column on every side.
const FOCUS_PAD: f32 = 16.0;
/// The bottom of the focus panel, under the line of keys.
const FOCUS_BOTTOM: f32 = 858.0;

/// The two keys the column answers, in the tiny type at its foot.
const HINT: &str = "OK  Play now   ·   Back  Keep watching";

/// The column's left edge. Its right edge is the right margin.
pub(super) fn left(canvas: &Canvas) -> f32 {
    canvas.right() - ART.width
}

pub(super) fn draw(
    brush: &mut Brush<'_>,
    offer: &Offer,
    focused: bool,
    art: &dyn Art,
    now: &Zoned,
) {
    let canvas = brush.canvas();
    let x = left(&canvas);
    if focused {
        brush.panel(Rectangle::new(
            Point::new(x - FOCUS_PAD, ART_Y - FOCUS_PAD),
            Size::new(
                ART.width + 2.0 * FOCUS_PAD,
                FOCUS_BOTTOM - ART_Y + FOCUS_PAD,
            ),
        ));
    }
    let plate = Rectangle::new(Point::new(x, ART_Y), ART);
    brush.rect(plate, theme::at(theme::color::SHADOW, theme::alpha::PANEL));
    if let Some(size) = art.next() {
        art.draw(brush, centred(&canvas, plate, size));
    }
    for line in lines(&canvas, offer, now) {
        brush.text(line);
    }
}

/// The picture in the middle of its plate, on the output pixel grid. The
/// decode fits it inside the plate and keeps its ratio, so a poster stands
/// in the middle with the plate on both sides.
fn centred(canvas: &Canvas, plate: Rectangle, size: Size) -> Rectangle {
    Rectangle::new(
        Point::new(
            canvas.snap(plate.center().x - size.width / 2.0).max(0.0),
            canvas.snap(plate.center().y - size.height / 2.0).max(0.0),
        ),
        size,
    )
}

/// The lines under the art, each clipped to the column's width. The end time
/// reads the wall clock `now`.
fn lines(canvas: &Canvas, offer: &Offer, now: &Zoned) -> Vec<Line> {
    let x = left(canvas);
    let room = ART.width;
    let line = |text: &str, y: f32, size: f32, color: Color| {
        Line::new(
            clip(text, size, room),
            Point::new(x, y),
            Anchor::TopLeft,
            size,
            color,
        )
    };
    let mut lines = Vec::new();
    if let Some(reason) = offer.reason.as_deref() {
        lines.push(line(
            &reason.to_uppercase(),
            REASON_Y,
            theme::type_scale::TINY,
            theme::color::muted(),
        ));
    }
    if let Some(title) = offer.title.as_deref() {
        lines.push(line(
            title,
            TITLE_Y,
            theme::type_scale::TITLE,
            theme::color::text(),
        ));
    }
    if let Some(detail) = offer.detail.as_deref() {
        lines.push(line(
            detail,
            DETAIL_Y,
            theme::type_scale::SMALL,
            theme::color::muted(),
        ));
    }
    if let Some(ends) = ends_at(offer, now) {
        lines.push(line(
            &ends,
            ENDS_Y,
            theme::type_scale::SMALL,
            theme::color::muted(),
        ));
    }
    lines.push(line(
        HINT,
        HINT_Y,
        theme::type_scale::TINY,
        theme::color::muted(),
    ));
    lines
}

#[cfg(test)]
mod tests {
    use super::*;

    fn offer() -> Offer {
        Offer {
            reason: Some("Next in Harbor Lights · S02".to_string()),
            title: Some("E05 · The Long Tide".to_string()),
            detail: Some("Harbor Lights · S02 · 45 min".to_string()),
            art: None,
            remaining: Some(2700),
        }
    }

    /// 1970-01-01 22:30 UTC.
    fn evening() -> jiff::Zoned {
        crate::clock::at(22 * 3600 + 30 * 60)
    }

    /// The design's numbers on a 1920 by 1080 canvas: the column starts at
    /// 1120 and ends on the right margin at 1824.
    #[test]
    fn the_column_takes_the_right_half_of_the_canvas() {
        let canvas = Canvas::default();
        assert_eq!(left(&canvas), 1120.0);
        assert_eq!(left(&canvas) + ART.width, canvas.right());
    }

    #[test]
    fn the_lines_stand_where_the_design_puts_them() {
        let canvas = Canvas::default();
        let placed: Vec<(String, f32, f32)> = lines(&canvas, &offer(), &evening())
            .into_iter()
            .map(|line| (line.content.clone(), line.at.x, line.at.y))
            .collect();
        assert_eq!(
            placed,
            [
                ("NEXT IN HARBOR LIGHTS · S02".to_string(), 1120.0, 596.0),
                ("E05 · The Long Tide".to_string(), 1120.0, 636.0),
                ("Harbor Lights · S02 · 45 min".to_string(), 1120.0, 716.0),
                ("Ends at 11:15 pm".to_string(), 1120.0, 762.0),
                (HINT.to_string(), 1120.0, 808.0),
            ]
        );
    }

    /// The end time takes the detail's type and colour.
    #[test]
    fn the_end_time_reads_like_the_detail() {
        let lines = lines(&Canvas::default(), &offer(), &evening());
        assert_eq!(lines[3].size, theme::type_scale::SMALL);
        assert_eq!(lines[3].color, theme::color::muted());
        assert_eq!(lines[3].size, lines[2].size);
    }

    /// The end time is the wall clock plus the seconds that remain, and it
    /// rolls past midnight.
    #[test]
    fn the_end_time_reads_the_wall_clock_plus_the_seconds_that_remain() {
        let late = crate::clock::at(23 * 3600 + 50 * 60);
        let lines = lines(&Canvas::default(), &offer(), &late);
        assert_eq!(lines[3].content, "Ends at 12:35 am");
    }

    /// A block with no seconds draws no end time, and every other line keeps
    /// its row.
    #[test]
    fn an_offer_with_no_seconds_draws_no_end_time() {
        let offer = Offer {
            remaining: None,
            ..offer()
        };
        let rows: Vec<f32> = lines(&Canvas::default(), &offer, &evening())
            .iter()
            .map(|line| line.at.y)
            .collect();
        assert_eq!(rows, [596.0, 636.0, 716.0, 808.0]);
    }

    /// The column stays clear of the line box of the corner clock's end
    /// time above it, and its focus panel ends where it did, above the
    /// scrubber.
    #[test]
    fn the_column_keeps_its_bottom_and_its_art_clears_the_clock() {
        assert_eq!(ART_Y, 176.0);
        assert_eq!(FOCUS_BOTTOM, 858.0);
        assert_eq!(ENDS_Y - DETAIL_Y, theme::LINE_PITCH);
    }

    #[test]
    fn the_title_takes_the_title_type() {
        let canvas = Canvas::default();
        let title = &lines(&canvas, &offer(), &evening())[1];
        assert_eq!(title.size, theme::type_scale::TITLE);
    }

    #[test]
    fn a_poster_stands_in_the_middle_of_the_plate() {
        let canvas = Canvas::default();
        let plate = Rectangle::new(Point::new(left(&canvas), ART_Y), ART);
        let bounds = centred(&canvas, plate, Size::new(264.0, 396.0));
        assert_eq!(bounds.position(), Point::new(1340.0, 176.0));
    }
}
