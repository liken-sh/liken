// Arrival on an episode: a home card that names one episode opens the
// series page with that episode's row already open, and back from that row
// leaves the page, because the person never saw the wall.

use super::super::*;
use super::serials::{SERIES, Serials, page};

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
