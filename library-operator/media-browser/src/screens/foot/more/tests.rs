// Which studio earns a title page's More from strip, what the strip
// holds, and what a select on it opens.

use super::*;
use crate::catalog::{Slot, Title};
use crate::sample::Catalog;
use crate::screens::Screen;

const FILMS: &str = "screening/films";

fn slot(id: &str, released: &str) -> Slot {
    Slot::of(
        FILMS,
        "movies",
        Title {
            id: id.into(),
            title: format!("Film {id}"),
            released: released.into(),
            ..Title::default()
        },
    )
}

// A studio of this many titles, newest first, "<prefix>0" the newest.
fn studio(name: &str, prefix: &str, count: usize) -> Studio {
    Studio {
        name: name.into(),
        titles: (0..count)
            .map(|number| slot(&format!("{prefix}{number}"), &format!("{}", 2020 - number)))
            .collect(),
    }
}

fn shown(ids: &[&str]) -> HashSet<Work> {
    ids.iter()
        .map(|id| (FILMS.to_string(), id.to_string()))
        .collect()
}

fn ids(more: &More) -> Vec<&str> {
    more.members.iter().map(|item| item.id.as_str()).collect()
}

#[test]
fn a_studio_earns_the_strip_at_the_home_page_s_floor() {
    for (count, drawn) in [(FLOOR - 1, false), (FLOOR, true), (FLOOR + 1, true)] {
        let more = More::of(vec![studio("Gullwing Pictures", "g", count)], &shown(&[]));
        assert_eq!(more.is_some(), drawn, "{count} titles");
    }
}

// The title the page is about and the members its set and franchise
// strips draw are already on the page, so they neither count toward the
// floor nor draw again.
#[test]
fn the_titles_the_page_shows_neither_count_nor_draw() {
    let gullwing = || studio("Gullwing Pictures", "g", FLOOR + 2);
    let more = More::of(vec![gullwing()], &shown(&["g0", "g3"])).expect("four remain");
    assert_eq!(ids(&more), ["g1", "g2", "g4", "g5"]);
    assert!(More::of(vec![gullwing()], &shown(&["g0", "g1", "g2"])).is_none());
}

#[test]
fn the_first_studio_the_body_names_that_earns_a_strip_takes_it() {
    let more = More::of(
        vec![
            studio("Lantern Works", "l", FLOOR - 1),
            studio("Gullwing Pictures", "g", FLOOR),
            studio("Tidewater", "t", FLOOR + 6),
        ],
        &shown(&[]),
    )
    .expect("a studio earns the strip");
    assert_eq!(more.studio, "Gullwing Pictures");
    assert_eq!(more.heading, "More from Gullwing Pictures");
}

#[test]
fn the_strip_draws_the_newest_titles_a_home_strip_shows_and_ends_in_see_all() {
    let more = More::of(vec![studio("Tidewater", "t", SHOWN + 10)], &shown(&[]))
        .expect("the studio earns the strip");
    assert_eq!(more.members.len(), SHOWN);
    assert_eq!(more.members[0].id, "t0");
    assert_eq!(more.count(), SHOWN + 1);
    assert_eq!(more.last().words, strip::SEE_ALL);
}

#[test]
fn the_production_line_names_every_studio_but_the_strip_s() {
    let names = ["Lantern Works".to_string(), "Gullwing Pictures".to_string()];
    let more = More::of(vec![studio("Gullwing Pictures", "g", FLOOR)], &shown(&[]));
    assert_eq!(named(&names, more.as_ref()), ["Lantern Works"]);
    assert_eq!(named(&names, None), names);
}

// The sample names "A Second Studio" on every movie, so a strip of it
// opens real pages and a real wall.
fn shared() -> More {
    let mut source = Catalog;
    let studios = source.studios(&["A Second Studio".to_string()]);
    More::of(studios, &shown(&[])).expect("every sample movie names the studio")
}

#[test]
fn a_select_on_a_title_opens_its_page_and_on_the_last_slot_the_studio_s_wall() {
    let more = shared();
    let mut source = Catalog;
    let Step::Open(Screen::Movie(page)) = more.select(0, &mut source) else {
        panic!("a title of the strip opens its page");
    };
    assert_eq!(page.id, more.members[0].id);
    let Step::Open(Screen::Wall(wall)) = more.select(more.members.len(), &mut source) else {
        panic!("the last slot opens the studio's wall");
    };
    assert_eq!(wall.slots.query, Query::studio(&more.studio));
    assert!(matches!(more.select(more.count(), &mut source), Step::Stay));
}
