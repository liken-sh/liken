// The bars a wall's film slots carry: the library wall, the wall a search
// answers, and the re-read that follows a new answer to who is watching.

use super::*;
use crate::audience::IDLE_SECONDS;

// The library and the film the fixture's audience is in the middle of,
// the second entry of the films library.
const FILMS: &str = "screening/films";
const HALFWAY: &str = "movies:2";

// The browser over this `Person` list, with the audience answered and one
// film of the library half watched.
fn halfway(known: &[&str], preset: &[&str]) -> Browser<Fake, NoArt> {
    let mut browser = browser(3);
    browser.source.reached = HashMap::from([(
        (FILMS.to_string(), HALFWAY.to_string()),
        Progress {
            play: "one".into(),
            position: 2_700,
            duration: 5_400,
            ..Progress::default()
        },
    )]);
    let people = known
        .iter()
        .map(|name| crate::audience::Person {
            name: (*name).to_string(),
            display_name: (*name).to_string(),
        })
        .collect();
    let preset = preset.iter().map(|name| (*name).to_string()).collect();
    browser.with_audience(people, preset)
}

// The id of every slot of the shown wall that carries a bar.
fn barred(browser: &Browser<Fake, NoArt>) -> Vec<String> {
    showing_wall(browser)
        .slots
        .items
        .iter()
        .filter(|item| item.progress.is_some())
        .map(|item| item.id.clone())
        .collect()
}

// The text the shown search wall was read over.
fn typed_query(browser: &Browser<Fake, NoArt>) -> String {
    match &showing_wall(browser).slots.query {
        Query::Search { text } => text.clone(),
        query => panic!("the browser is not showing a search wall: {query:?}"),
    }
}

#[test]
fn the_film_the_audience_is_in_the_middle_of_carries_a_bar_on_the_library_wall() {
    let mut browser = halfway(&["first"], &["first"]);

    browser.key("enter");

    assert_eq!(barred(&browser), [HALFWAY]);
    assert_eq!(
        showing_wall(&browser).slots.items[1].progress,
        Some(Played {
            position: 2_700,
            duration: 5_400
        })
    );
    assert_eq!(browser.source.watching, ["first".to_string()]);
}

#[test]
fn a_result_of_a_search_carries_the_bar_the_library_wall_carries() {
    let mut browser = halfway(&["first"], &["first"]);

    browser.key("e");

    assert_eq!(typed_query(&browser), "e");
    assert_eq!(barred(&browser), [HALFWAY]);
}

#[test]
fn a_letter_typed_into_the_search_field_keeps_the_bars() {
    let mut browser = halfway(&["first"], &["first"]);
    browser.key("e");

    browser.key("n");

    assert_eq!(typed_query(&browser), "en");
    assert_eq!(barred(&browser), [HALFWAY]);
}

#[test]
fn a_wall_read_again_after_a_change_keeps_the_bars() {
    let mut browser = halfway(&["first"], &["first"]);
    browser.key("enter");

    browser.source.changed = true;
    assert!(browser.pump(1.0));

    assert_eq!(barred(&browser), [HALFWAY]);
}

#[test]
fn a_wall_reads_its_bars_again_for_the_people_a_new_answer_names() {
    let mut browser = halfway(&["first", "second"], &[]);
    browser.tick(0.0);
    browser.key("escape");
    browser.key("enter");
    assert!(browser.source.watching.is_empty());

    // The answer lapses, the picker returns, and the first person is
    // chosen this time.
    browser.tick(IDLE_SECONDS + 1.0);
    browser.key("enter");
    browser.key("escape");

    assert_eq!(browser.source.watching, ["first".to_string()]);
    assert_eq!(barred(&browser), [HALFWAY]);
}

#[test]
fn a_wall_of_series_carries_no_bar() {
    let mut browser = halfway(&["first"], &["first"]);
    browser.source.reached = HashMap::from([(
        (SERIALS.to_string(), SERIAL.to_string()),
        Progress {
            play: "one".into(),
            position: 300,
            duration: 1_320,
            ..Progress::default()
        },
    )]);

    browser.key("right");
    browser.key("enter");

    assert_eq!(showing_wall(&browser).slots.name, "serials");
    assert_eq!(barred(&browser), Vec::<String>::new());
}
