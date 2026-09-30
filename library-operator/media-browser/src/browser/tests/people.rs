// The `Person` list as it changes under a running browser: the picker that
// stands over a new list, the faces cut again for a changed picture, the
// letters of a renamed person, and the file the watch reads, with the
// kubelet's swap and a file that does not parse.

use std::sync::mpsc::{Receiver, channel};
use std::time::Duration;

use super::*;
use crate::audience::Person;
use crate::audience::faces::Face;
use crate::audience::thumbnail::solid;
use crate::audience::watch::mount::{mounted, swap};
use crate::screens::audience::Focus;

// A person whose display name is their name in capitals, with this
// picture or none.
fn person(name: &str, colour: Option<[u8; 3]>) -> Person {
    Person {
        name: name.to_string(),
        display_name: name.to_uppercase(),
        thumbnail: colour.map(|colour| solid(32, colour)),
    }
}

// A list of people with no pictures.
fn list(names: &[&str]) -> Vec<Person> {
    names.iter().map(|name| person(name, None)).collect()
}

// The browser over these people after the first frame asked who is
// watching, so the picker stands.
fn asking(people: Vec<Person>) -> Browser<Fake, NoArt> {
    let (browser, _bus) = on_bus(3, Vec::new());
    let mut browser = browser.with_audience(people, Vec::new());
    browser.tick(0.0);
    browser
}

// The `Person` names the picker's tiles draw, in order.
fn tiles(browser: &Browser<Fake, NoArt>) -> Vec<String> {
    let layer = browser.picker_layer().expect("the picker is up");
    (0..layer.picker.tiles())
        .map(|index| layer.people[index].name.clone())
        .collect()
}

// The `Person` names the picker holds chosen.
fn chosen(browser: &Browser<Fake, NoArt>) -> Vec<String> {
    let picker = browser.picker.as_ref().expect("the picker is up");
    let known = browser.audience().known();
    (0..picker.tiles())
        .filter(|index| picker.holds(*index))
        .map(|index| known[index].name.clone())
        .collect()
}

#[test]
fn a_new_list_under_the_picker_redraws_its_tiles_and_keeps_the_choices_by_name() {
    let mut browser = asking(list(&["person-a", "person-b", "person-c"]));
    browser.key("right");
    browser.key("enter");

    browser.relist(list(&["person-c", "person-d", "person-b"]));

    assert_eq!(tiles(&browser), ["person-c", "person-d", "person-b"]);
    assert_eq!(chosen(&browser), ["person-b"]);
    assert_eq!(
        browser.picker.as_ref().unwrap().focus(),
        Focus::Tile(2),
        "focus follows person-b to the end of the row"
    );
}

#[test]
fn a_chosen_person_the_new_list_dropped_drops_out_of_the_picker() {
    let mut browser = asking(list(&["person-a", "person-b"]));
    browser.key("enter");
    browser.key("right");
    browser.key("enter");

    browser.relist(list(&["person-b"]));

    assert_eq!(tiles(&browser), ["person-b"]);
    assert_eq!(chosen(&browser), ["person-b"]);
}

// A person the picker answers after the list changed is the person the
// tile now draws, so the answer carries the new list's names.
#[test]
fn an_answer_after_a_new_list_names_the_people_of_the_new_list() {
    let mut browser = asking(list(&["person-a", "person-b"]));
    browser.key("enter");

    browser.relist(list(&["person-c", "person-a"]));
    browser.key("escape");

    assert_eq!(browser.audience().current(0.0), ["person-a".to_string()]);
}

#[test]
fn a_new_picture_cuts_the_face_of_that_person_alone() {
    let mut browser = asking(vec![
        person("person-a", Some([200, 40, 40])),
        person("person-b", Some([40, 40, 200])),
    ]);
    let face = |browser: &Browser<Fake, NoArt>, index| match browser
        .picker_layer()
        .expect("the picker is up")
        .face(index)
    {
        Face::Picture(image) => image.clone(),
        Face::Letter(letter) => panic!("the tile draws the letter {letter}"),
    };
    let (a, b) = (face(&browser, 0), face(&browser, 1));

    browser.relist(vec![
        person("person-a", Some([200, 40, 40])),
        person("person-b", Some([40, 200, 40])),
    ]);

    assert_eq!(
        face(&browser, 0),
        a,
        "the unchanged face is the same decode"
    );
    assert_ne!(face(&browser, 1), b);
}

#[test]
fn a_new_display_name_draws_on_the_tile() {
    let mut browser = asking(list(&["person-a"]));

    browser.relist(vec![Person {
        name: "person-a".into(),
        display_name: "Zed".into(),
        thumbnail: None,
    }]);

    let layer = browser.picker_layer().expect("the picker is up");
    assert_eq!(layer.face(0), Face::Letter("Z".into()));
}

