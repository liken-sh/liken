use super::header::buttons_left;
use super::*;
use crate::screens::movie::row::Button;
use crate::views::buttons;

// A row wholly under the region's top keeps its text, and a row the
// region's top cuts draws none of it, so no caption hangs under pictures
// that are cut away.
#[test]
fn a_row_cut_at_the_top_of_the_region_draws_no_text() {
    let top = 154.0;
    let cases = [
        (area(96.0, 200.0, 1728.0, 380.0), top),
        (area(96.0, 154.0, 1728.0, 380.0), top),
        (area(96.0, 150.0, 1728.0, 380.0), 530.0),
        (area(96.0, -100.0, 1728.0, 380.0), 280.0),
    ];
    for (row, line) in cases {
        assert_eq!(captions_line(row, top), line, "{row:?}");
    }
}

// An episode's widest row, with a start to resume and a trailer, fits in
// the room the header leaves beside its text column at 1920 by 1080.
#[test]
fn an_episodes_widest_row_fits_beside_the_text_column() {
    let bounds = area(0.0, 0.0, 1920.0, 1080.0);
    let region = layout::header(bounds);
    let words = [Button::Resume, Button::StartOver, Button::Trailer].map(Button::word);
    let last = buttons::button(
        &words,
        2,
        Point::new(buttons_left(region, bounds.width * COLUMN), 0.0),
    );
    let room = region.x + region.width;
    assert!(last.x + last.width <= room, "{last:?} past {room}");
}
