// The movie page's scrolls under a press: the page down to the next
// block, a stripe across to the next headshot, and the outline's place at
// the end of both.

use super::*;
use crate::catalog::{CreditSlot, Credits};
use crate::sample::Catalog;
use crate::screens::stripes::Stripes;
use crate::views::people;

const FRAME: Rectangle = Rectangle {
    x: 0.0,
    y: 0.0,
    width: 1920.0,
    height: 1080.0,
};

// The sample's first film with a cast of twelve, so its one stripe, the
// last row of the page, is longer than the frame is wide, at rest with
// focus on this rung.
fn on(focus: Focus) -> Movie {
    let mut page =
        Movie::open("sample/features", "movie:sample:0001", &mut Catalog).expect("a sample film");
    let slot = |part: usize| CreditSlot {
        name: format!("Player {part}"),
        role: format!("Part {part}"),
        contributor: format!(".contributors/Player {part}"),
        headshot: true,
    };
    page.stripes = Stripes::of(Credits {
        cast: (1..=12).map(slot).collect(),
        ..Credits::default()
    });
    page.focus = focus;
    page.follow(FRAME, 0.0, None);
    page
}

// The rows of the page where every scroll comes to rest.
fn settled(page: &Movie) -> Vec<f32> {
    let (blocks, offset) = placed(page, FRAME);
    rows(page, &blocks, FRAME, offset, Some(page.focus))
        .iter()
        .map(Across::scrolled)
        .collect()
}

#[test]
fn a_press_down_eases_the_page_to_the_next_row() {
    let mut page = on(Focus::Strip(0));
    let (_, from) = page.drawn(FRAME, 0.0);
    page.key("down", &mut Catalog);
    let (_, to) = placed(&page, FRAME);
    assert!(to > from, "{from} {to}");

    assert_eq!(page.follow(FRAME, 1.0, Some(1.0)), 1.0 + look::SCROLL);

    let (_, half) = page.drawn(FRAME, 1.0 + look::SCROLL / 2.0);
    assert!(from < half && half < to, "{half}");
    assert_eq!(page.drawn(FRAME, 1.0 + look::SCROLL).1, to);
}

#[test]
fn a_press_across_the_cast_eases_the_stripe() {
    let mut page = on(Focus::Stripe(0, 5));
    let cast = settled(&page).len() - 1;
    let from = settled(&page)[cast];
    page.key("right", &mut Catalog);
    let to = settled(&page)[cast];
    assert!(to > from, "{from} {to}");

    assert_eq!(page.follow(FRAME, 1.0, Some(1.0)), 1.0 + look::SCROLL);

    let half = page.rows.at(cast, to, 1.0 + look::SCROLL / 2.0);
    assert!(from < half && half < to, "{half}");
    assert_eq!(page.rows.at(cast, to, 1.0 + look::SCROLL), to);
}

// The outline goes to the next headshot where the stripe's whole scroll
// lands, while the stripe is still on its way there.
#[test]
fn the_outline_goes_to_the_headshot_where_the_stripe_comes_to_rest() {
    let mut page = on(Focus::Stripe(0, 5));
    let cast = settled(&page).len() - 1;
    let from = (
        page.marked(FRAME).expect("a headshot"),
        settled(&page)[cast],
    );
    page.key("right", &mut Catalog);
    page.follow(FRAME, 1.0, Some(1.0));
    let to = (
        page.marked(FRAME).expect("a headshot"),
        settled(&page)[cast],
    );

    assert_ne!(page.rows.at(cast, to.1, 1.0 + look::SCROLL / 2.0), to.1);
    let moved = to.0.x - from.0.x;
    assert!(
        (moved - (people::pitch() - (to.1 - from.1))).abs() < 0.01,
        "{moved}"
    );
}

#[test]
fn the_outline_goes_around_the_focused_button_and_mark() {
    let mut page = on(Focus::Buttons(0));
    let button = page.marked(FRAME).expect("the row holds a button");
    page.key("down", &mut Catalog);
    let mark = page.marked(FRAME).expect("the line holds a mark");

    assert!(mark.y > button.y + button.height, "{button:?} {mark:?}");
}
