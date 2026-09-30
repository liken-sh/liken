// The lines the browser prints for what a person does: one line per
// press that names the key and what it did, one per play request that
// names where it went, and one per thing the bus confirmed. A loop that
// only draws and reads prints nothing.

use std::cell::RefCell;
use std::rc::Rc;

use super::*;
use crate::log::Log;

// The browser's log swapped for one that keeps its lines.
fn kept<S: Source, A: Art>(browser: &mut Browser<S, A>) -> Rc<RefCell<Vec<String>>> {
    let (log, lines) = Log::kept();
    browser.log = log;
    lines
}

// The lines a run of presses printed on a browser with no bus. Each case
// starts on a fresh browser, so a line names only its own presses.
fn pressed(presses: &[&str]) -> Vec<String> {
    let mut browser = browser(3);
    let lines = kept(&mut browser);
    for press in presses {
        browser.key(press);
    }
    lines.take()
}

const PRESSES: [(&[&str], &str); 11] = [
    (&["right"], "press right: moved focus"),
    (
        &["enter"],
        "press enter: opened the wall of screening/films",
    ),
    (
        &["enter", "enter"],
        "press enter: opened the movie page of movies:1 in screening/films",
    ),
    (
        &["enter", "enter", "escape"],
        "press escape: went back to the wall of screening/films",
    ),
    (
        &["enter", "enter", "home"],
        "press home: went to the home page",
    ),
    (
        &["home"],
        "press home: moved focus to the top of the home page",
    ),
    (
        &["search"],
        "press search: opened the search wall with the keyboard grid",
    ),
    (
        &["q"],
        "press a character: opened the search wall with the character typed",
    ),
    (
        &["q", "z"],
        "press a character: search input, and the field holds 2 characters",
    ),
    (
        &["power"],
        "press power: changed nothing, because this run has no bus to ask for the shade",
    ),
    (
        &["people"],
        "press people: changed nothing, because the cluster holds no Person",
    ),
];

#[test]
fn every_press_prints_one_line_with_the_key_and_what_it_did() {
    for (presses, last) in PRESSES {
        let lines = pressed(presses);
        assert_eq!(lines.len(), presses.len(), "{presses:?}: {lines:?}");
        assert_eq!(lines.last().map(String::as_str), Some(last), "{presses:?}");
    }
}

#[test]
fn a_character_names_no_letter_the_person_typed() {
    let lines = pressed(&["q", "z", "backspace"]);

    assert!(
        lines
            .iter()
            .all(|line| !line.contains('q') && !line.contains('z')),
        "{lines:?}"
    );
    assert_eq!(
        lines[2],
        "press backspace: search input, and the field holds 1 character"
    );
}

// Presses over the bus, which carry the kernel's name beside the word.
const REMOTE: [(&str, &str); 3] = [
    (
        "KEY_OK",
        "press enter (KEY_OK): opened the wall of screening/films",
    ),
    (
        "KEY_F1",
        "press KEY_F1: changed nothing, because no key is bound to it",
    ),
    (
        "KEY_Q",
        "press a character: opened the search wall with the character typed",
    ),
];

#[test]
fn a_press_from_a_remote_names_the_word_and_the_kernel_name() {
    for (name, line) in REMOTE {
        let (mut browser, _bus) = on_bus(3, vec![Moment::Press(name.into())]);
        let lines = kept(&mut browser);

        browser.pump(0.0);

        assert_eq!(*lines.borrow(), [line], "{name}");
    }
}

#[test]
fn power_over_a_bus_asks_for_the_shade_and_the_shade_confirms() {
    let (mut browser, bus) = on_bus(3, Vec::new());
    let lines = kept(&mut browser);
    browser.key("power");
    bus.inbound
        .lock()
        .expect("no test panics with the lock")
        .extend([Moment::Sleep, Moment::Wake]);

    browser.pump(0.0);

    assert_eq!(
        *lines.borrow(),
        [
            "press power: asked for the shade",
            "the shade is down",
            "the shade is up",
        ]
    );
}

#[test]
fn a_play_prints_what_was_asked_and_where_the_request_went() {
    let (mut browser, _bus) = playing(vec![one_item()]);
    let lines = kept(&mut browser);
    browser.key("enter");
    browser.key("enter");

    browser.key("enter");

    assert_eq!(
        lines.borrow()[2..],
        [
            format!(
                "play movies:1 in screening/films from the start for nobody: \
                 sent a request for 1 file on {PLAY_TOPIC}"
            ),
            "press enter: asked to play movies:1".to_string(),
        ]
    );
}

