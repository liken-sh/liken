//! The page of a custom resource whose kind has a `KindKnowledge`, the
//! resource that tells `transect` which fields of the kind refer to other
//! objects and which condition reports its health. The object is drawn
//! in the middle with its conditions as chips, and each reference that
//! the `KindKnowledge` lists is a box around it. A reference
//! whose field is empty still gets its box, so the diagram keeps its
//! shape while an operator fills in status. The column on the left
//! lists the kind's objects in the namespace. When the knowledge
//! states an elevation, the page draws its sketch in place of the ring.

mod chips;
mod fit;
mod layout;
mod sketch;

use askama::Template;

use super::EdgeRow;
use crate::cards::WorkloadCard;
use crate::objects::{Center, ObjectBoard, ObjectCard, Section};
pub use crate::shapes::text::{End, Reading, Why};
pub use chips::{Chip, chips, every_chip};
use layout::{Place, Rect, places};
pub use sketch::{Lean, Sketch, SketchRole, Standing};
use transect_frame::frame::crumbs::Crumb;
use transect_frame::frame::tabs::{Lens, Tabs};
use transect_graph::edges::{Endpoint, Kind, Object, Plurals};
use transect_graph::links::Reference;
use transect_observe::health::Level;
use transect_resources::knowledge::{Count, KindKnowledge, KnownThrough, Names, Reached, Relation};

