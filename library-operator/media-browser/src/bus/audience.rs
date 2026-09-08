// Who is watching, as the browser keeps it on the bus. The browser is
// the one writer and the one reader that acts on it, so this file is
// both ends of the codec. A client that wants to know who is in the
// room reads the same message.

use serde_json::{Map, Value};

use crate::audience::{self, Person};

/// The answer as the retained message carries it: the room in answer
/// order, and the wall second of the last press.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Held {
    pub people: Vec<Person>,
    pub at: i64,
}

/// The answer as bytes. Each person carries the display name the browser
/// draws, so a client that reads the room needs no `Person` list of its
/// own. An answer of nobody carries an empty list, which is an answer and
/// not the absence of one.
///
/// `at` is the wall clock in whole seconds since the Unix epoch of the
/// last press. A browser that starts again measures the idle window from
/// it, so it is the one field that decides whether the answer still
/// stands.
pub fn payload(people: &[Person], at: i64) -> Vec<u8> {
    let mut message = Map::new();
    message.insert(
        "people".into(),
        Value::Array(people.iter().map(one).collect()),
    );
    message.insert("at".into(), Value::from(at));
    Value::Object(message).to_string().into_bytes()
}

// One person: the name every record keys on, and the name a screen
// draws.
fn one(person: &Person) -> Value {
    serde_json::json!({
        "name": person.name,
        "displayName": person.display_name,
    })
}

/// The answer the bytes carry, or nothing where they are not a message
/// this browser wrote. The clear is an empty payload, so it carries no
/// answer either.
pub fn held(payload: &[u8]) -> Option<Held> {
    let message: Value = serde_json::from_slice(payload).ok()?;
    Some(Held {
        people: audience::people_from_value(message.get("people")?).ok()?,
        at: message.get("at")?.as_i64()?,
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    fn watching(names: &[(&str, &str)]) -> Vec<Person> {
        names
            .iter()
            .map(|(name, display_name)| Person {
                name: (*name).to_string(),
                display_name: (*display_name).to_string(),
            })
            .collect()
    }

    #[test]
    fn the_message_carries_the_room_and_the_second_of_the_last_press() {
        let bytes = payload(
            &watching(&[("chris", "Chris"), ("second", "Second")]),
            1_757_350_000,
        );

        assert_eq!(
            serde_json::from_slice::<Value>(&bytes).expect("the message is JSON"),
            serde_json::json!({
                "people": [
                    {"name": "chris", "displayName": "Chris"},
                    {"name": "second", "displayName": "Second"},
                ],
                "at": 1_757_350_000,
            })
        );
    }

    #[test]
    fn an_answer_of_nobody_carries_an_empty_list() {
        assert_eq!(
            serde_json::from_slice::<Value>(&payload(&[], 7)).expect("the message is JSON"),
            serde_json::json!({"people": [], "at": 7})
        );
    }

    #[test]
    fn the_room_comes_back_the_way_it_went_out() {
        let people = watching(&[("chris", "Chris")]);

        assert_eq!(
            held(&payload(&people, 1_757_350_000)),
            Some(Held {
                people,
                at: 1_757_350_000
            })
        );
    }

    #[test]
    fn an_answer_of_nobody_comes_back_as_an_answer() {
        let back = held(&payload(&[], 7)).expect("the message decodes");

        assert!(back.people.is_empty());
        assert_eq!(back.at, 7);
    }

    #[test]
    fn a_person_the_message_names_without_a_display_name_draws_by_name() {
        let back = held(br#"{"people":[{"name":"chris"}],"at":7}"#).expect("the message decodes");

        assert_eq!(back.people, watching(&[("chris", "chris")]));
    }

    #[test]
    fn bytes_this_browser_did_not_write_decode_to_nothing() {
        let cases: [&[u8]; 7] = [
            b"",
            b"who is watching",
            b"{}",
            br#"{"people":[]}"#,
            br#"{"at":7}"#,
            br#"{"people":7,"at":7}"#,
            br#"{"people":[{"displayName":"Chris"}],"at":7}"#,
        ];
        for payload in cases {
            assert_eq!(held(payload), None);
        }
    }
}
