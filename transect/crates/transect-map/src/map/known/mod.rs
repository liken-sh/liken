//! The page of a custom resource whose kind has a `KindKnowledge`, the
//! resource that tells `transect` which fields of the kind refer to other
//! objects and which condition reports its health. The object is drawn
//! in the middle with its conditions as chips, and each reference that
//! the `KindKnowledge` lists is a box around it. A reference
//! whose field is empty still gets its box, so the diagram keeps its
//! shape while an operator fills in status. The column on the left
//! lists the kind's objects in the namespace. When the knowledge
//! states elevations, the page draws the sketch of one in place of the
//! ring: the first at the object's URL, and each at a URL of its own.

mod board;
mod chips;
mod end;
mod fit;
mod layout;
mod ring;
mod sketch;

use askama::Template;

use super::EdgeRow;
use crate::cards::WorkloadCard;
use crate::objects::{Center, ObjectBoard, ObjectCard, Section};
pub use crate::shapes::text::{End, Reading, Why};
pub use board::{Board, Standing, Subject};
pub use chips::{Chip, chips, every_chip};
use layout::{Rect, places};
use ring::{RelationBox, relation_box};
pub use sketch::{
    ElevationTab, Expected, Head, Lean, NestedPart, Plan, Sketch, SketchRole, SketchWire,
};
use transect_frame::frame::crumbs::Crumb;
use transect_frame::frame::tabs::{Lens, Tabs};
use transect_graph::edges::Kind;
use transect_graph::links::Reference;
use transect_observe::health::Level;
use transect_resources::knowledge::{Count, KnownThrough};

#[derive(Template)]
#[template(path = "map/known/known.html", blocks = ["main"])]
pub struct KnownPage {
    pub edge_row: EdgeRow,
    pub namespace: String,
    pub layer: String,
    pub kind: String,
    pub name: String,
    /// The page's path, which is part of its event stream URL: the
    /// object's, or one of its elevations'.
    pub path: String,
    /// The kind's objects in the namespace, this one among them.
    pub column: Vec<Entry>,
    pub counts: Vec<Count>,
    pub chips: Vec<Chip>,
    pub level: Level,
    /// The message or reason of the kind's health condition, when the
    /// condition is `False` with a reason that the knowledge lists in
    /// `healthy.calm`, such as `Idle`. Shown as a gray line under the
    /// object's name.
    pub resting: Option<String>,
    /// The lens tabs, for an object that runs pods.
    pub tabs: Option<Tabs>,
    pub width: i32,
    pub height: i32,
    pub center: Rect,
    pub boxes: Vec<RelationBox>,
    /// The elevation of the kind's knowledge that the URL names, or its
    /// first, which the page draws in place of the boxes when the
    /// knowledge states one.
    pub sketch: Option<Sketch>,
    /// The workload's card, for an object that runs pods.
    pub card: Option<WorkloadCard>,
    /// The object's card, for an object that runs no pods.
    pub known: Option<ObjectCard>,
    /// The boxes that every object page has. They are in a row under
    /// the diagram, or around the object when there is no diagram.
    pub board: ObjectBoard,
}

/// One object in the column.
#[derive(Debug, PartialEq)]
pub struct Entry {
    pub text: String,
    pub href: Option<String>,
    pub current: bool,
}

impl KnownPage {
    /// The page of `board`'s object, with the workload's card when it
    /// runs pods and `object_card` when it does not.
    pub fn build(board: Board, card: Option<WorkloadCard>, object_card: ObjectCard) -> Self {
        let object = &board.subject.object;
        let namespace = object.namespace.clone().unwrap_or_default();
        let described = card.as_ref().and_then(|card| card.known.as_ref());
        let described = described.or(object_card.known.as_ref());
        let knowledge = board.subject.knowledge;
        let author = described.map(KnownThrough::through).unwrap_or(Reference {
            text: knowledge.name.clone(),
            href: None,
        });
        let elevations = knowledge.stated();
        let chosen = sketch::choose(
            &elevations,
            board.elevation,
            &board.path,
            &object.kind,
            author,
        );
        let sketch = chosen.map(|(chosen, head)| Sketch::build(&board, chosen, &elevations, head));
        let mut references: Vec<(String, String, Option<Kind>)> = knowledge
            .references
            .iter()
            .filter(|r| r.edge != Kind::PartOf)
            .map(|r| {
                let label = r.label.clone().unwrap_or_else(|| r.edge.words().into());
                let edge = match r.target {
                    transect_resources::knowledge::Target::Value => None,
                    _ => Some(r.edge),
                };
                (r.path.clone(), label, edge)
            })
            .collect();
        if let Some(path) = &knowledge.writes_secret {
            references.push((path.clone(), "writes".into(), None));
        }
        // A sketch takes the place of the ring, so the page has no boxes.
        if sketch.is_some() {
            references.clear();
        }
        let unplaced: Vec<RelationBox> = references
            .into_iter()
            .map(|(path, label, edge)| relation_box(&board, &path, label, edge))
            .collect();
        let needs: Vec<i32> = unplaced
            .iter()
            .map(|b| fit::relation(&b.label, &b.ends, &b.source))
            .collect();
        let center_needs = fit::center(&object.kind, board.resting.as_deref(), &board.chips);
        let (width, height, center, placed) = places(&needs, center_needs);
        let boxes = unplaced
            .into_iter()
            .zip(placed)
            .map(|(b, place)| b.at(place))
            .collect();
        let tabs = card.as_ref().map(|card| {
            let tabs = Tabs::workload(&board.path, Lens::Overview, false);
            match card.vulnerabilities.is_some() {
                true => tabs.with_vulnerabilities(&board.path, false),
                false => tabs,
            }
        });
        // The diagram already draws every relation, so the cards drop the
        // related objects from the kind's knowledge and keep the rest.
        let card = card.map(|card| WorkloadCard {
            known: card.known.map(KnownThrough::without_related),
            ..card
        });
        let known = match card {
            Some(_) => None,
            None => Some(ObjectCard {
                known: object_card.known.map(KnownThrough::without_related),
                ..object_card
            }),
        };
        Self {
            edge_row: EdgeRow::default(),
            namespace,
            layer: board.layer,
            kind: object.kind.clone(),
            name: object.name.clone(),
            path: match board.elevation {
                Some(slug) => sketch::href(&board.path, slug),
                None => board.path,
            },
            column: board.column,
            counts: board.counts,
            chips: board.chips,
            level: board.level,
            resting: board.resting,
            tabs,
            width,
            height,
            center,
            boxes,
            sketch,
            card,
            known,
            board: ObjectBoard::build(None, Vec::new()),
        }
    }

