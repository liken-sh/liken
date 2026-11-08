// The person's page over an invented catalog: the words it builds out of
// an entry, the credits it reads in release order, what the people at the
// screen have seen of them, and what a press on them does.

// Invented careers for the tests that need no catalog.
pub mod careers;
// The page in motion: the glides a press starts and where the outline
// goes.
mod motion;
// The invented catalog the page reads.
mod people;

use std::collections::HashMap;
use std::path::PathBuf;

use self::people::{LIBRARY, PATH, People, id};
use super::*;
use crate::art::Image;
use crate::screens::person::credit::Seen;
use crate::screens::person::walk::Rest;
use crate::screens::{Screen, Step};

// The year every test reads "now" at.
const NOW: i64 = 2026;

pub(super) fn opened(people: People) -> (Person, People) {
    let mut source = people;
    let page =
        Person::read(LIBRARY, PATH, NOW, &mut source).expect("the library holds this person");
    (page, source)
}

pub(super) fn credited(works: usize) -> (Person, People) {
    opened(People {
        works,
        born: "1956-01-02",
        ..People::default()
    })
}

// The ids of the page's credits, in the row's order.
fn ids(page: &Person) -> Vec<&str> {
    page.credits
        .iter()
        .map(|credit| credit.item.id.as_str())
        .collect()
}

#[test]
fn a_page_carries_the_name_the_facts_and_the_files() {
    let (page, _) = credited(3);
    assert_eq!(page.name, "A Player");
    assert_eq!(page.dates, "1956 to 2019");
    assert_eq!(page.facts, "1956 to 2019 · 3 credits from 1997 to 1999");
    assert_eq!(page.headshot, ".contributors/A Player/headshot.jpg");
    assert_eq!(page.headshot_library, LIBRARY);
    assert_eq!(page.biography_path, "");
}

#[test]
fn a_person_the_library_does_not_hold_has_no_page() {
    let mut source = People::default();
    assert!(Person::open(LIBRARY, ".contributors/Nobody", &mut source).is_none());
}

#[test]
fn the_dates_read_as_the_entry_holds_them() {
    assert_eq!(dates("1956-01-02", "2019-11-30"), "1956 to 2019");
    assert_eq!(dates("1956", ""), "born 1956");
    assert_eq!(dates("", "2019"), "died 2019");
    assert_eq!(dates("", ""), "");
}

#[test]
fn the_credits_run_oldest_first_with_the_undated_last() {
    let (page, _) = opened(People {
        works: 3,
        undated: true,
        ..People::default()
    });
    assert_eq!(
        ids(&page),
        ["title:3", "title:2", "title:1", "title:undated"]
    );
    assert_eq!(page.credits[0].year, Some(1997));
    assert_eq!(page.credits[3].year, None);
    assert_eq!(
        page.facts,
        "died 2019 · 3 credits from 1997 to 1999, and 1 undated"
    );
}

#[test]
fn a_card_names_the_part_and_the_age_at_the_release() {
    let (page, _) = credited(3);
    let card = &page.credits[2].item;
    assert_eq!(card.caption, "Director, The Part");
    assert_eq!(card.under, "1999 · age 43");
    assert_eq!(card.name, "Title 1");
    assert!(!card.tagline);

    let (acting, _) = opened(People {
        works: 1,
        acting: true,
        ..People::default()
    });
    assert_eq!(acting.credits[0].item.caption, "The Part");
    assert_eq!(acting.credits[0].item.under, "1999");
}

#[test]
fn a_birth_after_a_credit_takes_every_age_off_the_cards() {
    let (page, _) = opened(People {
        works: 3,
        born: "1998-01-01",
        ..People::default()
    });
    let lines: Vec<&str> = page
        .credits
        .iter()
        .map(|credit| credit.item.under.as_str())
        .collect();
    assert_eq!(lines, ["1997", "1998", "1999"]);
}

#[test]
fn a_short_career_draws_no_axis_and_a_longer_one_does() {
    assert!(credited(4).0.axis.is_none());
    let (page, _) = credited(6);
    let axis = page.axis.expect("six credits over five years");
    assert_eq!((axis.start, axis.end), (1991, 2000));
}

#[test]
fn the_arrows_move_along_the_row_of_a_page_with_no_axis() {
    let (mut page, mut source) = credited(3);
    assert_eq!(page.walk.focus, Focus::Poster(2));
    assert!(matches!(page.key("up", &mut source), Step::Still));
    assert!(matches!(page.key("right", &mut source), Step::Still));
    assert!(matches!(page.key("left", &mut source), Step::Stay));
    assert_eq!(page.walk.focus, Focus::Poster(1));
}

#[test]
fn up_from_a_poster_reaches_the_axis_and_up_again_the_strip() {
    let (mut page, mut source) = credited(6);
    assert!(matches!(page.key("up", &mut source), Step::Stay));
    assert_eq!(page.walk.focus, Focus::Axis(5));
    assert!(matches!(page.key("up", &mut source), Step::Still));
}

#[test]
fn a_select_opens_the_movie_or_the_series_the_credit_names() {
    let (mut page, mut source) = credited(3);
    let Step::Open(Screen::Movie(opened)) = page.key("enter", &mut source) else {
        panic!("a select on a movie opens its page");
    };
    assert_eq!(opened.id, "title:1");

    page.walk.focus = Focus::Poster(0);
    let Step::Open(Screen::Series(opened)) = page.key("enter", &mut source) else {
        panic!("a select on a series opens its page");
    };
    assert_eq!(opened.id, "title:3");
}

