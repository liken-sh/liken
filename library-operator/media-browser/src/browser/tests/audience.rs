// The people a play request is recorded against: the answer the run opened
// with, the presses that hold it open, and the hours that end it.

use std::collections::BTreeMap;

use super::*;
use crate::audience::IDLE_SECONDS;
use crate::views::clock::strip::Target;

// The browser on a bus, over this `Person` list, with the audience already
// answered and one film to play.
fn watching(known: &[&str], preset: &[&str]) -> (Browser<Fake, NoArt>, FakeBus) {
    let (browser, bus) = playing(vec![one_item()]);
    let people = known
        .iter()
        .map(|name| crate::audience::Person {
            name: (*name).to_string(),
            display_name: (*name).to_string(),
        })
        .collect();
    let preset = preset.iter().map(|name| (*name).to_string()).collect();

    (browser.with_audience(people, preset), bus)
}

// The walk from the home page to a film's page: the wall, then the film. A
// third press plays it.
fn open_the_film(browser: &mut Browser<Fake, NoArt>) {
    browser.key("enter");
    browser.key("enter");
}

#[test]
fn a_request_names_the_audience_and_the_work() {
    let (mut browser, bus) = watching(&["first", "second"], &["first"]);
    browser.source.identity = Identity {
        aliases: BTreeMap::from([
            ("tmdb".to_string(), "603".to_string()),
            ("path".to_string(), "some-film-1999".to_string()),
        ]),
        season: 2,
        episode: 5,
    };
    open_the_film(&mut browser);

    browser.key("enter");

    let request = published(&bus);
    assert_eq!(request["people"], serde_json::json!(["first"]));
    assert_eq!(
        request["aliases"],
        serde_json::json!({"tmdb": "603", "path": "some-film-1999"})
    );
    assert_eq!(request["season"], 2);
    assert_eq!(request["episode"], 5);
}

#[test]
fn a_request_after_the_idle_hours_names_nobody() {
    let (mut browser, bus) = watching(&["first"], &["first"]);
    open_the_film(&mut browser);
    browser.tick(IDLE_SECONDS + 1.0);
    // The lapse raises the picker, and back answers that nobody is
    // watching.
    browser.key("escape");

    browser.key("enter");

    assert_eq!(published(&bus).get("people"), None);
}

#[test]
fn a_press_inside_the_hours_keeps_the_audience() {
    let (mut browser, bus) = watching(&["first"], &["first"]);
    open_the_film(&mut browser);
    browser.tick(IDLE_SECONDS - 1.0);
    browser.key("left");
    browser.tick(2.0 * IDLE_SECONDS - 2.0);

    browser.key("enter");

    assert_eq!(published(&bus)["people"], serde_json::json!(["first"]));
}

#[test]
fn a_preset_name_the_person_list_does_not_hold_is_dropped() {
    let (mut browser, bus) = watching(&["first"], &["third"]);
    open_the_film(&mut browser);

    browser.key("enter");

    assert_eq!(published(&bus).get("people"), None);
    assert!(!browser.audience().needs_answer(0.0));
}

#[test]
fn a_browser_that_knows_no_people_asks_nobody() {
    let (browser, _bus) = watching(&[], &[]);

    assert!(browser.audience().known().is_empty());
    assert!(!browser.audience().needs_answer(0.0));
}

#[test]
fn the_first_frame_asks_who_is_watching() {
    let (mut browser, _bus) = watching(&["first", "second", "third"], &[]);

    browser.tick(0.0);

    assert!(browser.picker.is_some());
}

// The list on disk is read again each time the picker opens, so a
// `Person` added after the run started is offered, and a file that went
// bad leaves the list the browser holds.
#[test]
fn the_picker_reads_the_person_list_again_when_it_opens() {
    let dir = tempfile::TempDir::new().unwrap();
    let path = dir.path().join("people.json");
    let (browser, _bus) = watching(&["first"], &[]);
    let mut browser = browser.with_people_file(Some(path.clone()));

    std::fs::write(
        &path,
        r#"[{"name":"first","displayName":"First"},{"name":"second","displayName":"Second"}]"#,
    )
    .unwrap();
    browser.tick(0.0);

    assert!(browser.picker.is_some());
    assert_eq!(browser.audience().known().len(), 2);

    std::fs::write(&path, "not json").unwrap();
    browser.key("escape");
    browser.key("up");
    browser.key("left");
    browser.key("enter");

    assert_eq!(browser.audience().known().len(), 2);
}

