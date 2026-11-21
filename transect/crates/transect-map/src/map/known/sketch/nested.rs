//! A part drawn as an elevation of its own kind, such as each shelf on
//! the page of its room. `Plan::build` draws it, with the roles of the
//! part's kind read against the part, so a nested elevation is drawn by
//! the code that draws every elevation. After its last column, the part
//! draws its label and its name, as the head of a sketch names its
//! object, and marks that line when the part deviates.
//!
//! `transect` nests one level. Inside a nested elevation, a role that
//! names an elevation draws each part in its shape.

use super::super::board::{Board, Subject};
use super::Plan;
use crate::shapes::text::{State, Why};
use transect_graph::links::Reference;

/// The widest that the name of a nested part stands, in pixels, and the
/// gap between it and the part's grid. The page writes both into the
/// part's style, and the grid around the part keeps room for them.
const NAME_WIDTH: f64 = 112.0;
const NAME_GAP: f64 = 16.0;

/// One part of a role, drawn as an elevation of the part's kind.
#[derive(Debug, PartialEq)]
pub struct NestedPart {
    /// The role's label, such as "shelf".
    pub label: String,
    /// The part's name, linked to its page.
    pub name: Reference,
    /// The fields that the part was read from.
    pub source: String,
    pub state: State,
    /// Why the part's object is failing, warning, or at rest.
    pub why: Option<Why>,
    pub plan: Plan,
    /// The name of the `KindKnowledge` that states the elevation.
    pub knowledge: String,
}

impl NestedPart {
    /// `subject` drawn as its kind's elevation named `elevation`, or
    /// none when the knowledge of its kind states no elevation of that
    /// name, so the part is drawn in its shape.
    pub fn build(
        board: &Board,
        subject: &Subject,
        elevation: &str,
        name: Reference,
        label: &str,
        source: &str,
    ) -> Option<Self> {
        let stated = subject.knowledge.own_elevation(elevation)?;
        let (state, why) = (board.standing)(&subject.object).state();
        Some(Self {
            label: label.into(),
            name,
            source: source.into(),
            state,
            why,
            plan: Plan::build(board, subject, stated, true),
            knowledge: subject.knowledge.name.clone(),
        })
    }

    /// The class of the part: marked as the head of a sketch is marked
    /// for a failing or warning object. The parts inside show whether
    /// each is at rest.
    pub fn class(&self) -> &'static str {
        match self.state {
            State::Failing => "sketch-nested sketch-failing",
            State::Warning | State::NoObject => "sketch-nested sketch-warning",
            _ => "sketch-nested",
        }
    }

    /// The part's style: the width of its name and the gap before it.
    pub fn style(&self) -> String {
        format!("--name-width: {NAME_WIDTH}px; column-gap: {NAME_GAP}px")
    }

    /// The narrowest width of the part: its grid's and its name's.
    pub fn narrowest(&self) -> f64 {
        self.plan.narrowest + NAME_GAP + NAME_WIDTH
    }

    /// The state as a word for a screen reader, as a part has one.
    pub fn spoken(&self) -> Option<&'static str> {
        self.state.spoken()
    }
}