#[test]
fn a_list_with_nobody_in_it_closes_the_picker() {
    let mut browser = asking(list(&["person-a"]));

    browser.relist(Vec::new());

    assert!(browser.picker.is_none());
}

// A browser that knew nobody asks once the list names somebody.
#[test]
fn a_first_person_raises_the_picker() {
    let mut browser = asking(Vec::new());
    assert!(browser.picker.is_none());

    browser.relist(list(&["person-a"]));
    browser.pump(1.0);

    assert_eq!(tiles(&browser), ["person-a"]);
}

#[test]
fn an_equal_list_changes_nothing() {
    let mut browser = asking(list(&["person-a"]));

    assert!(!browser.relist(list(&["person-a"])));
}

// A new list changes no answer: a person the list dropped stays in the
// room until the next answer is taken.
#[test]
fn a_new_list_keeps_the_answer_that_stands() {
    let (browser, _bus) = on_bus(3, Vec::new());
    let mut browser = browser.with_audience(
        list(&["person-a", "person-b"]),
        vec!["person-a".into(), "person-b".into()],
    );

    browser.relist(list(&["person-b"]));

    assert_eq!(
        browser.audience().current(0.0),
        ["person-a".to_string(), "person-b".to_string()]
    );
}

// The strip, the continue-watching row, and the message on the bus all
// carry the display name, so a rename in the room reaches all three.
#[test]
fn a_renamed_person_in_the_room_draws_their_new_letter_everywhere() {
    let (mut browser, bus) = super::resume::watched(super::resume::plays(), false);

    browser.relist(vec![Person {
        name: "first".into(),
        display_name: "Zed".into(),
        thumbnail: None,
    }]);
    browser.pump(1.0);

    let strip = browser.strip().expect("the strip draws");
    assert_eq!(letters(&strip.viewers), ["Z"]);
    assert_eq!(letters(&super::resume::row(&browser).viewers), ["Z"]);
    let held = bus
        .published
        .lock()
        .unwrap()
        .iter()
        .rev()
        .find(|(topic, _, _)| topic == AUDIENCE_TOPIC)
        .and_then(|(_, payload, _)| crate::bus::audience::held(payload))
        .expect("the room went out again");
    assert_eq!(held.people[0].display_name, "Zed");
}

// The browser over the `Person` list in this directory, with its loop
// woken on the channel, after it took the watch's first read. The file
// differs from the list the run opened with, so the first read is a
// change the browser takes. The first read may land before the waker is
// set, so the browser looks before it waits, and the wakes the first read
// sent are dropped, so the next wait is for the next read.
fn following(dir: &tempfile::TempDir) -> (Browser<Fake, NoArt>, Receiver<()>) {
    let (browser, _bus) = on_bus(3, Vec::new());
    let mut browser = browser
        .with_audience(list(&["person-a"]), Vec::new())
        .with_people_file(Some(dir.path().join("people.json")));
    let (sender, woken) = channel();
    browser.wake_by(Arc::new(move || {
        let _ = sender.send(());
    }));
    browser.tick(0.0);
    while !browser.follow_people() {
        woken
            .recv_timeout(Duration::from_secs(5))
            .expect("the watch reads within five seconds");
    }
    while woken.try_recv().is_ok() {}
    (browser, woken)
}

// Wait for the watch's next read and fold it in.
fn settle(browser: &mut Browser<Fake, NoArt>, woken: &Receiver<()>) {
    woken
        .recv_timeout(Duration::from_secs(5))
        .expect("the watch reads within five seconds");
    browser.follow_people();
}

const TWO: &str = r#"[{"name":"person-a","displayName":"Person A"},{"name":"person-b","displayName":"Person B"}]"#;

#[test]
fn the_kubelets_swap_of_the_list_redraws_the_picker() {
    let dir = mounted(r#"[{"name":"person-a","displayName":"Person A"}]"#);
    let (mut browser, woken) = following(&dir);

    swap(&dir, "..2026_09_30_10_05_00.000000002", TWO);
    settle(&mut browser, &woken);

    assert_eq!(tiles(&browser), ["person-a", "person-b"]);
}

#[test]
fn a_file_that_does_not_parse_keeps_the_last_list_and_prints_a_line() {
    let dir = mounted(TWO);
    let (mut browser, woken) = following(&dir);
    let (log, lines) = crate::log::Log::kept();
    browser.log = log;

    swap(&dir, "..2026_09_30_10_05_00.000000002", "not json");
    settle(&mut browser, &woken);

    assert_eq!(tiles(&browser), ["person-a", "person-b"]);
    let lines = lines.take();
    assert_eq!(lines.len(), 1);
    assert!(
        lines[0].starts_with("the Person list could not be read, and the browser keeps"),
        "{}",
        lines[0]
    );
}