#[test]
fn a_run_that_names_the_audience_asks_nobody() {
    let (mut browser, _bus) = watching(&["first", "second"], &["first"]);

    browser.tick(0.0);

    assert!(browser.picker.is_none());
}

#[test]
fn a_walk_across_the_tiles_answers_the_person_it_stopped_on() {
    let (mut browser, _bus) = watching(&["first", "second", "third"], &[]);
    browser.tick(0.0);

    browser.key("right");
    browser.key("right");
    browser.key("enter");
    browser.key("escape");

    assert!(browser.picker.is_none());
    assert_eq!(browser.audience().current(0.0), ["third".to_string()]);
    browser.pump(1.0);
    assert_eq!(browser.source.watching, ["third".to_string()]);
}

#[test]
fn a_press_on_the_link_with_nobody_chosen_answers_the_empty_set() {
    let (mut browser, _bus) = watching(&["first", "second"], &[]);
    browser.tick(0.0);

    browser.key("down");
    browser.key("enter");

    assert!(browser.picker.is_none());
    assert!(browser.audience().current(0.0).is_empty());
    assert!(!browser.audience().needs_answer(0.0));
}

#[test]
fn a_press_on_the_link_answers_the_people_the_row_chose() {
    let (mut browser, _bus) = watching(&["first", "second", "third"], &[]);
    browser.tick(0.0);

    browser.key("enter");
    browser.key("right");
    browser.key("right");
    browser.key("enter");
    browser.key("down");
    browser.key("enter");

    assert!(browser.picker.is_none());
    assert_eq!(
        browser.audience().current(0.0),
        ["first".to_string(), "third".to_string()]
    );
    browser.pump(1.0);
    assert_eq!(
        browser.source.watching,
        ["first".to_string(), "third".to_string()]
    );
}

#[test]
fn a_press_after_the_idle_hours_asks_again_and_moves_no_focus_under_it() {
    let (mut browser, _bus) = watching(&["first", "second"], &["first"]);
    browser.source.trailers = true;
    open_the_film(&mut browser);
    browser.tick(IDLE_SECONDS + 1.0);

    browser.key("right");

    assert!(browser.picker.is_some());
    assert_eq!(showing_page(&browser).focus, Focus::Buttons(0));
}

// The browser over two people whose display names differ from the names
// the records key on, both of them in the room.
fn a_room_of_two() -> Browser<Fake, NoArt> {
    let (browser, _bus) = playing(vec![one_item()]);
    browser.with_audience(
        vec![
            crate::audience::Person {
                name: "first".into(),
                display_name: "Coral".into(),
            },
            crate::audience::Person {
                name: "second".into(),
                display_name: "Kestrel".into(),
            },
        ],
        vec!["first".into(), "second".into()],
    )
}

#[test]
fn the_strip_carries_one_letter_for_each_person_in_the_room() {
    let mut browser = a_room_of_two();

    browser.tick(0.0);

    assert_eq!(
        browser.strip().expect("the strip draws").letters,
        ["C".to_string(), "K".to_string()]
    );
}

#[test]
fn the_strip_carries_no_letters_once_the_answer_has_lapsed() {
    let (mut browser, _bus) = watching(&["first"], &["first"]);

    browser.tick(IDLE_SECONDS + 1.0);

    assert!(browser.strip().expect("the strip draws").letters.is_empty());
}

// The walk from the home page up to the strip, which is where the glass
// and the circles of the room stand.
fn reach_the_strip(browser: &mut Browser<Fake, NoArt>) {
    for _ in 0..4 {
        browser.key("up");
    }
    assert!(browser.on_strip, "the strip holds focus");
}

