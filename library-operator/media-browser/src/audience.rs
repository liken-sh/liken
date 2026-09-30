// The people in the room: the `Person` list a screen may name, and the
// answer to "who is watching" that every play request carries. This is not
// catalog data; the catalog's `Person` is a credited contributor.

use serde_json::Value;

// The pictures of the people, cut to the circle every screen draws.
pub mod faces;
// The picture a person's entry carries.
pub mod thumbnail;
// The inotify watch that keeps the list current from its file.
pub mod watch;

pub use thumbnail::Thumbnail;

/// One `Person` of the cluster: the name every record keys on, and the name
/// a screen draws.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Person {
    /// The `Person` resource's own name, which a play request and a progress
    /// row carry.
    pub name: String,
    /// The name a screen draws. It is the resource's name where the resource
    /// declares none.
    pub display_name: String,
    /// The person's picture, which every screen draws in place of the
    /// letter. It is none where the entry carries no thumbnail, as on a
    /// cluster where people-operator does not run, or one the browser
    /// cannot open.
    pub thumbnail: Option<Thumbnail>,
}

/// One person in the room as a screen draws them in a circle: the
/// `Person` name their face is found by, and the first letter of their
/// display name, which draws where they have no face.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Viewer {
    /// The `Person` resource's own name.
    pub name: String,
    /// The first letter of the display name.
    pub letter: String,
}

impl Viewer {
    /// This person as a circle draws them.
    pub fn of(person: &Person) -> Self {
        Self {
            name: person.name.clone(),
            letter: first_letter(&person.display_name),
        }
    }

    /// The one "?" circle the strip draws where nobody is watching. It
    /// names no `Person`, so it has no face.
    pub fn nobody() -> Self {
        Self {
            name: String::new(),
            letter: "?".to_string(),
        }
    }
}

// The first letter of a display name, which a circle draws for a person
// with no face.
fn first_letter(display_name: &str) -> String {
    display_name.chars().next().unwrap_or_default().to_string()
}

/// The `Person` list as a file holds it:
/// `[{"name":"person-a","displayName":"Person A","thumbnail":"data:image/jpeg;base64,..."}]`.
pub fn people_from_json(bytes: &[u8]) -> Result<Vec<Person>, String> {
    let document: Value = serde_json::from_slice(bytes).map_err(|error| error.to_string())?;
    people_from_value(&document)
}

/// The same list inside a message that carries more than the people, which
/// is how the answer to who is watching travels on the bus.
pub fn people_from_value(value: &Value) -> Result<Vec<Person>, String> {
    let list = value
        .as_array()
        .ok_or_else(|| "the people are not a list".to_string())?;
    list.iter().map(person).collect()
}

// One entry of the list. The name is required; the display name falls back
// to it. A thumbnail that does not open is dropped and not an error, because
// the letter is a face every screen can always draw.
fn person(entry: &Value) -> Result<Person, String> {
    let name = entry
        .get("name")
        .and_then(Value::as_str)
        .unwrap_or_default();
    if name.is_empty() {
        return Err("a person has no name".to_string());
    }
    let display_name = entry
        .get("displayName")
        .and_then(Value::as_str)
        .unwrap_or(name);

    let thumbnail = entry
        .get("thumbnail")
        .and_then(Value::as_str)
        .and_then(Thumbnail::from_uri);

    Ok(Person {
        name: name.to_string(),
        display_name: display_name.to_string(),
        thumbnail,
    })
}

/// How long an answer stands with no press before the browser must ask
/// again: longer than any one film, and shorter than a day. The room
/// changes between sittings, not during one.
pub const IDLE_SECONDS: f64 = 3.0 * 60.0 * 60.0;

/// The seconds between two stamps on the bus. A press moves the stamp at
/// most this often, because the stamp says how long ago somebody pressed
/// a key, and a second either way changes nothing a reader can act on.
/// Without the window, a walk across a wall would publish one message per
/// keystroke.
pub const STAMP_SECONDS: i64 = 60;

/// The people in the room as the browser knows them: the `Person` list it
/// may name, the answer it holds, and the second of the last press.
#[derive(Debug, Clone, Default)]
pub struct Audience {
    known: Vec<Person>,
    // The answer. None is an audience never asked, or one whose answer
    // lapsed. An empty list is the answer "nobody".
    answered: Option<Vec<String>>,
    // The clock second of the last press, which the lapse measures from.
    active: f64,
    // The wall second the message on the bus carries, or zero where the
    // bus holds none. It is a wall second and not a run second, because a
    // browser that starts again reads it against its own wall clock.
    stamp: i64,
}

impl Audience {
    /// The audience of a browser that knows these people and holds no answer
    /// yet.
    pub fn new(known: Vec<Person>) -> Self {
        Self {
            known,
            answered: None,
            active: 0.0,
            stamp: 0,
        }
    }

