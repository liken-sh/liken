//! The drawing of a sequence: a lane for each role, with its parts at
//! the top, and each step as an arrow from a lane to a lane, in the
//! author's order, with what the cluster says about the step beside it.
//! A sequence explains a system whose parts take turns, where a picture
//! of the parts in place does not show who acts first.
//!
//! After the stated lanes, the drawing adds a lane for each program that
//! accepts an audience that the page object's templates present, so a
//! project that ships the knowledge of a program adds its lane to every
//! sequence whose pods call it.

mod added;
mod state;

use askama::Template;

use super::super::board::{Board, Standing};
use super::fields::{self, text};
use super::fill::{self, Found};
use super::part::end_of;
use transect_graph::links::Reference;
use transect_resources::knowledge::Stated;
use transect_resources::knowledge::role::Role;
use transect_resources::knowledge::sequence;

/// The drawing of one sequence.
#[derive(Debug, PartialEq, Template)]
#[template(path = "map/known/sequence.html")]
pub struct Sequence {
    /// The elevation's name, which names the drawing for a screen
    /// reader.
    pub name: String,
    /// The stated lanes, then the added ones.
    pub lanes: Vec<Lane>,
    pub steps: Vec<Step>,
}

/// One lane: a role of the page object's kind, or a program that the
/// knowledge of another project describes.
#[derive(Debug, PartialEq)]
pub struct Lane {
    pub label: String,
    pub parts: Vec<Reference>,
    /// The fields that the parts are read from, or the knowledge that
    /// adds the lane.
    pub source: String,
    /// What the lane says when it has no part.
    pub note: Option<String>,
    pub kind: LaneKind,
}

/// Where a lane comes from.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum LaneKind {
    /// A role that the sequence states.
    Stated,
    /// A program that accepts an audience of the templates.
    Added,
    /// An audience that no loaded knowledge accepts.
    Unknown,
}

/// One arrow, from the lane at `from` to the lane at `to`, by their
/// places from the left.
#[derive(Debug, PartialEq)]
pub struct Step {
    pub from: usize,
    pub to: usize,
    pub verb: String,
    /// What the cluster says about the step, such as "Ready 4/4".
    pub state: String,
    pub judged: Judged,
    /// Each part that marks the step, with why.
    pub marks: Vec<Mark>,
    /// Where the state was read from.
    pub source: String,
}

/// A part that is not as a step expects.
#[derive(Debug, PartialEq)]
pub struct Mark {
    pub part: Reference,
    /// What its condition says, such as "Connected is False,
    /// Unreachable".
    pub said: String,
}

/// What a step's state says.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Judged {
    /// Each part of its role is as the step expects.
    Met,
    /// A part of its role is not as the step expects.
    Marked,
    /// The step counts values and judges nothing.
    Counted,
    /// No signal names the step.
    Unsaid,
    /// The step presents an audience that no loaded knowledge accepts.
    Unknown,
}

impl Sequence {
    /// The sequence `sequence` of the page's object, whose roles are
    /// those of `stated`.
    pub fn build(board: &Board, stated: Stated, sequence: &sequence::Sequence) -> Self {
        let subject = &board.subject;
        let role_of = |name: &str| stated.roles.iter().find(|role| role.name == name);
        let mut lanes = Vec::new();
        let mut objects = Vec::new();
        for name in &sequence.lanes {
            let Some(role) = role_of(name) else { continue };
            let found = fill::found(board, subject, role);
            objects.push(found.iter().filter_map(Found::object).cloned().collect());
            lanes.push(stated_lane(board, role, &found));
        }
        let at = |name: &str| sequence.lanes.iter().position(|lane| lane == name);
        let mut steps: Vec<Step> = sequence
            .steps
            .iter()
            .filter_map(|step| {
                let (from, to) = (at(&step.from)?, at(&step.to)?);
                let read = state::read(board, stated.roles, step.state.as_ref());
                Some(Step {
                    from,
                    to,
                    verb: step.verb.clone(),
                    state: read.text,
                    judged: read.judged,
                    marks: read.marks,
                    source: read.source,
                })
            })
            .collect();
        if let Some(from) = sequence.presents.as_deref().and_then(at) {
            for one in added::lanes(board.presents, &objects, board.top) {
                let to = lanes.len();
                steps.extend(one.steps.into_iter().map(|step| Step {
                    from,
                    to,
                    verb: step.verb,
                    state: step.state,
                    judged: step.judged,
                    marks: Vec::new(),
                    source: step.source,
                }));
                let here = subject.object.namespace.as_deref();
                let parts = one.runs.iter().map(|object| {
                    let found = Found::Object(object.clone());
                    let reference = end_of(board, &subject.object, &found).reference;
                    Reference {
                        text: name(object, here),
                        ..reference
                    }
                });
                lanes.push(Lane {
                    label: one.label,
                    parts: parts.collect(),
                    source: one.source,
                    note: one.note,
                    kind: one.kind,
                });
            }
        }
        Self {
            name: stated.elevation.name.clone(),
            lanes,
            steps,
        }
    }

    /// Each object that the lanes name.
    pub fn references(&self) -> Vec<&Reference> {
        self.lanes.iter().flat_map(|lane| &lane.parts).collect()
    }