#[test]
fn left_from_the_glass_reaches_the_circles_and_right_returns() {
    let mut browser = a_room_of_two();
    browser.tick(0.0);
    reach_the_strip(&mut browser);

    assert!(browser.key("left"));
    assert_eq!(
        browser.strip().expect("the strip draws").focus,
        Some(Target::Circles)
    );

    assert!(browser.key("right"));
    assert_eq!(
        browser.strip().expect("the strip draws").focus,
        Some(Target::Glass)
    );
}

#[test]
fn left_from_the_glass_moves_nothing_where_nobody_is_watching() {
    let (mut browser, _bus) = watching(&["first"], &[]);
    browser.tick(0.0);
    // The first frame asks who is watching, and the link answers nobody.
    browser.key("down");
    browser.key("enter");
    reach_the_strip(&mut browser);

    assert!(!browser.key("left"));
    assert_eq!(
        browser.strip().expect("the strip draws").focus,
        Some(Target::Glass)
    );
}

#[test]
fn enter_on_the_circles_asks_again_with_the_room_chosen() {
    let mut browser = a_room_of_two();
    browser.tick(0.0);
    reach_the_strip(&mut browser);
    browser.key("left");

    assert!(browser.key("enter"));

    let picker = browser.picker.as_ref().expect("the picker stands");
    assert_eq!(picker.tiles(), 2);
    assert!(picker.holds(0));
    assert!(picker.holds(1));
}

#[test]
fn an_answer_from_the_re_opened_picker_reads_the_screens_again() {
    let mut browser = a_room_of_two();
    browser.tick(0.0);
    reach_the_strip(&mut browser);
    browser.key("left");
    browser.key("enter");

    // The second tile drops out of the room, and the link answers.
    browser.key("right");
    browser.key("enter");
    browser.key("down");
    browser.key("enter");

    assert!(browser.picker.is_none());
    assert_eq!(browser.audience().current(0.0), ["first".to_string()]);
    browser.pump(1.0);
    assert_eq!(browser.source.watching, ["first".to_string()]);
}

// The people key raises the picker over the screen a person is on, and
// the answer uncovers that same screen, because the picker is a layer
// over the stack and pops nothing off it.

#[test]
fn the_people_key_raises_the_picker_over_the_page() {
    let mut browser = a_room_of_two();
    browser.tick(0.0);
    open_the_film(&mut browser);
    assert!(browser.picker.is_none());

    assert!(browser.key("people"));

    let picker = browser.picker.as_ref().expect("the picker stands");
    assert_eq!(picker.tiles(), 2);
    assert!(picker.holds(0));
    assert!(picker.holds(1));
    assert_eq!(browser.stack.len(), 2);
}

#[test]
fn the_people_key_under_the_picker_leaves_it_as_it_stands() {
    let mut browser = a_room_of_two();
    browser.tick(0.0);
    open_the_film(&mut browser);
    browser.key("people");
    browser.key("right");
    let raised = browser.picker.clone().expect("the picker stands");

    browser.key("people");

    assert_eq!(browser.picker, Some(raised));
    assert_eq!(browser.stack.len(), 2);
}

#[test]
fn an_answer_to_the_people_key_returns_to_the_page_it_was_raised_over() {
    let mut browser = a_room_of_two();
    browser.tick(0.0);
    open_the_film(&mut browser);
    browser.key("people");

    browser.key("right");
    browser.key("enter");
    browser.key("escape");

    assert!(browser.picker.is_none());
    assert_eq!(browser.audience().current(0.0), ["first".to_string()]);
    assert_eq!(browser.stack.len(), 2);
    assert_eq!(showing_page(&browser).focus, Focus::Buttons(0));
}

#[test]
fn the_people_key_raises_no_picker_where_the_browser_knows_no_people() {
    let (mut browser, _bus) = watching(&[], &[]);
    browser.tick(0.0);

    assert!(!browser.key("people"));

    assert!(browser.picker.is_none());
}
