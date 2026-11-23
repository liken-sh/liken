//! The up-next column. While the arranger has stepped the film back to the
//! left half of the screen, the display draws the next work in this column
//! on the right half, in place of the corner card. It shows the same art and
//! lines as the card: the reason, the title, the detail, and the time the
//! next work would end. Each line is one step larger on the type scale than
//! on the card, because the column is read from across the room.

use iced::{Color, Point, Rectangle, Size};
use jiff::ZonedDateTime;

use super::{Art, Offer, ends_at};
use crate::canvas::{Anchor, Brush, Canvas, Line, clip};
use crate::theme;

/// The art box, 16:9 and as wide as the column.
pub(super) const ART: Size = Size::new(704.0, 396.0);
/// The top of the art, just below the clock's end time, whose line box ends
/// at y 170. The positions are fixed from the bottom up: near the credits the
/// scrubber draws the current time under the column's right end, from y 846
/// down to the bar, and the focus panel has to end above it. So a new line
/// in the column moves the lines above it and the art up.
const ART_Y: f32 = 176.0;
const _: () = assert!(ART_Y >= theme::MARGIN_Y + theme::LINE_PITCH + theme::type_scale::SMALL);
/// The top of each line under the art. The 12 in `ENDS_DROP` is the gap that
/// `theme::LINE_PITCH` puts between lines.
const REASON_Y: f32 = 604.0;
const TITLE_Y: f32 = 652.0;
const DETAIL_Y: f32 = 740.0;
const ENDS_Y: f32 = DETAIL_Y + ENDS_DROP;
const ENDS_DROP: f32 = theme::type_scale::LABEL + 12.0;
/// How far the focus panel extends past the column on the left, the right,
/// and the top. `FOCUS_BOTTOM` sets its bottom edge.
const FOCUS_PAD: f32 = 16.0;
const FOCUS_BOTTOM: f32 = 858.0;
const _: () = assert!(ENDS_Y + theme::type_scale::LABEL + FOCUS_PAD <= FOCUS_BOTTOM);

/// The column's left edge. The column ends at the right margin.
pub(super) fn left(canvas: &Canvas) -> f32 {
    canvas.right() - ART.width
}

/// Draws the focus panel when `focused`, then the plate, the art, and the
/// lines.
pub(super) fn draw(
    brush: &mut Brush<'_>,
    offer: &Offer,
    focused: bool,
    art: &dyn Art,
    now: &ZonedDateTime,
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

/// Centers a picture of `size` on the plate, snapped to the output pixels.
/// The decode keeps the picture's aspect ratio, so a poster is narrower than
/// the plate and leaves plate showing on both sides.
fn centred(canvas: &Canvas, plate: Rectangle, size: Size) -> Rectangle {
    Rectangle::new(
        Point::new(
            canvas.snap(plate.center().x - size.width / 2.0).max(0.0),
            canvas.snap(plate.center().y - size.height / 2.0).max(0.0),
        ),
        size,
    )
}

/// The lines under the art, each clipped to the column's width. A field the
/// offer does not have draws no line, and the other lines keep their rows.
fn lines(canvas: &Canvas, offer: &Offer, now: &ZonedDateTime) -> Vec<Line> {
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
            theme::type_scale::SMALL,
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
            theme::type_scale::LABEL,
            theme::color::muted(),
        ));
    }
    if let Some(ends) = ends_at(offer, now) {
        lines.push(line(
            &ends,
            ENDS_Y,
            theme::type_scale::LABEL,
            theme::color::muted(),
        ));
    }
    lines
}

#[cfg(test)]
mod tests {
    use super::*;

    fn offer() -> Offer {
        Offer {
            reason: Some("Next in Harbor Lights · S02".to_string()),
            title: Some("E05 · The Long Tide".to_string()),
            detail: Some("Harbor Lights · S02".to_string()),
            art: None,
            remaining: Some(2700),
        }
    }

    /// 1970-01-01 22:30 UTC.
    fn evening() -> jiff::ZonedDateTime {
        crate::clock::at(22 * 3600 + 30 * 60)
    }

    #[test]
    fn the_column_ends_at_the_right_margin() {
        let canvas = Canvas::default();
        assert_eq!(left(&canvas), 1120.0);
        assert_eq!(left(&canvas) + ART.width, canvas.right());
    }

    #[test]
    fn the_lines_are_placed_at_their_fixed_positions() {
        let canvas = Canvas::default();
        let placed: Vec<(String, f32, f32)> = lines(&canvas, &offer(), &evening())
            .into_iter()
            .map(|line| (line.content.clone(), line.at.x, line.at.y))
            .collect();
        assert_eq!(
            placed,
            [
                ("NEXT IN HARBOR LIGHTS · S02".to_string(), 1120.0, 604.0),
                ("E05 · The Long Tide".to_string(), 1120.0, 652.0),
                ("Harbor Lights · S02".to_string(), 1120.0, 740.0),
                ("45m · ends at 11:15 pm".to_string(), 1120.0, 792.0),
            ]
        );
    }

    #[test]
    fn the_end_time_reads_like_the_detail() {
        let lines = lines(&Canvas::default(), &offer(), &evening());
        assert_eq!(lines[3].size, theme::type_scale::LABEL);
        assert_eq!(lines[3].color, theme::color::muted());
        assert_eq!(lines[3].size, lines[2].size);
    }

    /// The end time rolls past midnight.
    #[test]
    fn the_end_time_reads_the_wall_clock_plus_the_seconds_that_remain() {
        let late = crate::clock::at(23 * 3600 + 50 * 60);
        let lines = lines(&Canvas::default(), &offer(), &late);
        assert_eq!(lines[3].content, "45m · ends at 12:35 am");
    }

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
        assert_eq!(rows, [604.0, 652.0, 740.0]);
    }

    #[test]
    fn the_art_top_and_the_focus_panel_bottom_keep_their_positions() {
        assert_eq!(ART_Y, 176.0);
        assert_eq!(FOCUS_BOTTOM, 858.0);
        assert_eq!(ENDS_Y, 792.0);
    }

    #[test]
    fn the_title_takes_the_title_type() {
        let canvas = Canvas::default();
        let title = &lines(&canvas, &offer(), &evening())[1];
        assert_eq!(title.size, theme::type_scale::TITLE);
    }

    #[test]
    fn a_narrow_poster_is_centered_on_the_plate() {
        let canvas = Canvas::default();
        let plate = Rectangle::new(Point::new(left(&canvas), ART_Y), ART);
        let bounds = centred(&canvas, plate, Size::new(264.0, 396.0));
        assert_eq!(bounds.position(), Point::new(1340.0, 176.0));
    }
}