#[test]
fn a_page_with_no_works_opens_nothing() {
    let (mut page, mut source) = credited(0);
    assert!(page.credits.is_empty());
    assert!(matches!(page.key("enter", &mut source), Step::Still));
    assert!(matches!(page.key("right", &mut source), Step::Still));
    assert_eq!(page.resting(&mut source), None);
}

#[test]
fn a_page_opened_from_a_title_lands_on_its_credit() {
    let mut source = People {
        works: 6,
        ..People::default()
    };
    let via = (LIBRARY.to_string(), id(4));
    let page = Person::open_from(LIBRARY, PATH, Some(&via), &mut source).expect("a page");
    assert_eq!(page.walk.focus, Focus::Poster(2));
    assert_eq!(page.walk.rest, Rest::Centered(2));
}

#[test]
fn the_focused_work_names_the_backdrop_the_store_decodes_next() {
    let (page, mut source) = credited(3);
    assert_eq!(
        page.resting(&mut source),
        Some((LIBRARY.to_string(), "title:1.backdrop.jpg".to_string()))
    );
}

#[test]
fn a_reread_that_shortens_the_row_clamps_the_focus() {
    let (mut page, mut source) = credited(3);
    source.works = 1;
    page.reread(&mut source);
    assert_eq!(ids(&page), ["title:1"]);
    assert_eq!(page.walk.focus, Focus::Poster(0));
}

// A work the scanner adds ahead of the focused one moves that work one
// place along, and focus goes with the work, not with the place.
#[test]
fn a_reread_that_adds_an_earlier_credit_keeps_focus_on_the_same_work() {
    let (mut page, mut source) = credited(3);
    page.walk.focus = Focus::Poster(1);
    source.older = true;
    page.reread(&mut source);
    assert_eq!(ids(&page)[0], "title:older");
    assert_eq!(page.walk.focus, Focus::Poster(2));
}

#[test]
fn a_reread_of_a_person_that_left_the_store_keeps_the_page() {
    let (mut page, mut source) = credited(3);
    source.gone = true;
    page.reread(&mut source);
    assert_eq!(page.credits.len(), 3);
}

fn audience(names: &[&str]) -> Vec<String> {
    names.iter().map(|name| name.to_string()).collect()
}

// Title 1 is a film both people finished, title 2 a film one of them
// finished, and title 3 the series, whose every episode both finished.
#[test]
fn each_credit_is_seen_by_nobody_by_some_or_by_everyone() {
    let (mut page, mut source) = opened(People {
        works: 3,
        finished: vec![
            ("first", 1),
            ("second", 1),
            ("first", 2),
            ("first", 3),
            ("second", 3),
        ],
        ..People::default()
    });
    page.read_progress(&mut source, &audience(&["first", "second"]), &[]);
    let seen: Vec<Seen> = page.credits.iter().map(|credit| credit.seen).collect();
    assert_eq!(seen, [Seen::Everyone, Seen::Some, Seen::Everyone]);
    assert_eq!(
        page.facts,
        "died 2019 · 3 credits from 1997 to 1999 · you've seen 2"
    );
    assert_eq!(page.credits[2].item.reached.share, Some(1.0));
    assert_eq!(page.credits[0].item.reached.share, None);

    page.read_progress(&mut source, &[], &[]);
    let seen: Vec<Seen> = page.credits.iter().map(|credit| credit.seen).collect();
    assert_eq!(seen, [Seen::Nobody; 3]);
    assert_eq!(page.facts, "died 2019 · 3 credits from 1997 to 1999");
}

#[test]
fn the_page_draws_the_progress_of_every_work_it_credits() {
    let (page, _) = credited(3);
    assert!(page.draws_progress_of(LIBRARY, "title:3"));
    assert!(page.draws_progress_of(LIBRARY, "title:1"));
    assert!(!page.draws_progress_of(LIBRARY, "title:9"));
}

// A store over one volume, so a test reads the biography the way
// the browser does.
#[derive(Default)]
struct Volume {
    roots: HashMap<String, PathBuf>,
}

impl Art for Volume {
    fn covered(&mut self, _library: &str, _art: &str, _width: u32, _height: u32) -> Option<Image> {
        None
    }

    fn file(&self, library: &str, path: &str) -> Option<PathBuf> {
        if path.contains("..") {
            return None;
        }
        Some(self.roots.get(library)?.join(path))
    }
}

#[test]
fn the_biography_arrives_from_the_file_beside_the_entry() {
    let dir = tempfile::TempDir::new().expect("a scratch volume");
    let entry = dir.path().join(PATH);
    std::fs::create_dir_all(&entry).expect("the entry's directory");
    std::fs::write(entry.join("biography.txt"), "One line of a life.").expect("the file");

    let (mut page, _) = opened(People {
        biography: true,
        works: 1,
        ..People::default()
    });
    assert_eq!(page.biography_path, ".contributors/A Player/biography.txt");

    let store = Volume {
        roots: HashMap::from([(LIBRARY.to_string(), dir.path().to_path_buf())]),
    };
    page.read_biography(&store);
    assert_eq!(page.biography, "One line of a life.");
}

#[test]
fn a_person_with_no_biography_file_reads_nothing() {
    let (mut page, _) = credited(1);
    page.biography = "stale".into();
    page.read_biography(&Volume::default());
    assert_eq!(page.biography, "");
}