    /// The line under the drawing.
    pub fn caption(&self, knowledge: &str) -> String {
        let added = self.lanes.iter().any(|lane| lane.kind != LaneKind::Stated);
        let added = match added {
            true => {
                " A lane after the line is added for a program that accepts an audience of the object's pod templates, from that program's knowledge."
            }
            false => "",
        };
        format!(
            "Hover a step for the field its state was read from. The lanes and the order of the steps are from the KindKnowledge {knowledge}, and the parts and their states are from the Kubernetes API.{added}"
        )
    }

    /// The columns of the grid, one for each lane.
    pub fn columns(&self) -> String {
        format!(
            "repeat({}, minmax(var(--sequence-lane), 1fr))",
            self.lanes.len()
        )
    }

    /// The column where `step`'s cell starts and the column after it
    /// ends, as `grid-column` takes them, and the number of lanes that
    /// the cell spans. An arrow spans the lanes from its start to its
    /// end. A step that returns to its own lane also takes the lane to
    /// its right, when there is one, so its words have room beside the
    /// loop.
    pub fn place(&self, step: &Step) -> (usize, usize, usize) {
        let (first, last) = (step.from.min(step.to), step.from.max(step.to));
        let last = match step.from == step.to && last + 1 < self.lanes.len() {
            true => last + 1,
            false => last,
        };
        (first + 1, last + 2, last - first + 1)
    }

    /// The last row of the grid's lines, after the last step.
    pub fn last_row(&self) -> usize {
        self.steps.len() + 2
    }

    /// The place from the left of the first added lane, which a line
    /// sets apart from the stated lanes.
    pub fn first_added(&self) -> Option<usize> {
        self.lanes
            .iter()
            .position(|lane| lane.kind != LaneKind::Stated)
    }
}

impl Lane {
    pub fn unknown(&self) -> bool {
        self.kind == LaneKind::Unknown
    }

    pub fn class(&self) -> &'static str {
        match self.kind {
            LaneKind::Stated => "sequence-lane",
            LaneKind::Added => "sequence-lane sequence-added",
            LaneKind::Unknown => "sequence-lane sequence-unknown",
        }
    }
}

impl Step {
    pub fn class(&self) -> String {
        let direction = match self.from.cmp(&self.to) {
            std::cmp::Ordering::Less => "sequence-right",
            std::cmp::Ordering::Greater => "sequence-left",
            std::cmp::Ordering::Equal => "sequence-self",
        };
        let dashed = match self.judged {
            Judged::Unknown => " sequence-dashed",
            _ => "",
        };
        format!("sequence-arrow {direction}{dashed}")
    }

    pub fn state_class(&self) -> &'static str {
        match self.judged {
            Judged::Met => "sequence-said sequence-met",
            Judged::Marked => "sequence-said sequence-marked",
            Judged::Counted => "sequence-said sequence-counted",
            Judged::Unsaid => "sequence-said sequence-unsaid",
            Judged::Unknown => "sequence-said sequence-unknown-state",
        }
    }
}

/// The most parts that a lane's head names.  A `Mandate` of a busy
/// namespace has hundreds of `Session`s, and a head that names each one
/// pushes the steps far down the page.
pub const FEW: usize = 6;

/// The lane of `role`, whose parts are `found`.  A lane with more than
/// `FEW` parts names only the parts that are not healthy, and counts
/// the parts by the reason of their health, such as "Finished 160".
fn stated_lane(board: &Board, role: &Role, found: &[Found]) -> Lane {
    let subject = &board.subject;
    let here = subject.object.namespace.as_deref();
    let reference = |one: &Found| {
        let reference = end_of(board, &subject.object, one).reference;
        match one.object() {
            Some(object) => Reference {
                text: name(object, here),
                ..reference
            },
            None => reference,
        }
    };
    let (parts, note) = match found.len() {
        0 => (Vec::new(), Some("none running now".to_string())),
        count if count <= FEW => (found.iter().map(reference).collect(), None),
        count => {
            let mut tally: Vec<(String, usize)> = Vec::new();
            let mut named = Vec::new();
            for one in found {
                let standing = one.object().map(|object| (board.standing)(object));
                let (word, unhealthy) = match standing {
                    Some(Standing::Failing(why) | Standing::Warning(why)) => (why.reason, true),
                    Some(Standing::Resting(why)) => (why.reason, false),
                    Some(Standing::Missing) => ("missing".to_string(), true),
                    Some(Standing::Plain) | None => ("healthy".to_string(), false),
                };
                if unhealthy {
                    named.push(reference(one));
                }
                match tally.iter_mut().find(|(known, _)| *known == word) {
                    Some((_, n)) => *n += 1,
                    None => tally.push((word, 1)),
                }
            }
            tally.sort_by(|a, b| b.1.cmp(&a.1).then(a.0.cmp(&b.0)));
            let tally: Vec<String> = tally
                .iter()
                .map(|(word, n)| format!("{word} {n}"))
                .collect();
            (named, Some(format!("{count} in all: {}", tally.join(", "))))
        }
    };
    Lane {
        label: role.label(&subject.knowledge.references),
        parts,
        source: text(&fields::fields(board, subject, role)),
        note,
        kind: LaneKind::Stated,
    }
}

/// The name of `object` in a lane's head, whose label already says
/// what the parts are, with the namespace when it is not `here`.
fn name(object: &transect_graph::edges::Object, here: Option<&str>) -> String {
    match object.namespace.as_deref() {
        Some(namespace) if Some(namespace) != here => format!("{namespace}/{}", object.name),
        _ => object.name.clone(),
    }
}

#[cfg(test)]
mod tests;
