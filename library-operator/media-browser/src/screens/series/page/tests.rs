use super::*;
use crate::screens::movie::row::Button;

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
