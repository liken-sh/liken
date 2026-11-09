// The studios and networks on a series page's foot: each one a stop under
// the stripes, which opens that studio's wall.

use super::*;
use crate::catalog::Query;

#[test]
fn down_from_the_last_stripe_reaches_the_first_studio_and_up_returns() {
    let (mut page, mut source) = credited(Serials::default());
    let last = page.stripes.last().expect("the series credits people");
    page.focus = Focus::Stripe(last.0, last.1);
    assert_eq!(pressed(&mut page, &mut source, "down"), Focus::Studio(0));
    assert_eq!(
        pressed(&mut page, &mut source, "up"),
        Focus::Stripe(last.0, last.1)
    );
}

#[test]
fn down_from_the_last_row_reaches_the_studios_where_nobody_is_credited() {
    let (mut page, mut source) = page(Serials::default());
    page.focus = Focus::Still(13);
    assert_eq!(pressed(&mut page, &mut source, "down"), Focus::Studio(0));
    assert_eq!(still(pressed(&mut page, &mut source, "up")), 13);
}

#[test]
fn the_studios_follow_the_franchise_strip_and_up_returns_to_it() {
    let (mut page, mut source) = page(Serials {
        franchise: true,
        ..Serials::default()
    });
    page.focus = Focus::Franchise(0, crate::screens::franchise::strips::Place::Heading);
    pressed(&mut page, &mut source, "down");
    let below = pressed(&mut page, &mut source, "down");
    assert_eq!(below, Focus::Studio(0));
    assert!(matches!(
        pressed(&mut page, &mut source, "up"),
        Focus::Franchise(0, _)
    ));
}

#[test]
fn left_and_right_move_across_the_studios_and_down_holds() {
    let (mut page, mut source) = page(Serials::default());
    page.focus = Focus::Studio(0);
    assert_eq!(pressed(&mut page, &mut source, "right"), Focus::Studio(1));
    assert_eq!(pressed(&mut page, &mut source, "down"), Focus::Studio(1));
    assert_eq!(pressed(&mut page, &mut source, "left"), Focus::Studio(0));
}

#[test]
fn a_select_on_a_network_opens_its_wall_over_the_page() {
    let (mut page, mut source) = page(Serials::default());
    page.focus = Focus::Studio(1);
    let Step::Open(Screen::Wall(wall)) = page.key("enter", &mut source) else {
        panic!("a network opens a wall");
    };
    assert_eq!(wall.slots.query, Query::studio("A Network"));
    page.focus = Focus::Studio(9);
    assert!(matches!(page.key("enter", &mut source), Step::Stay));
}

// A series with no episode and nobody credited reaches its studios from
// its trailer button, and up from them holds, because nothing stands
// over them on the wall.
#[test]
fn up_from_the_studios_of_an_empty_series_holds() {
    let (mut page, mut source) = page(Serials {
        empty: true,
        ..Serials::default()
    });
    page.focus = Focus::Studio(0);
    assert_eq!(pressed(&mut page, &mut source, "up"), Focus::Studio(0));
}

#[test]
fn a_reread_holds_the_studio_and_returns_to_the_wall_where_none_is_left() {
    let (mut page, mut source) = page(Serials::default());
    page.focus = Focus::Studio(9);
    page.reread(&mut source);
    assert_eq!(page.focus, Focus::Studio(1));
    assert!(page.focused().is_none());

    source.unproduced = true;
    page.reread(&mut source);
    assert_eq!(page.focus, Focus::Still(0));
}

// The page scrolls to its end, and the outline goes around the focused
// name where the foot draws it, on the screen.
#[test]
fn the_outline_goes_around_the_focused_studio_at_the_end_of_the_wall() {
    let (mut page, _) = credited(Serials::default());
    page.focus = Focus::Studio(1);
    let frame = crate::views::screen::FRAME;
    let placed = super::super::page::placed(&page, frame);
    let name = super::super::page::studio_box(&page, &placed, 1).expect("two names");
    assert_eq!(page.marked(frame), Some(crate::views::marked(name)));
    assert!(placed.offset > 0.0);
    assert!(name.y + name.height <= frame.height);
}
