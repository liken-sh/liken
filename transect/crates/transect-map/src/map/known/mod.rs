//! The page of a custom resource drawn through its kind's knowledge.  The
//! object is drawn in the middle with its conditions as chips, and each
//! of its knowledge's references is a box around it with its label, what
//! the field names, and the field it was read from, so a reader sees
//! every relation that the operator declares for the kind, and where each
//! was read.  A reference whose field is not written yet keeps its box
//! and states that, so the board keeps its shape while an operator fills
//! in status.  The column on the left lists the kind's other objects in
//! the namespace and the counts of its known kinds that run nothing.
//! The card beside it is the workload's card when the object runs
//! pods, and its knowledge alone when it does not.

mod chips;
mod fit;
mod layout;

use askama::Template;

use super::EdgeRow;
use crate::cards::WorkloadCard;
use crate::objects::{Center, ObjectBoard, ObjectCard, Section};
pub use chips::{Chip, chips, every_chip};
use layout::{Place, Rect, places};
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
    /// The page's own path, which its event stream's URL includes.
    pub path: String,
    /// The kind's objects in the namespace, this one among them.
    pub column: Vec<Entry>,
    pub counts: Vec<Count>,
    pub chips: Vec<Chip>,
    pub level: Level,
    /// Why it is at rest, when its condition says so, as a gray line
    /// under its name.
    pub resting: Option<String>,
    /// Its lenses, for an object that runs pods.
    pub tabs: Option<Tabs>,
    pub width: i32,
    pub height: i32,
    pub center: Rect,
    pub boxes: Vec<RelationBox>,
    /// The workload's card, for an object that runs pods.
    pub card: Option<WorkloadCard>,
    /// Its object's card, for an object that runs no pods.
    pub known: Option<ObjectCard>,
    /// The boxes every object page shows: under the diagram in a row,
    /// or, for a kind whose knowledge names no references, around the
    /// object in place of the diagram.
    pub board: ObjectBoard,
}

/// One object in the column.
#[derive(Debug, PartialEq)]
pub struct Entry {
    pub text: String,
    pub href: Option<String>,
    pub current: bool,
}

/// One reference's box: its label, each end its field names, and the
/// field.
#[derive(Debug, PartialEq)]
pub struct RelationBox {
    pub rect: Rect,
    pub arrow: &'static str,
    pub line: Option<String>,
    /// How its line is drawn: solid for structure, dashed for what the
    /// object calls, and faint for a value that names no object.
    pub line_class: &'static str,
    pub label: String,
    pub ends: Vec<End>,
    pub source: String,
}

/// One value in a reference's field.
#[derive(Debug, PartialEq)]
pub struct End {
    pub reference: Reference,
    /// What it represents on the map when that is something other than
    /// itself, such as the workload behind an address.
    pub under: Option<String>,
    /// The objects it reaches, which `under` names, each linked to its
    /// page.
    pub reaches: Vec<Reference>,
}

/// What the board draws, read for one object.
pub struct Board<'a> {
    pub object: &'a Object,
    pub knowledge: &'a KindKnowledge,
    pub layer: String,
    pub path: String,
    /// Each relation in the object's fields, with what it reaches.
    pub reached: Vec<(&'a Relation, Reached)>,
    pub column: Vec<Entry>,
    pub counts: Vec<Count>,
    pub chips: Vec<Chip>,
    pub level: Level,
    pub resting: Option<String>,
    pub plurals: &'a Plurals,
    /// The page of any object that has one.
    pub page: &'a dyn Fn(&Object) -> Option<String>,
    /// The page of a ground box, by its title, when it has one.
    pub ground: &'a dyn Fn(&str) -> Option<String>,
    /// A device as a line names it, by its title.
    pub device: &'a dyn Fn(&transect_graph::edges::Device) -> String,
}

impl KnownPage {
    /// The page of `board`'s object, with the workload's card when it
    /// runs pods and `object_card` when it does not.
    pub fn build(board: Board, card: Option<WorkloadCard>, object_card: ObjectCard) -> Self {
        let object = board.object;
        let namespace = object.namespace.clone().unwrap_or_default();
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
        // The board draws every relation its fields name, so the card
        // leaves them out and keeps what the kind is and when it works.
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
            card,
            known,
            board: ObjectBoard::build(None, Vec::new()),
        }
    }

    /// The page with the boxes every object page shows.  When the only
    /// references are those that say what the object is part of, which
    /// nest, the object takes the middle of the boxes as `center`.
    /// Otherwise the diagram draws the object, and the boxes leave out
    /// what the diagram's boxes already name.
    pub fn with_board(self, center: Center, sections: Vec<Section>) -> Self {
        if self.boxes.is_empty() {
            let board = ObjectBoard::build(Some(center), sections);
            return Self { board, ..self };
        }
        let drawn: Vec<&Reference> = self
            .boxes
            .iter()
            .flat_map(|relation| relation.ends.iter().map(|end| &end.reference))
            .collect();
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

    /// The page with its board nested in what its object is part of,
    /// around its parts.
    pub fn with_nesting(self, nesting: crate::objects::Nesting) -> Self {
        let board = self.board.with_nesting(nesting);
        Self { board, ..self }
    }

    /// Whether the diagram draws the object, with a box per reference.
    pub fn diagram(&self) -> bool {
        !self.boxes.is_empty()
    }

    /// The page with the main column's row of edge kinds.
    pub fn with_edge_row(self, edge_row: EdgeRow) -> Self {
        Self { edge_row, ..self }
    }

    /// The crumb: the cluster leads home, the layer back to the cluster
    /// map with this namespace selected, and the namespace to its page.
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

    /// The object's box takes the failure fill or the warning outline,
    /// as a box on the namespace page does.
    pub fn center_class(&self) -> &'static str {
        match self.level {
            Level::Failing => "known-object known-failing",
            Level::Warning => "known-object known-warning",
            _ => "known-object",
        }
    }
}

/// The line that closes the object's box.
const CLOSING: &str = "Each relation names the field it was read from.";

impl RelationBox {
    /// The box moved to its place on the board.
    fn at(self, place: Place) -> Self {
        Self {
            rect: place.rect,
            arrow: place.arrow,
            line: place.line,
            ..self
        }
    }
}

/// The box of the reference at `path`, with each value in its field,
/// before the board places it.
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

/// One value, linked when it names an object with a page or a Node,
/// whose page is its machine's, and what it represents when that is
/// something else.
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
    // An address that reaches one thing with a page links to it, since
    // that thing is what a reader following the address wants to open.
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