#[test]
fn a_play_with_nothing_to_play_says_why_nothing_went() {
    let (mut browser, bus) = playing(Vec::new());
    let lines = kept(&mut browser);
    browser.key("enter");
    browser.key("enter");

    browser.key("enter");

    assert!(published_nothing(&bus));
    assert_eq!(
        lines.borrow()[2],
        "play movies:1 in screening/films from the start for nobody: \
         sent nothing, because the catalog holds no file to play"
    );
}

#[test]
fn a_play_with_no_topic_says_why_nothing_went() {
    let mut browser = browser(3).with_bus(
        Some(Box::new(FakeBus::default())),
        String::new(),
        String::new(),
    );
    browser.source.items = vec![one_item()];
    let lines = kept(&mut browser);
    browser.key("enter");
    browser.key("enter");

    browser.key("enter");

    assert_eq!(
        lines.borrow()[2],
        "play movies:1 in screening/films from the start for nobody: \
         sent nothing, because the operator named no play topic"
    );
}

#[test]
fn a_resume_names_the_second_and_the_people() {
    let (browser, _bus) = playing(vec![one_item()]);
    let mut browser = browser.with_audience(
        vec![crate::audience::Person {
            name: "person-a".into(),
            display_name: "A".into(),
            thumbnail: None,
        }],
        vec!["person-a".into()],
    );
    browser.source.reached.insert(
        ("screening/films".to_string(), "movies:1".to_string()),
        Progress {
            position: 600,
            duration: 5_400,
            ..Progress::default()
        },
    );
    let lines = kept(&mut browser);
    browser.key("enter");
    browser.key("enter");

    browser.key("enter");

    assert_eq!(
        lines.borrow()[2],
        format!(
            "play movies:1 in screening/films from 600 s for person-a: \
             sent a request for 1 file on {PLAY_TOPIC}"
        )
    );
}

#[test]
fn an_answer_in_the_picker_names_the_people_and_the_topic() {
    let (browser, _bus) = playing(vec![one_item()]);
    let mut browser = browser.with_audience(
        vec![crate::audience::Person {
            name: "person-a".into(),
            display_name: "A".into(),
            thumbnail: None,
        }],
        Vec::new(),
    );
    let lines = kept(&mut browser);

    // The first press raises the picker, the second chooses the one
    // person, and the third and fourth reach the link and answer.
    for press in ["right", "enter", "down", "enter"] {
        browser.key(press);
    }

    assert_eq!(
        *lines.borrow(),
        [
            "press right: raised the person picker, because nobody has said who is watching",
            "press enter: chose person-a in the person picker",
            "press down: moved focus",
            &format!(
                "press enter: answered who is watching with person-a, and sent it on {AUDIENCE_TOPIC}"
            ),
        ]
    );
}

#[test]
fn a_change_of_activity_prints_once_and_a_repeated_status_prints_nothing() {
    let (mut browser, _bus) = on_bus(
        3,
        vec![
            status(Activity::Starting),
            status(Activity::Starting),
            status(Activity::Playing),
            status(Activity::Playing),
            status(Activity::Idle),
        ],
    );
    let lines = kept(&mut browser);

    browser.pump(0.0);

    assert_eq!(
        *lines.borrow(),
        [
            "the player went from idle to starting",
            "the player went from starting to playing",
            "the player went from playing to idle",
        ]
    );
}

#[test]
fn an_up_next_ask_names_the_work_and_the_request() {
    let payload = serde_json::json!({
        "library": "screening/films",
        "selection": {"movie": {"id": "movies:2"}},
    })
    .to_string()
    .into_bytes();
    let (mut browser, _bus) = on_bus(3, vec![Moment::PlayNext(payload)]);
    browser.source.items = vec![one_item()];
    let lines = kept(&mut browser);

    browser.pump(0.0);

    assert_eq!(
        *lines.borrow(),
        [
            "the player asked for up next: movies:2 in screening/films".to_string(),
            format!(
                "play movies:2 in screening/films from the start for nobody: \
                 sent a request for 1 file on {PLAY_TOPIC}"
            ),
        ]
    );
}

#[test]
fn an_up_next_ask_this_browser_did_not_write_says_so() {
    let (mut browser, _bus) = on_bus(3, vec![Moment::PlayNext(b"{}".to_vec())]);
    let lines = kept(&mut browser);

    browser.pump(0.0);

    assert_eq!(
        *lines.borrow(),
        ["the player asked for up next, and the ask carried no request of this browser's"]
    );
}

