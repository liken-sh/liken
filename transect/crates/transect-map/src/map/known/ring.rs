//! The ring of boxes: the page of an object whose kind states no
//! elevation draws a box for each reference around the object, and
//! `layout` places them.

use super::layout::{Place, Rect};
use super::{Board, end::end};
use crate::shapes::text::End;
use transect_graph::edges::Kind;

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

impl RelationBox {
    pub fn at(self, place: Place) -> Self {
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
pub fn relation_box(board: &Board, path: &str, label: String, edge: Option<Kind>) -> RelationBox {
    let here = board.subject.object.namespace.as_deref();
    let ends = board
        .subject
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
