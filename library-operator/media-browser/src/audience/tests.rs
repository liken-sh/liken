// The `Person` list as it is read, and the answer an `Audience` holds,
// keeps, and lets lapse.

use super::*;

#[test]
fn a_person_carries_the_name_and_the_display_name() {
    let people = people_from_json(br#"[{"name":"first","displayName":"First"}]"#).unwrap();
    assert_eq!(
        people,
        [Person {
            name: "first".into(),
            display_name: "First".into(),
        }]
    );
}

#[test]
fn a_person_with_no_display_name_draws_as_their_name() {
    let people = people_from_json(br#"[{"name":"second"}]"#).unwrap();
    assert_eq!(people[0].display_name, "second");
}

#[test]
fn an_empty_list_is_no_people() {
    assert_eq!(people_from_json(b"[]"), Ok(Vec::new()));
}

#[test]
fn a_document_that_is_not_a_list_is_an_error() {
    assert_eq!(
        people_from_json(br#"{"name":"first"}"#),
        Err("the people are not a list".to_string())
    );
}

#[test]
fn a_person_with_no_name_is_an_error() {
    assert_eq!(
        people_from_json(br#"[{"displayName":"First"}]"#),
        Err("a person has no name".to_string())
    );
}

#[test]
fn text_that_is_not_json_is_an_error() {
    assert!(people_from_json(b"first, second").is_err());
}

// An audience over a `Person` list of these names, the closed set an answer
// may name.
fn audience(known: &[&str]) -> Audience {
    Audience::new(
        known
            .iter()
            .map(|name| Person {
                name: (*name).to_string(),
                display_name: (*name).to_string(),
            })
            .collect(),
    )
}

#[test]
fn an_audience_that_was_never_asked_names_nobody() {
    let audience = audience(&["first"]);

    assert!(audience.current(0.0).is_empty());
    assert!(audience.needs_answer(0.0));
}

#[test]
fn an_audience_may_name_the_people_it_knows() {
    let audience = audience(&["first", "second"]);

    assert_eq!(audience.known().len(), 2);
    assert_eq!(audience.known()[0].name, "first");
}

#[test]
fn an_answer_stands_until_the_idle_hours_are_up() {
    let mut audience = audience(&["first", "second"]);
    audience.answer(vec!["first".into()], 0.0);

    assert_eq!(audience.current(IDLE_SECONDS), ["first".to_string()]);
    assert!(!audience.needs_answer(IDLE_SECONDS));
}

#[test]
fn an_answer_lapses_once_the_idle_hours_are_up() {
    let mut audience = audience(&["first"]);
    audience.answer(vec!["first".into()], 0.0);

    assert!(audience.current(IDLE_SECONDS + 1.0).is_empty());
    assert!(audience.needs_answer(IDLE_SECONDS + 1.0));
}

#[test]
fn a_press_inside_the_hours_holds_the_answer_open() {
    let mut audience = audience(&["first"]);
    audience.answer(vec!["first".into()], 0.0);

    audience.touch(IDLE_SECONDS);

    assert_eq!(audience.current(2.0 * IDLE_SECONDS), ["first".to_string()]);
}

#[test]
fn a_press_after_the_hours_clears_the_answer_it_found() {
    let mut audience = audience(&["first"]);
    audience.answer(vec!["first".into()], 0.0);

    audience.touch(IDLE_SECONDS + 1.0);

    assert!(audience.current(IDLE_SECONDS + 1.0).is_empty());
    assert!(audience.needs_answer(IDLE_SECONDS + 1.0));
}

#[test]
fn nobody_is_an_answer() {
    let mut audience = audience(&["first"]);
    audience.answer(Vec::new(), 0.0);

    assert!(audience.current(0.0).is_empty());
    assert!(!audience.needs_answer(0.0));
}

#[test]
fn a_new_person_list_keeps_the_answer_that_stands() {
    let mut audience = audience(&["first", "second"]);
    audience.answer(vec!["first".to_string()], 0.0);

    audience.learn(
        ["first", "second", "third"]
            .iter()
            .map(|name| Person {
                name: (*name).to_string(),
                display_name: (*name).to_string(),
            })
            .collect(),
    );

    assert_eq!(audience.known().len(), 3);
    assert_eq!(audience.current(1.0), ["first".to_string()]);
}

#[test]
fn a_name_the_person_list_does_not_hold_is_dropped() {
    let mut audience = audience(&["first"]);

    audience.answer(vec!["first".into(), "third".into()], 0.0);

    assert_eq!(audience.current(0.0), ["first".to_string()]);
}

// An audience over a `Person` list whose display names differ from the
// names the records key on.
fn named(known: &[(&str, &str)]) -> Audience {
    Audience::new(
        known
            .iter()
            .map(|(name, display_name)| Person {
                name: (*name).to_string(),
                display_name: (*display_name).to_string(),
            })
            .collect(),
    )
}

#[test]
fn the_room_carries_the_first_letter_of_each_display_name() {
    let mut audience = named(&[("first", "Coral"), ("second", "Kestrel")]);
    audience.answer(vec!["first".into(), "second".into()], 0.0);

    assert_eq!(audience.letters(0.0), ["C".to_string(), "K".to_string()]);
}

#[test]
fn a_lapsed_answer_carries_no_letters() {
    let mut audience = named(&[("first", "Coral")]);
    audience.answer(vec!["first".into()], 0.0);

    assert!(audience.letters(IDLE_SECONDS + 1.0).is_empty());
}

#[test]
fn the_room_names_the_people_of_the_answer_by_their_index() {
    let mut audience = audience(&["first", "second", "third"]);
    audience.answer(vec!["third".into(), "first".into()], 0.0);

    assert_eq!(audience.chosen(0.0), [2, 0]);
    assert!(audience.chosen(IDLE_SECONDS + 1.0).is_empty());
}

#[test]
fn any_name_stands_where_the_browser_knows_no_people() {
    let mut audience = audience(&[]);

    audience.answer(vec!["third".into()], 0.0);

    assert_eq!(audience.current(0.0), ["third".to_string()]);
    assert!(!audience.needs_answer(0.0));
}

// The answer on the bus: who is watching with the names a screen draws,
// the stamp the message carries, and the lapse that ends both.

#[test]
fn the_answer_on_the_bus_carries_each_display_name() {
    let mut audience = named(&[("first", "Coral"), ("second", "Kestrel")]);
    audience.answer(vec!["second".into(), "first".into()], 0.0);

    assert_eq!(
        audience.watching(0.0),
        Some(vec![
            Person {
                name: "second".into(),
                display_name: "Kestrel".into(),
            },
            Person {
                name: "first".into(),
                display_name: "Coral".into(),
            },
        ])
    );
}

#[test]
fn an_answer_of_nobody_is_an_empty_room_and_not_the_absence_of_one() {
    let mut audience = audience(&["first"]);
    audience.answer(Vec::new(), 0.0);

    assert_eq!(audience.watching(0.0), Some(Vec::new()));
}

#[test]
fn an_audience_with_no_answer_holds_nothing_for_the_bus() {
    let mut audience = audience(&["first"]);
    assert_eq!(audience.watching(0.0), None);

    audience.answer(vec!["first".into()], 0.0);

    assert_eq!(audience.watching(IDLE_SECONDS + 1.0), None);
}

#[test]
fn the_first_press_of_a_minute_moves_the_stamp_and_the_rest_do_not() {
    let mut audience = audience(&["first"]);
    assert!(audience.stamp_due(1_000));

    audience.stamped(1_000);

    assert_eq!(audience.stamp(), 1_000);
    assert!(!audience.stamp_due(1_000 + STAMP_SECONDS - 1));
    assert!(audience.stamp_due(1_000 + STAMP_SECONDS));
}

#[test]
fn the_lapse_drops_the_answer_and_the_stamp_with_it() {
    let mut audience = audience(&["first"]);
    audience.answer(vec!["first".into()], 0.0);
    audience.stamped(1_000);

    assert!(audience.lapse(IDLE_SECONDS + 1.0));

    assert!(audience.needs_answer(IDLE_SECONDS + 1.0));
    assert_eq!(audience.stamp(), 0);
}

#[test]
fn the_lapse_reports_an_answer_gone_once_and_no_more() {
    let mut audience = audience(&["first"]);
    audience.answer(vec!["first".into()], 0.0);

    assert!(!audience.lapse(IDLE_SECONDS));
    assert!(audience.lapse(IDLE_SECONDS + 1.0));
    assert!(!audience.lapse(IDLE_SECONDS + 2.0));
}

#[test]
fn a_press_reports_the_lapsed_answer_it_cleared() {
    let mut audience = audience(&["first"]);
    audience.answer(vec!["first".into()], 0.0);

    assert!(!audience.touch(IDLE_SECONDS));
    assert!(audience.touch(2.0 * IDLE_SECONDS + 1.0));
}