#[derive(Template)]
#[template(path = "map/known/known.html", blocks = ["main"])]
pub struct KnownPage {
    pub edge_row: EdgeRow,
    pub namespace: String,
    pub layer: String,
    pub kind: String,
    pub name: String,
    /// The page's path, which is part of its event stream URL.
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
    /// The first elevation of the kind's knowledge, which the page draws
    /// in place of the boxes when the knowledge states one.
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

/// The box for one reference: its label, the values in its field, and
/// in `source`, the path of that field.
#[derive(Debug, PartialEq)]
pub struct RelationBox {
    pub rect: Rect,
    pub arrow: &'static str,
    pub line: Option<String>,
    /// How the line is drawn: dashed for `DependsOn` and
    /// `AuthenticatesThrough` references, faint for a reference whose
    /// target is a value and for the `Secret` that `writes_secret` names,
    /// and solid for the rest.
    pub line_class: &'static str,
    pub label: String,
    pub ends: Vec<End>,
    pub source: String,
}

/// What `KnownPage::build` needs to draw the page of one object.
pub struct Board<'a> {
    pub object: &'a Object,
    pub knowledge: &'a KindKnowledge,
    pub layer: String,
    pub path: String,
    /// Each relation read from the object's fields, with what it
    /// resolves to.
    pub reached: Vec<(&'a Relation, Reached)>,
    pub column: Vec<Entry>,
    pub counts: Vec<Count>,
    pub chips: Vec<Chip>,
    pub level: Level,
    pub resting: Option<String>,
    pub plurals: &'a Plurals,
    /// The page URL of an object, when it has a page.
    pub page: &'a dyn Fn(&Object) -> Option<String>,
    /// The URL of the ground page of a system outside the cluster, by
    /// the name of its `Endpoint::Outside`, when it has one.
    pub ground: &'a dyn Fn(&str) -> Option<String>,
    /// The text that names a device on a line.
    pub device: &'a dyn Fn(&transect_graph::edges::Device) -> String,
    /// What the cluster says about an object that a part of the sketch
    /// names.
    pub standing: &'a dyn Fn(&Object) -> Standing,
    /// The readings of an object: each of the printer columns named in
    /// the list that has a value, in the list's order. A sketch reads
    /// the objects that its parts name and the page's own object.
    pub readings: &'a dyn Fn(&Object, &[String]) -> Vec<Reading>,
}

impl KnownPage {
    /// The page of `board`'s object, with the workload's card when it
    /// runs pods and `object_card` when it does not.
    pub fn build(board: Board, card: Option<WorkloadCard>, object_card: ObjectCard) -> Self {
        let object = board.object;
        let namespace = object.namespace.clone().unwrap_or_default();
        let elevation = board.knowledge.elevations.first();
        let sketch = elevation.map(|elevation| Sketch::build(&board, elevation));
        let mut references: Vec<(String, String, Option<Kind>)> = board
            .knowledge
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
        if let Some(path) = &board.knowledge.writes_secret {
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
            path: board.path,
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
    /// leave out each value that a relation box already shows.
    pub fn with_board(self, center: Center, sections: Vec<Section>) -> Self {
        if !self.diagram() {
            let board = ObjectBoard::build(Some(center), sections);
            return Self { board, ..self };
        }
        let in_boxes = self.boxes.iter().flat_map(|relation| &relation.ends);
        let roles = self.sketch.iter().flat_map(|sketch| &sketch.roles);
        let in_parts = roles.flat_map(|role| role.drawn.parts.iter().map(|part| &part.end));
        let drawn: Vec<&Reference> = in_boxes.chain(in_parts).map(|end| &end.reference).collect();
        let shown = |value: &Reference| {
            drawn.iter().any(|end| match (&end.href, &value.href) {
                (Some(drawn), Some(href)) => drawn == href,
                _ => end.text == value.text,
            })
        };
        let sections = sections.into_iter().map(|section| section.without(shown));
        let board = ObjectBoard::build(None, sections.collect());
        Self { board, ..self }
    }

    /// Nests the boxes inside what the object is part of, and around the
    /// object's parts.
    pub fn with_nesting(self, nesting: crate::objects::Nesting) -> Self {
        let board = self.board.with_nesting(nesting);
        Self { board, ..self }
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

impl RelationBox {
    fn at(self, place: Place) -> Self {
        Self {
            rect: place.rect,
            arrow: place.arrow,
            line: place.line,
            ..self
        }
    }
}

/// The box for the reference at `path`, with a value for each end,
/// before `places` assigns its position.
fn relation_box(board: &Board, path: &str, label: String, edge: Option<Kind>) -> RelationBox {
    let here = board.object.namespace.as_deref();
    let ends = board
        .reached
        .iter()
        .filter(|(relation, _)| relation.path == path)
        .map(|(relation, reached)| end(relation, reached, here, board))
        .collect();
    let line_class = match edge {
        None => "section-path known-path-value",
        Some(Kind::DependsOn | Kind::AuthenticatesThrough) => "section-path section-path-dashed",
        Some(_) => "section-path",
    };
    RelationBox {
        rect: Rect::default(),
        arrow: "",
        line: None,
        line_class,
        label,
        ends,
        source: format!("from {path}"),
    }
}

/// One value of a reference's field. It links to the page of the object
/// it names when that object has one, and a Node links to its machine's
/// page. `under` is the note under the value: what the value resolves
/// to, or why it resolves to nothing.
fn end(relation: &Relation, reached: &Reached, here: Option<&str>, board: &Board) -> End {
    let refer = |end: &Endpoint| {
        let mut reference = Reference::to_end(end, here, board.plurals);
        reference.href = match end {
            Endpoint::Object(object)
                if (object.group.as_str(), object.kind.as_str()) == ("", "Node") =>
            {
                Some(format!("/machines/{}", object.name))
            }
            Endpoint::Object(object) => (board.page)(object),
            Endpoint::Machine(machine) => Some(transect_graph::links::machine(machine)),
            Endpoint::Outside(name) => (board.ground)(name),
            Endpoint::Device(device) => transect_graph::links::device(device),
        };
        if let Endpoint::Device(device) = end {
            reference.text = (board.device)(device);
        }
        reference
    };
    let (reference, itself) = match &relation.names {
        Names::Object(object) => {
            let end = Endpoint::Object(object.clone());
            (refer(&end), Some(end))
        }
        Names::Address | Names::Value | Names::Ambiguous(_) => (
            Reference {
                text: relation.value.clone(),
                href: None,
            },
            None,
        ),
    };
    let others: Vec<Reference> = reached
        .ends
        .iter()
        .filter(|end| Some(*end) != itself.as_ref())
        .map(refer)
        .collect();
    // An address that resolves to exactly one endpoint links to that
    // endpoint's page, because that is the page a reader who follows the
    // address is looking for.
    let reference = match (&relation.names, &others[..]) {
        (Names::Address, [one]) => Reference {
            href: one.href.clone(),
            ..reference
        },
        _ => reference,
    };
    let texts: Vec<&str> = others.iter().map(|other| other.text.as_str()).collect();
    let under = match (&relation.names, others.is_empty()) {
        (Names::Value, _) => Some("not an object in the cluster".into()),
        (Names::Ambiguous(count), _) => Some(format!("matches {count} objects, so none is drawn")),
        (Names::Address, true) => Some("an address transect does not resolve".into()),
        (_, true) => None,
        (_, false) => Some(format!("reaches {}", texts.join(", "))),
    };
    let reaches = match relation.names {
        Names::Value => Vec::new(),
        _ => others,
    };
    End {
        reference,
        under,
        reaches,
    }
}

#[cfg(test)]
mod tests;