    /// Adds the boxes that every object page has. Without a diagram, the
    /// boxes draw the object in the middle as `center`. With one, they
    /// leave out each value that the diagram already shows.
    pub fn with_board(self, center: Center, sections: Vec<Section>) -> Self {
        if !self.diagram() {
            let board = ObjectBoard::build(Some(center), sections);
            return Self { board, ..self };
        }
        let sections = sections
            .into_iter()
            .map(|section| section.without(|value| self.draws(value)));
        let board = ObjectBoard::build(None, sections.collect());
        Self { board, ..self }
    }

    /// Nests the boxes inside what the object is part of, and around the
    /// object's parts. A sketch draws what the object is made of, so a
    /// part that the sketch draws is not nested again, and what the
    /// object is part of stays.
    pub fn with_nesting(self, nesting: crate::objects::Nesting) -> Self {
        let nesting = match &self.sketch {
            Some(_) => nesting.without(|value| self.draws(value)),
            None => nesting,
        };
        let board = self.board.with_nesting(nesting);
        Self { board, ..self }
    }

    /// Whether the diagram draws `value`: the same page when both link,
    /// and the same text when either does not.
    fn draws(&self, value: &Reference) -> bool {
        let in_boxes = self.boxes.iter().flat_map(|relation| &relation.ends);
        let in_boxes = in_boxes.map(|end| &end.reference);
        let in_sketch = self
            .sketch
            .iter()
            .flat_map(|sketch| sketch.plan.references());
        let mut drawn = in_boxes.chain(in_sketch);
        drawn.any(|end| end::same(end, value))
    }

    /// Whether the page draws a diagram: the ring of boxes or a sketch.
    pub fn diagram(&self) -> bool {
        !self.boxes.is_empty() || self.sketch.is_some()
    }

    /// Adds the row of edge kinds above the main column.
    pub fn with_edge_row(self, edge_row: EdgeRow) -> Self {
        Self { edge_row, ..self }
    }

    /// The breadcrumbs. The layer's breadcrumb links to the cluster map
    /// with this namespace selected.
    pub fn crumbs(&self) -> Vec<Crumb> {
        let select = format!("/?select={}", self.namespace);
        vec![
            Crumb::home(),
            Crumb::link(&self.layer, format!("{select}#{}", self.layer)),
            Crumb::link_name(&self.namespace, format!("/namespaces/{}", self.namespace)),
            Crumb::current_name(&transect_graph::edges::kind_and_name(
                &self.kind, &self.name,
            )),
        ]
    }

    pub fn events_url(&self) -> String {
        format!("/events{}", self.path)
    }

    pub fn closing(&self) -> &'static str {
        CLOSING
    }

    /// The CSS class of the object's box: filled for a failing object
    /// and outlined for a warning, as a box on the namespace page is.
    pub fn center_class(&self) -> &'static str {
        match self.level {
            Level::Failing => "known-object known-failing",
            Level::Warning => "known-object known-warning",
            _ => "known-object",
        }
    }

    /// The CSS class of the sketch's head, which marks the object's
    /// health as `center_class` does on the ring.
    pub fn sketch_head_class(&self) -> &'static str {
        match self.level {
            Level::Failing => "sketch-head sketch-failing",
            Level::Warning => "sketch-head sketch-warning",
            _ => "sketch-head",
        }
    }
}

const CLOSING: &str = "Each relation names the field it was read from.";

#[cfg(test)]
mod tests;
