//! The sketch of a known kind: one elevation of its `KindKnowledge`, or
//! of the pattern of `transect`'s catalog that the kind fills, drawn in
//! place of the ring of boxes. The author states only where
//! each role is, its shape, which printer columns its parts show, and
//! what each wire carries. `transect` draws the state of each part and
//! each wire the same way in every sketch, so a failing part looks the
//! same on every kind's page.

mod area;
mod empty;
mod expected;
mod fields;
mod fill;
mod head;
mod lean;
mod nested;
mod part;
mod plan;
mod stack;
mod tracks;
mod unplaced;
mod widths;
mod wires;

pub use area::SketchRole;
pub use expected::Expected;
pub use head::{ElevationTab, Head, choose, href};
pub use lean::Lean;
pub use nested::NestedPart;
pub use plan::Plan;
pub use wires::SketchWire;

use super::board::Board;
use transect_graph::links::Reference;
use transect_resources::knowledge::Stated;

/// The drawing of one elevation of the page's object.
#[derive(Debug, PartialEq)]
pub struct Sketch {
    /// The elevation's name, such as "the room".
    pub name: String,
    pub question: String,
    /// The tabs of the kind's elevations, who states them, and the
    /// elevation that the URL named and the kind does not have.
    pub head: Head,
    /// The grid of the elevation, with its roles and its wires.
    pub plan: Plan,
    /// The line that names the parts of the roles that the elevation
    /// does not place, as words and links.
    pub unplaced: Option<Vec<Reference>>,
    /// The name of the `KindKnowledge` that states the elevation, or
    /// whose `pattern` fills the roles of an elevation of the catalog.
    pub knowledge: String,
    /// What the pattern expects, under an elevation of the catalog.
    pub expected: Vec<Expected>,
}

impl Sketch {
    /// The sketch of the elevation `chosen`, by its slug, for `board`'s
    /// object, under `head`. `all` is every elevation of the page.
    pub fn build(
        board: &Board,
        chosen: &(String, Stated),
        all: &[(String, Stated)],
        head: Head,
    ) -> Self {
        let (slug, stated) = chosen;
        let elevation = stated.elevation;
        let judged = board.judged.filter(|_| stated.pattern.is_some());
        let lines = judged.iter().flat_map(|judged| &judged.lines);
        let expected = lines.map(|line| Expected::of(line, board.page));
        let plan = Plan::build(board, &board.subject, *stated, false);
        let unplaced = unplaced::line(board, slug, *stated, all, &plan.references());
        Self {
            name: elevation.name.clone(),
            question: elevation.question.clone(),
            head,
            plan,
            unplaced,
            knowledge: board.subject.knowledge.name.clone(),
            expected: expected.collect(),
        }
    }

    /// The line under the sketch. A part drawn as an elevation of its
    /// kind has its places from the knowledge of that kind, so the line
    /// names that knowledge too. Under an elevation of the catalog, the
    /// line names the source of each kind of fact.
    pub fn caption(&self) -> String {
        if let Some(pattern) = &self.head.pattern {
            return format!(
                "Hover a part or an expectation for the field it was read from. The parts and their states are from the Kubernetes API. Which part has which role is from the KindKnowledge {}. The places, the shapes, and the expectations are from transect's pattern {pattern}, and /shapes draws each shape.",
                self.knowledge
            );
        }
        let mut nested: Vec<&str> = Vec::new();
        let parts = self.plan.roles.iter().flat_map(|role| &role.nested);
        for part in parts {
            if !nested.contains(&part.knowledge.as_str()) {
                nested.push(&part.knowledge);
            }
        }
        let inner = match nested.is_empty() {
            true => String::new(),
            false => format!(
                " The places inside a part drawn as an elevation of its kind are from the KindKnowledge {}.",
                nested.join(", ")
            ),
        };
        format!(
            "Hover a part for the field it was read from. The places are from the KindKnowledge {}.{inner} A part's shape is from it or from the KindKnowledge of the part's kind, and /shapes draws each shape.",
            self.knowledge
        )
    }
}

#[cfg(test)]
mod tests;
