//! What the cluster says about one step of a sequence: whether a
//! condition of each part of a role has the status that the step
//! expects, or how many parts have each value of some printer columns.

use super::super::super::board::Board;
use super::super::fields::text;
use super::super::fill::{self, Found};
use super::super::part::end_of;
use super::{Judged, Mark};
use transect_resources::knowledge::role::{Column, Role};
use transect_resources::knowledge::sequence::StepState;

/// The state of a step, as its row writes it.
pub struct Read {
    pub text: String,
    pub judged: Judged,
    pub marks: Vec<Mark>,
    pub source: String,
}

/// What `state` reads of the parts of its role, among `roles`. A step
/// with no state says that no signal names it.
pub fn read(board: &Board, roles: &[Role], state: Option<&StepState>) -> Read {
    let Some(state) = state else {
        return Read {
            text: "transect reads no state for this step".into(),
            judged: Judged::Unsaid,
            marks: Vec::new(),
            source: "No field names this step, so the cluster cannot say whether it happens."
                .into(),
        };
    };
    let Some(role) = roles.iter().find(|role| role.name == state.role()) else {
        return Read {
            text: format!("no role {}", state.role()),
            judged: Judged::Unsaid,
            marks: Vec::new(),
            source: String::new(),
        };
    };
    let subject = &board.subject;
    let found = fill::found(board, subject, role);
    let label = role.label(&subject.knowledge.references);
    let fields = text(&super::super::fields::fields(board, subject, role));
    match state {
        StepState::Condition {
            condition,
            good,
            said,
            ..
        } => {
            let wanted = Wanted {
                condition,
                good,
                said: said.as_deref(),
            };
            judge(board, &found, &label, wanted, &fields)
        }
        StepState::Columns { columns, .. } => count(board, &found, &label, columns, &fields),
    }
}

/// The condition that a step reads, the status that meets it, and what
/// that status means when the knowledge says.
struct Wanted<'a> {
    condition: &'a str,
    good: &'a str,
    said: Option<&'a str>,
}

/// Whether the wanted condition of each part in `found` has the good
/// status.  The row writes what the status means when the knowledge
/// says, because "Holding False" makes a reader work out that the
/// budget has room.
fn judge(board: &Board, found: &[Found], label: &str, wanted: Wanted, fields: &str) -> Read {
    let Wanted {
        condition,
        good,
        said,
    } = wanted;
    let subject = &board.subject;
    let mut met = 0;
    let mut marks = Vec::new();
    for one in found {
        let part = end_of(board, &subject.object, one).reference;
        let part = match one.object() {
            Some(object) => transect_graph::links::Reference {
                text: super::name(object, subject.object.namespace.as_deref()),
                ..part
            },
            None => part,
        };
        let said = one.object().and_then(|object| (board.subjects)(object));
        let conditions = said.map(|said| said.conditions).unwrap_or_default();
        match conditions.iter().find(|c| c.kind == condition) {
            Some(found) if found.status == good => met += 1,
            Some(found) => {
                let why = found.reason.as_deref().or(found.message.as_deref());
                let why = why.map(|why| format!(", {why}")).unwrap_or_default();
                let said = format!("{condition} is {}{why}", found.status);
                marks.push(Mark { part, said });
            }
            None => {
                let said = format!("has no {condition} condition");
                marks.push(Mark { part, said });
            }
        }
    }
    let text = match (found.len(), good) {
        (0, _) => format!("no {label} now"),
        (all, _) if said.is_some() => format!("{} {met}/{all}", said.unwrap_or_default()),
        (all, "True") => format!("{condition} {met}/{all}"),
        (all, "False") => format!("not {condition} {met}/{all}"),
        (all, other) => format!("{condition} {other} {met}/{all}"),
    };
    let judged = match (found.is_empty(), marks.is_empty()) {
        (false, true) => Judged::Met,
        (false, false) => Judged::Marked,
        (true, _) => Judged::Counted,
    };
    Read {
        text,
        judged,
        marks,
        source: format!("{condition} of each part of {label}, read from {fields}"),
    }
}

/// How many parts in `found` have each value of each of `columns`, in
/// the order that the values first appear.
fn count(board: &Board, found: &[Found], label: &str, columns: &[String], fields: &str) -> Read {
    let mut said = Vec::new();
    for name in columns {
        let column = [Column {
            name: name.clone(),
            through: None,
        }];
        let mut values: Vec<(String, usize)> = Vec::new();
        for object in found.iter().filter_map(Found::object) {
            let readings = (board.readings)(object, &column);
            for reading in readings.into_iter().filter(|r| r.name == *name) {
                match values.iter_mut().find(|(value, _)| *value == reading.value) {
                    Some((_, count)) => *count += 1,
                    None => values.push((reading.value, 1)),
                }
            }
        }
        let values = values
            .iter()
            .map(|(value, count)| format!("{value} {count}"));
        let values: Vec<String> = values.collect();
        if !values.is_empty() {
            said.push(format!("{name} {}", values.join(", ")));
        }
    }
    let text = match (found.is_empty(), said.is_empty()) {
        (true, _) => format!("no {label} now"),
        (false, true) => format!("{} {label}, with no {}", found.len(), columns.join(" or ")),
        (false, false) => said.join("; "),
    };
    Read {
        text,
        judged: Judged::Counted,
        marks: Vec::new(),
        source: format!(
            "the columns {} of each part of {label}, read from {fields}",
            columns.join(", ")
        ),
    }
}
