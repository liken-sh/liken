// Arrival on an episode: a home card that names one episode opens the
// series page with that episode's row already open, and back from that row
// leaves the page, because the person never saw the wall.

use super::super::layout::{self, Layout};
use super::super::*;
use super::serials::{SERIES, Serials, page};
use crate::views::wall;

// The page a home card for the third episode of the first season opens,
// after the arrival.
fn arrived() -> (Series, Serials) {
    let mut source = Serials::default();
    let mut page = Series::open_at("screening/serials", SERIES, (1, 3), &mut source)
        .expect("the catalog holds it");
    page.arrive();
    (page, source)
}

#[test]
fn an_arrival_on_an_episode_opens_its_row_with_focus_on_the_first_button() {
    let (page, _) = arrived();

    assert_eq!(page.focus, Focus::Episode(2, 0));
    assert_eq!(page.opened().map(|still| still.episode), Some(3));
}

#[test]
fn an_arrival_on_a_page_that_names_no_episode_stays_on_the_wall() {
    let (mut page, _) = page(Serials::default());

    page.arrive();

    assert_eq!(page.focus, Focus::Still(0));
}

#[test]
fn back_from_the_row_of_an_arrival_leaves_the_page() {
    let cases: [(&str, &[&str]); 3] = [
        ("at once", &[]),
        ("from a button further along", &["right"]),
        ("from the marks and up again", &["down", "up"]),
    ];
    for (name, keys) in cases {
        let (mut page, mut source) = arrived();
        for key in keys {
            page.key(key, &mut source);
        }

        assert!(page.escape().is_none(), "{name}");
    }
}

#[test]
fn a_row_opened_again_from_the_wall_closes_on_back() {
    let (mut page, mut source) = arrived();
    page.key("down", &mut source);
    page.key("down", &mut source);
    assert_eq!(page.focus, Focus::Still(2));

    page.key("enter", &mut source);

    assert!(matches!(page.escape(), Some(Step::Stay)));
    assert_eq!(page.focus, Focus::Still(2));
}

#[test]
fn a_reread_keeps_the_arrival() {
    let (mut page, mut source) = arrived();

    page.reread(&mut source);

    assert_eq!(page.focus, Focus::Episode(2, 0));
    assert!(page.escape().is_none());
}

// Five seasons of eight, which fill two rows each. The third episode of
// the fourth season is the page's fifteenth row and 27th still.
fn deep() -> Serials {
    Serials {
        seasons: vec![8; 5],
        ..Serials::default()
    }
}

// How far the wall scrolls at a frame of 1920 by 1080, computed as the
// page computes it when it draws.
fn scrolled(page: &Series) -> f32 {
    let bounds = crate::views::area(0.0, 0.0, 1920.0, 1080.0);
    let region = layout::region(bounds);
    let cells = wall::lined(
        region.width,
        wall::STILL,
        COLUMNS,
        crate::views::card::LINES,
    );
    Layout::of(&page.seasons, cells, 0, 0, 0.0).scroll(
        seasons::standing(page),
        &page.seasons,
        region.height,
    )
}

fn arrived_deep() -> (Series, Serials) {
    let mut source = deep();
    let mut page = Series::open_at("screening/serials", SERIES, (4, 3), &mut source)
        .expect("the catalog holds it");
    page.arrive();
    (page, source)
}

#[test]
fn an_arrival_deep_in_the_series_scrolls_the_wall_to_its_still() {
    let (page, _) = arrived_deep();
    let top = page_at_top();

    assert_eq!(page.focus, Focus::Episode(26, 0));
    assert!(scrolled(&page) > scrolled(&top));
}

#[test]
fn down_from_the_row_of_an_arrival_lands_on_its_still() {
    let (mut page, mut source) = arrived_deep();

    page.key("down", &mut source);
    page.key("down", &mut source);

    assert_eq!(page.focus, Focus::Still(26));
}

#[test]
fn left_from_the_button_row_after_an_arrival_returns_to_its_still() {
    let mut source = Serials {
        trailer: true,
        ..deep()
    };
    let mut page = Series::open_at("screening/serials", SERIES, (4, 3), &mut source)
        .expect("the catalog holds it");
    page.arrive();
    page.focus = Focus::Buttons(0);

    page.key("left", &mut source);

    assert_eq!(page.focus, Focus::Still(26));
}

fn page_at_top() -> Series {
    let mut source = deep();
    Series::open("screening/serials", SERIES, &mut source).expect("the catalog holds it")
}

#[test]
fn left_from_the_button_row_after_a_reread_returns_to_the_arrival_still() {
    let mut source = Serials {
        trailer: true,
        ..deep()
    };
    let mut page = Series::open_at("screening/serials", SERIES, (4, 3), &mut source)
        .expect("the catalog holds it");
    page.arrive();
    page.reread(&mut source);
    page.focus = Focus::Buttons(0);

    page.key("left", &mut source);

    assert_eq!(page.focus, Focus::Still(26));
}
