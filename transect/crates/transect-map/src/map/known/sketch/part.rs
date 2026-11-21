//! The part that the page draws for one value or object of a role. It
//! links and notes the value as the ring's box does, takes its state from
//! the object it names, and reads the columns that the role names.

use super::super::board::Board;
use super::super::end::{end, refer};
use super::fill::Found;
use crate::shapes::text::{End, Part, Reading, Shown, State};
use transect_graph::edges::{Endpoint, Object, kind_and_name};
use transect_graph::links::Reference;
use transect_resources::knowledge::Names;
use transect_resources::knowledge::role::{Column, Role, Show};

/// The part for `found`, one part that `role` reads in the elevation of
/// the object `subject`, whose caption is `label`. Only a part that
/// names an object shows `shows`, so a role whose reference reads
/// nothing draws nothing of the object.
pub fn part(
    board: &Board,
    subject: &Object,
    role: &Role,
    label: &str,
    shows: &[Shown],
    found: &Found,
) -> Part {
    let mut end = end_of(board, subject, found);
    let object = match found {
        Found::Value(relation, _) => match &relation.names {
            Names::Object(object) => object,
            Names::Value => return quiet(end, State::Value, true),
            Names::Address | Names::Ambiguous(_) => return quiet(end, State::Plain, true),
        },
        Found::Object(object) => object,
        Found::Ambiguous { .. } => return quiet(end, State::Plain, true),
    };
    end.reference = named(label, object, end.reference);
    described(board, role, shows, end, object)
}

/// `reference`, which names `object`, under a caption `label`. When the
/// caption already says the object's kind, as "shelf" says `Shelf`, the
/// name leaves the kind out, so a part does not say its kind twice. The
/// words are compared without case, spaces, or hyphens.
pub fn named(label: &str, object: &Object, reference: Reference) -> Reference {
    let plain = |text: &str| {
        let letters = text.chars().filter(|c| !matches!(c, ' ' | '-'));
        letters.flat_map(char::to_lowercase).collect::<String>()
    };
    let prefix = format!("{} ", object.kind);
    match (
        plain(label) == plain(&object.kind),
        reference.text.strip_prefix(&prefix),
    ) {
        (true, Some(name)) => Reference {
            text: name.to_string(),
            ..reference
        },
        _ => reference,
    }
}

/// The value or the object that `found` names, linked to its page, as
/// the drawing names it in the elevation of `subject`.
pub fn end_of(board: &Board, subject: &Object, found: &Found) -> End {
    let here = subject.namespace.as_deref();
    match found {
        Found::Value(relation, reached) => end(relation, reached, here, board),
        Found::Object(object) => End {
            reference: refer(&Endpoint::Object(object.clone()), here, board),
            under: None,
            reaches: Vec::new(),
        },
        Found::Ambiguous { count, from, path } => End {
            reference: Reference {
                text: format!("{path} of {}", kind_and_name(&from.kind, &from.name)),
                href: None,
            },
            under: Some(format!("matches {count} objects, so none is drawn")),
            reaches: Vec::new(),
        },
    }
}

/// The part for `object`, which `end` names, in the state that the
/// cluster gives it, with the columns that `role` reads.
fn described(board: &Board, role: &Role, shows: &[Shown], mut end: End, object: &Object) -> Part {
    let (state, why) = (board.standing)(object).state();
    if state == State::NoObject {
        end.under = Some(format!("the cluster has no such {}", object.kind));
        end.reaches.clear();
        return quiet(end, State::NoObject, false);
    }
    let readings = read(board, object, &role.reads).into_iter();
    let readings = readings.map(|reading| Reading {
        name: reading.name.to_lowercase(),
        value: units_kept(&reading.value),
        ..reading
    });
    Part {
        readings: readings.collect(),
        shows: shows.to_vec(),
        end,
        state,
        why,
        literal: false,
    }
}

/// The first column of each entry of `shows` that has a value on
/// `object`, whose elevation is drawn, or on the object at the end of
/// its reference. Each column is read on its own, so a column of the
/// object and the column of the same name through a reference are not
/// confused.
pub fn shows(board: &Board, object: &Object, shows: &[Show]) -> Vec<Shown> {
    let first = |show: &Show| {
        let mut columns = show.columns.iter().enumerate();
        columns.find_map(|(at, column)| {
            let read = read(board, object, std::slice::from_ref(column));
            let reading = read.into_iter().next()?;
            Some(Shown {
                reading: Reading {
                    value: units_kept(&reading.value),
                    ..reading
                },
                fallback: at > 0,
            })
        })
    };
    shows.iter().filter_map(first).collect()
}

fn read(board: &Board, object: &Object, columns: &[Column]) -> Vec<Reading> {
    if columns.is_empty() {
        return Vec::new();
    }
    (board.readings)(object, columns)
}

/// `value` with each number kept on one line with its sign and its unit:
/// a space after a digit becomes a no-break space, as in "200 mm", and a
/// hyphen that starts a number becomes a minus sign, as in "−10 °C",
/// because a line may break after a hyphen and the number would read as
/// positive.
fn units_kept(value: &str) -> String {
    let mut kept = String::with_capacity(value.len());
    let mut chars = value.chars().peekable();
    let mut previous: Option<char> = None;
    while let Some(c) = chars.next() {
        let next_digit = chars.peek().is_some_and(char::is_ascii_digit);
        let starts = previous.is_none_or(char::is_whitespace);
        let kept_char = match c {
            ' ' if previous.is_some_and(|p| p.is_ascii_digit()) => '\u{a0}',
            '-' if starts && next_digit => '\u{2212}',
            other => other,
        };
        kept.push(kept_char);
        previous = Some(c);
    }
    kept
}

/// A part with no readings.
fn quiet(end: End, state: State, literal: bool) -> Part {
    Part {
        end,
        readings: Vec::new(),
        shows: Vec::new(),
        state,
        why: None,
        literal,
    }
}