    /// The `Person` list this audience may name, which the picker draws.
    pub fn known(&self) -> &[Person] {
        &self.known
    }

    /// Replace the `Person` list, and answer the list it replaces. The
    /// answer stands: a person who was chosen stays chosen, and one the
    /// new list dropped is filtered out of what a play records the next
    /// time an answer is taken. Until then the dropped name draws its own
    /// first letter, because no display name is left for it.
    pub fn learn(&mut self, known: Vec<Person>) -> Vec<Person> {
        std::mem::replace(&mut self.known, known)
    }

    /// Set who is watching, as of this second. A name outside the known
    /// list is dropped, because a play recorded against a `Person` the
    /// cluster does not hold names nobody. A browser that knows no people
    /// takes every name, so a run with no `Person` list still records.
    pub fn answer(&mut self, people: Vec<String>, at: f64) {
        let known = &self.known;
        self.answered = Some(match known.is_empty() {
            true => people,
            false => people
                .into_iter()
                .filter(|name| known.iter().any(|person| &person.name == name))
                .collect(),
        });
        self.active = at;
    }

    /// Record a press. It holds the answer open, and it clears an answer
    /// that already lapsed, so the next answer starts from nothing. The
    /// answer is whether the press found a lapsed answer, which is a
    /// message to clear from the bus.
    pub fn touch(&mut self, at: f64) -> bool {
        let lapsed = self.lapse(at);
        self.active = at;
        lapsed
    }

    /// Drop an answer the idle window ended, whether or not anybody
    /// pressed. The answer is whether one went, so the caller clears the
    /// message on the bus once and no more. The stamp goes with the
    /// answer, because the bus then holds nothing.
    pub fn lapse(&mut self, at: f64) -> bool {
        if self.answered.is_none() || !self.lapsed(at) {
            return false;
        }
        self.answered = None;
        self.stamp = 0;
        true
    }

    /// The current answer as `Person` records, in answer order, each with
    /// the display name this browser draws, or nothing where no answer
    /// stands. An answer of nobody is an empty room and not the absence of
    /// one, so the bus carries the two differently. The records carry no
    /// thumbnail, because the message on the bus names each person by
    /// their two names alone.
    pub fn watching(&self, at: f64) -> Option<Vec<Person>> {
        if self.lapsed(at) {
            return None;
        }
        Some(
            self.answered
                .as_ref()?
                .iter()
                .map(|name| Person {
                    name: name.clone(),
                    display_name: self.display_name(name).to_string(),
                    thumbnail: None,
                })
                .collect(),
        )
    }

    /// The wall second the message on the bus carries, or zero where the
    /// bus holds none.
    pub fn stamp(&self) -> i64 {
        self.stamp
    }

    /// Record the stamp the message on the bus now carries.
    pub fn stamped(&mut self, at: i64) {
        self.stamp = at;
    }

    /// Whether a press at this wall second must move the stamp on the bus,
    /// which is every press outside [`STAMP_SECONDS`] of the stamp that
    /// stands.
    pub fn stamp_due(&self, now: i64) -> bool {
        now - self.stamp >= STAMP_SECONDS
    }

    /// Who a play recorded this second names: the answer, or nobody where
    /// there is none or the last press is further back than
    /// [`IDLE_SECONDS`].
    pub fn current(&self, at: f64) -> &[String] {
        if self.lapsed(at) {
            return &[];
        }
        self.answered.as_deref().unwrap_or_default()
    }

    /// Each person in the current answer as a screen draws them, in the
    /// order of the answer. The strip draws one circle per viewer. A
    /// lapsed answer and an answer of nobody both carry none.
    pub fn viewers(&self, at: f64) -> Vec<Viewer> {
        self.current(at)
            .iter()
            .map(|name| Viewer {
                name: name.clone(),
                letter: first_letter(self.display_name(name)),
            })
            .collect()
    }

    /// The people of the current answer by their index in the known list. A
    /// picker opened from the strip starts with them chosen.
    pub fn chosen(&self, at: f64) -> Vec<usize> {
        self.current(at)
            .iter()
            .filter_map(|name| self.known.iter().position(|person| &person.name == name))
            .collect()
    }

    // The name a screen draws for one `Person` name.
    fn display_name<'a>(&'a self, name: &'a str) -> &'a str {
        self.known
            .iter()
            .find(|person| person.name == name)
            .map_or(name, |person| person.display_name.as_str())
    }

    /// Whether the browser must ask who is watching. An answer of nobody is
    /// an answer, so the browser asks again only where none stands. A
    /// browser that knows no people never asks.
    pub fn needs_answer(&self, at: f64) -> bool {
        !self.known.is_empty() && (self.answered.is_none() || self.lapsed(at))
    }

    // Whether the last press is further back than the idle window, which is
    // what ends an answer.
    fn lapsed(&self, at: f64) -> bool {
        at - self.active > IDLE_SECONDS
    }
}

#[cfg(test)]
mod tests;
