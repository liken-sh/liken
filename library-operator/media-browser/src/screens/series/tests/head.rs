// The header of a series page while focus moves from the wall to the rows
// under the seasons. The header holds while focus is on the wall, because
// it shows the focused episode, and scrolls away with the page once focus
// is on a row under the seasons, so no row passes under the header's text.

use super::*;
use crate::look;
use crate::screens::franchise::strips::Place;
use crate::views::screen::FRAME;
use iced_winit::core::Rectangle;

use super::super::layout::{self, VIEWPORT_TOP, head_shown};
use super::super::page::{placed, rows, studio_box};

// A series long enough that its rows sit far below the header: a
// franchise strip, the crew and the cast, the More from strip, and the
// studios under three seasons of ten.
fn long() -> Serials {
    Serials {
        credits: true,
        franchise: true,
        made: true,
        seasons: vec![10, 10, 10],
        ..Serials::default()
    }
}

fn on(focus: Focus) -> Series {
    let (mut page, _) = page(long());
    page.focus = focus;
    page
}

// Whether two boxes share any part. A box with no height shares none, so
// a header scrolled wholly away holds nothing a row can cross.
fn crosses(row: Rectangle, head: Rectangle) -> bool {
    head.height > 0.0
        && row.y < head.y + head.height
        && head.y < row.y + row.height
        && row.x < head.x + head.width
        && head.x < row.x + row.width
}

#[test]
fn the_header_holds_its_whole_height_while_focus_is_on_the_wall() {
    for focus in [Focus::Still(0), Focus::Still(29), Focus::Buttons(0)] {
        let page = on(focus);
        let placed = placed(&page, FRAME);
        assert_eq!(placed.lift, 0.0, "{focus:?}");
        let head = head_shown(FRAME, placed.lift);
        assert_eq!((head.y, head.height), (0.0, layout::head()), "{focus:?}");
    }
}

#[test]
fn no_row_crosses_the_part_of_the_header_still_drawn_when_focus_is_under_the_seasons() {
    let cases = [
        Focus::Franchise(0, Place::Heading),
        Focus::Stripe(0, 0),
        Focus::Stripe(1, 0),
        Focus::More(0),
        Focus::Studio(0),
    ];
    for focus in cases {
        let page = on(focus);
        let placed = placed(&page, FRAME);
        let head = head_shown(FRAME, placed.lift);
        for row in rows(&page, &placed, placed.offset, Some(focus)) {
            let region = row.region();
            assert!(
                !crosses(region, head),
                "{focus:?}: row {region:?} crosses the header {head:?}"
            );
        }
    }
}

// The focused row rests whole between the strip's line and the foot of
// the frame, and the header has scrolled away far enough to leave it
// that room.
#[test]
fn focus_on_a_row_under_the_seasons_rests_it_whole_under_the_strip() {
    let cases = [
        Focus::Franchise(0, Place::Heading),
        Focus::Stripe(0, 0),
        Focus::Stripe(1, 0),
        Focus::More(0),
    ];
    for focus in cases {
        let page = on(focus);
        let placed = placed(&page, FRAME);
        let row = rows(&page, &placed, placed.offset, Some(focus))
            .iter()
            .find(|row| row.focused().is_some())
            .map(|row| row.region())
            .unwrap_or_else(|| panic!("{focus:?}: no focused row"));
        assert!(placed.lift > 0.0, "{focus:?}");
        assert!(row.y >= VIEWPORT_TOP, "{focus:?}: {row:?}");
        assert!(row.y + row.height <= FRAME.height, "{focus:?}: {row:?}");
    }
    let page = on(Focus::Studio(0));
    let placed = placed(&page, FRAME);
    let name = studio_box(&page, &placed, 0).expect("a studio");
    assert!(name.y >= VIEWPORT_TOP && name.y + name.height <= FRAME.height);
}

// The header and the wall ease to their new places on one press, on the
// page's own scroll curve, and the header comes back when focus returns
// to the wall.
#[test]
fn a_press_from_the_last_season_into_the_rows_eases_the_header_away_and_back() {
    let (mut page, mut source) = page(long());
    page.focus = Focus::Still(29);
    page.follow(FRAME, 0.0, None);
    page.key("down", &mut source);
    assert!(!matches!(page.focus, Focus::Still(_)), "{:?}", page.focus);

    let to = placed(&page, FRAME).lift;
    assert!(to > 0.0);
    assert_eq!(page.follow(FRAME, 1.0, Some(1.0)), 1.0 + look::SCROLL);
    let half = page.lift.toward(to, 1.0 + look::SCROLL / 2.0);
    assert!(0.0 < half && half < to, "{half}");
    assert_eq!(page.lift.toward(to, 1.0 + look::SCROLL), to);

    page.key("up", &mut source);
    assert!(matches!(page.focus, Focus::Still(_)), "{:?}", page.focus);
    assert_eq!(placed(&page, FRAME).lift, 0.0);
}
