//! What a role with no part shows in its shape. A role that is expected
//! says that its part is missing. A role that the object needs only in
//! some state, and a role whose objects other roles of the elevation
//! draw, is drawn empty and at rest, because it is not missing a part. A
//! role whose fill `Kinds::resolve` could not confirm says why.

use super::super::board::Subject;
use crate::shapes::text::{Nothing, State};
use transect_resources::knowledge::role::{Expected, First, Role};

/// What `role` shows in `subject`'s elevation when it has no part, or
/// none when the page leaves its area blank. `elsewhere` is the source
/// line of a role whose objects other roles draw, which names them.
///
/// The empty place names what is missing: the role's own `label` when
/// the author gave it one, such as "no keeper", or else the kind of its
/// parts, such as "no Lamp". The label of a reference is often a verb,
/// such as "lit by", which does not name a thing.
pub fn nothing(subject: &Subject, role: &Role, elsewhere: Option<&str>) -> Option<Nothing> {
    let references = &subject.knowledge.references;
    let kind = role.fill.parts_kind(references).map(|(_, kind)| kind);
    let missing_thing = role.label.as_deref().or(kind);
    let text = |other: &str| match missing_thing {
        Some(thing) => format!("no {other}{thing}"),
        None => "nothing".into(),
    };
    let empty = |note: String, state| {
        Some(Nothing {
            text: text(""),
            note,
            state,
        })
    };
    if let Some(unresolved) = role.unresolved() {
        return empty(unresolved.into(), State::Empty);
    }
    if role.expected == Expected::Never {
        return None;
    }
    if let Some(line) = elsewhere {
        return Some(Nothing {
            text: text("other "),
            note: line.into(),
            state: State::Elsewhere,
        });
    }
    let condition = match &role.expected {
        Expected::While(condition) => condition,
        _ => return empty(missing(subject, role), State::Empty),
    };
    let found = subject.conditions.iter().find(|c| c.kind == *condition);
    let note = match found {
        Some(found) if found.status == "True" => {
            return empty(missing(subject, role), State::Empty);
        }
        Some(found) => {
            let said = found.message.as_ref().or(found.reason.as_ref());
            let said = said
                .cloned()
                .unwrap_or_else(|| format!("{condition} is {}", found.status));
            format!("expected while {condition}, now {said}")
        }
        None => format!(
            "expected while {condition}, and the {} has no {condition} condition",
            subject.object.kind
        ),
    };
    empty(note, State::Elsewhere)
}

/// Why an expected role has no part: "spec.lamp names nothing now", or
/// "no Bin names this Shelf in spec.shelf".
fn missing(subject: &Subject, role: &Role) -> String {
    let references = &subject.knowledge.references;
    let fill = &role.fill;
    let end = fill.parts_kind(references).map(|(_, kind)| kind);
    match (&fill.first, &fill.through) {
        (First::Reference(path), None) => format!("{path} names nothing now"),
        (First::NamedBy(named), None) => format!(
            "no {} names this {} in {}",
            named.kind, subject.object.kind, named.path
        ),
        (First::NamedBy(named), Some(through)) => match end {
            Some(end) => format!(
                "no {} names {} {end} in {}",
                named.kind,
                article(end),
                through.path
            ),
            None => format!("no {} names anything in {}", named.kind, through.path),
        },
        (First::Reference(_), Some(_)) => format!("{} names nothing now", fill.fields()),
    }
}

/// "an" before a word that starts with a vowel, and "a" before others.
fn article(word: &str) -> &'static str {
    match word.chars().next().map(|c| c.to_ascii_lowercase()) {
        Some('a' | 'e' | 'i' | 'o' | 'u') => "an",
        _ => "a",
    }
}
