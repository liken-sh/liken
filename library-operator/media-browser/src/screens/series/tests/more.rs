// The More from strip on a series page: the first studio or network that
// aired enough else takes it, it leaves out what the franchise strip
// draws, and it stands between the stripes and the studios that stay
// names.

use super::*;
use crate::catalog::Query;

fn made(serials: Serials) -> (Series, Serials) {
    credited(Serials {
        made: true,
        ..serials
    })
}

fn ids(page: &Series) -> Vec<&str> {
    let more = page.more.as_ref().expect("the page draws the strip");
    more.members.iter().map(|item| item.id.as_str()).collect()
}

#[test]
fn the_first_network_that_aired_enough_else_takes_the_strip() {
    let (page, _) = made(Serials::default());
    let more = page.more.as_ref().expect("the page draws the strip");
    assert_eq!(more.heading, "More from A Network");
    assert_eq!(
        ids(&page),
        [
            "series:six",
            "series:two",
            "series:five",
            "series:four",
            "series:three"
        ]
    );
    assert_eq!(page.foot.studios().collect::<Vec<_>>(), ["A Studio"]);
}

#[test]
fn the_strip_leaves_out_what_the_franchise_strip_draws() {
    let (page, _) = made(Serials {
        franchise: true,
        ..Serials::default()
    });
    assert_eq!(
        ids(&page),
        ["series:six", "series:five", "series:four", "series:three"]
    );
}

#[test]
fn a_network_with_too_few_other_serials_stays_a_name() {
    let (page, _) = credited(Serials::default());
    assert!(page.more.is_none());
    assert_eq!(
        page.foot.studios().collect::<Vec<_>>(),
        ["A Studio", "A Network"]
    );
}

#[test]
fn down_from_the_last_stripe_reaches_the_strip_and_then_the_studios() {
    let (mut page, mut source) = made(Serials::default());
    let last = page.stripes.last().expect("the series credits people");
    page.focus = Focus::Stripe(last.0, last.1);
    assert_eq!(pressed(&mut page, &mut source, "down"), Focus::More(0));
    assert_eq!(pressed(&mut page, &mut source, "down"), Focus::Studio(0));
    assert_eq!(pressed(&mut page, &mut source, "up"), Focus::More(0));
    assert_eq!(
        pressed(&mut page, &mut source, "up"),
        Focus::Stripe(last.0, last.1)
    );
}

#[test]
fn down_from_the_last_row_reaches_the_strip_where_nobody_is_credited() {
    let (mut page, mut source) = page(Serials {
        made: true,
        ..Serials::default()
    });
    page.focus = Focus::Still(13);
    assert_eq!(pressed(&mut page, &mut source, "down"), Focus::More(0));
    assert_eq!(still(pressed(&mut page, &mut source, "up")), 13);
}

#[test]
fn the_strip_follows_the_franchise_strip_where_nobody_is_credited() {
    let (mut page, mut source) = page(Serials {
        made: true,
        franchise: true,
        ..Serials::default()
    });
    page.focus = Focus::Franchise(0, crate::screens::franchise::strips::Place::Member(0));
    assert_eq!(pressed(&mut page, &mut source, "down"), Focus::More(0));
    assert!(matches!(
        pressed(&mut page, &mut source, "up"),
        Focus::Franchise(0, _)
    ));
}

#[test]
fn right_reaches_see_all_and_a_select_there_opens_the_network_s_wall() {
    let (mut page, mut source) = made(Serials::default());
    page.focus = Focus::More(4);
    assert_eq!(pressed(&mut page, &mut source, "right"), Focus::More(5));
    assert_eq!(pressed(&mut page, &mut source, "right"), Focus::More(5));
    assert!(page.focused().is_none());
    let Step::Open(Screen::Wall(wall)) = page.key("enter", &mut source) else {
        panic!("see all opens the network's wall");
    };
    assert_eq!(wall.slots.query, Query::studio("A Network"));
}

#[test]
fn a_reread_holds_the_strip_and_returns_to_the_wall_where_it_left() {
    let (mut page, mut source) = made(Serials::default());
    page.focus = Focus::More(9);
    page.reread(&mut source);
    assert_eq!(page.focus, Focus::More(5));

    source.made = false;
    page.reread(&mut source);
    assert_eq!(page.focus, Focus::Still(0));
}

// The strip is the last row over the foot, so it pulls the foot into
// view, and the outline goes around its focused slot on the screen.
#[test]
fn the_outline_goes_around_the_focused_slot_of_the_strip() {
    let (mut page, _) = made(Serials::default());
    page.focus = Focus::More(5);
    let frame = crate::views::screen::FRAME;
    let placed = super::super::page::placed(&page, frame);
    let marked = page.marked(frame).expect("the page marks the slot");
    let name = super::super::page::studio_box(&page, &placed, 0).expect("one name");
    assert!(marked.y >= placed.region.y);
    assert!(marked.y + marked.height <= frame.height);
    assert!(marked.x + marked.width <= frame.width);
    assert!(name.y + name.height <= frame.height);
}