#[test]
fn an_audience_that_lapses_prints_once() {
    let (browser, _bus) = on_bus(3, Vec::new());
    let mut browser = browser.with_audience(
        vec![crate::audience::Person {
            name: "person-a".into(),
            display_name: "A".into(),
            thumbnail: None,
        }],
        vec!["person-a".into()],
    );
    let lines = kept(&mut browser);

    browser.tick(crate::audience::IDLE_SECONDS + 1.0);
    browser.tick(crate::audience::IDLE_SECONDS + 2.0);

    assert_eq!(
        *lines.borrow(),
        [format!(
            "the answer to who is watching lapsed, and cleared the message on {AUDIENCE_TOPIC}"
        )]
    );
}

#[test]
fn an_audience_from_the_retained_message_names_the_people() {
    let held = crate::bus::audience::payload(
        &[crate::audience::Person {
            name: "person-a".into(),
            display_name: "A".into(),
            thumbnail: None,
        }],
        crate::clock::seconds(),
    );
    let (mut browser, _bus) = on_bus(
        3,
        vec![Moment::Message {
            topic: AUDIENCE_TOPIC.into(),
            payload: held,
            retained: true,
        }],
    );
    let lines = kept(&mut browser);

    browser.pump(0.0);

    assert_eq!(
        *lines.borrow(),
        [format!(
            "took who is watching from the retained message on {AUDIENCE_TOPIC}: person-a"
        )]
    );
}

// A browser left alone: a pass of the loop and a frame's tick, many times,
// with the bus delivering a status that changes nothing.
#[test]
fn a_steady_loop_prints_nothing() {
    let (mut browser, bus) = on_bus(3, Vec::new());
    let lines = kept(&mut browser);

    for second in 0..600 {
        bus.inbound
            .lock()
            .expect("no test panics with the lock")
            .push(status(Activity::Idle));
        let at = f64::from(second) / 10.0;
        browser.pump(at);
        browser.tick(at);
    }

    assert!(lines.borrow().is_empty(), "{:?}", lines.borrow());
}

// Every screen a line can name, as the line names it. A title, a
// person's entry, a search, and a genre are named by an id, a hash, or
// their kind, so no line carries the library's own words.
#[test]
fn a_screen_is_named_by_its_kind_and_its_opaque_id() {
    let mut source = Fake {
        movies: 5,
        orders: true,
        people: true,
        ..Fake::default()
    };
    let wall = |query| screens::Screen::Wall(Box::new(Wall::open(query, &mut Fake::default())));
    let screens = [
        (
            wall(Query::Search {
                text: "hidden".into(),
            }),
            "the search wall".to_string(),
        ),
        (
            wall(Query::Genre {
                name: "Hidden".into(),
                order: Order::Released,
                sort: GenreSort::default(),
            }),
            "a genre wall".to_string(),
        ),
        (
            wall(Query::Released { fold: Fold::Titles }),
            "the recently released wall".to_string(),
        ),
        (
            wall(Query::Added { fold: Fold::Titles }),
            "the recently added wall".to_string(),
        ),
        (
            wall(Query::Set {
                library: "screening/films".into(),
                id: "set:tmdb:1001".into(),
            }),
            "the wall of set set:tmdb:1001 in screening/films".to_string(),
        ),
        (
            wall(Query::Franchise {
                library: "screening/orders".into(),
                id: "franchise:name:hidden".into(),
            }),
            format!(
                "the wall of franchise {} in screening/orders",
                crate::log::opaque("franchise:name:hidden")
            ),
        ),
        (
            wall(Query::Person {
                library: "screening/films".into(),
                path: ENTRY.into(),
            }),
            format!(
                "the credits wall of entry {} in screening/films",
                crate::log::hashed(ENTRY)
            ),
        ),
        (
            screens::Screen::Person(Box::new(
                screens::person::Person::open("screening/films", ENTRY, &mut source)
                    .expect("the fake credits the player"),
            )),
            format!(
                "the person page of entry {} in screening/films",
                crate::log::hashed(ENTRY)
            ),
        ),
        (
            screens::Screen::Series(Box::new(
                screens::series::Series::open(SERIALS, SERIAL, &mut source)
                    .expect("the fake holds the serial"),
            )),
            format!("the series page of {SERIAL} in {SERIALS}"),
        ),
    ];
    for (screen, named) in screens {
        let line = crate::browser::lines::screen(&screen);
        assert_eq!(line, named);
        assert!(
            !line.contains("hidden") && !line.contains("Hidden"),
            "{line}"
        );
        assert!(!line.contains(PLAYER), "{line}");
    }
}
