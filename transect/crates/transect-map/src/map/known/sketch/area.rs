//! One role in its area of the grid: its parts, each in the role's shape
//! or as an elevation of the part's kind, or its empty place, and the
//! class that says where its parts stand in the area.

use super::super::board::{Board, Subject};
use super::super::end::refer;
use super::fill::{self, Found};
use super::nested::NestedPart;
use super::tracks::{Contact, Footprint, Joins};
use super::{Lean, empty, fields, part, stack};
use crate::shapes::text::{Drawn, Part};
use crate::shapes::{Drawing, Pose, stacked};
use transect_graph::edges::{Endpoint, Object, kind_and_name};
use transect_graph::links::Reference;
use transect_resources::knowledge::Grid;
use transect_resources::knowledge::catalog::Marks;
use transect_resources::knowledge::role::Role;
use transect_resources::shapes::{Edge, Shape};

/// One role and its parts, in the role's area of the grid.
#[derive(Debug, PartialEq)]
pub struct SketchRole {
    /// The role's name, which is also its `grid-area`.
    pub area: String,
    pub lean: Lean,
    /// How the role's area meets its neighbors.
    pub contact: Contact,
    /// Whether the role's parts touch each other, because their shape
    /// joins on its left and its right.
    pub abutting: bool,
    /// The role's parts in its shape, and its empty place.
    pub drawn: Drawn,
    /// The role's parts drawn as an elevation of their kind.
    pub nested: Vec<NestedPart>,
}

/// What a role draws, before `Plan::build` lays out the grid.
pub struct Fills<'a, 's> {
    /// The role's parts that no earlier role of the elevation draws.
    pub parts: &'a [Found<'s>],
    /// Each object of the role that an earlier role draws, with that
    /// role's label.
    pub elsewhere: &'a [(Object, String)],
    /// Whether every part of the role is the object of the page, which a
    /// nested elevation leaves out.
    pub only_the_page: bool,
    /// Whether a wire meets the role, so its ports are marked.
    pub wired: bool,
    /// The marks of the page's pattern, when the elevation is the
    /// pattern's own and not drawn inside a part, because only that
    /// elevation lists what the pattern expects.
    pub marks: Option<&'a Marks>,
    /// Whether the elevation is drawn inside a part of the page's own
    /// elevation. `transect` nests one level, so a role of a nested
    /// elevation draws each part in its shape.
    pub nested: bool,
}

impl SketchRole {
    /// The class of the role's area: the edge that its parts stand
    /// against, how they touch their neighbors, and whether they stand
    /// on the axis of their row. In a row on one axis, the parts of a
    /// shape that joins on its left and its right fill their area, so
    /// they touch a neighbor on either side at every width. Other parts
    /// stand against the side where they touch a neighbor.
    pub fn area_class(&self) -> String {
        let contact = &self.contact;
        let fill = contact.axis && self.abutting;
        let mut class = match (fill, contact.left, contact.right) {
            (true, _, _) => "sketch-area sketch-fill",
            (false, true, _) => Lean::Start.area_class(),
            (false, false, true) => Lean::End.area_class(),
            (false, false, false) => self.lean.area_class(),
        }
        .to_string();
        let flags = [
            (contact.above, " sketch-joined-above"),
            (contact.below, " sketch-joined-below"),
            (contact.top_row, " sketch-top-row"),
            (contact.bottom_row, " sketch-bottom-row"),
            (self.abutting, " sketch-abutting"),
            (contact.axis, " sketch-on-axis"),
            (contact.free_top, " sketch-free-top"),
            (contact.free_bottom, " sketch-free-bottom"),
        ];
        for (on, name) in flags {
            if on {
                class.push_str(name);
            }
        }
        class
    }
}

/// `role` as the page draws it in `subject`'s elevation, with the edges
/// on which its parts join a neighbor and the narrowest width that its
/// area keeps, or none when the page leaves its area blank.
pub fn draw(
    board: &Board,
    subject: &Subject,
    grid: &Grid,
    role: &Role,
    fills: Fills,
) -> Option<(SketchRole, Footprint)> {
    let references = &subject.knowledge.references;
    let label = role.label(references);
    let source = source(board, subject, role, fills.elsewhere);
    let source_text = fields::text(&source);
    let shows = part::shows(board, &subject.object, &role.shows);
    let as_elevation = role.elevation.as_deref();
    let as_elevation = as_elevation.filter(|_| !fills.nested && role.unresolved().is_none());
    let here = subject.object.namespace.as_deref();
    let mut nested = Vec::new();
    let mut parts: Vec<Part> = Vec::new();
    for found in fills.parts {
        let object = found.object().filter(|_| as_elevation.is_some());
        let inner = object.and_then(|object| (board.subjects)(object));
        let built = inner.and_then(|inner| {
            let name = refer(&Endpoint::Object(inner.object.clone()), here, board);
            let name = part::named(&label, &inner.object, name);
            NestedPart::build(board, &inner, as_elevation?, name, &label, &source_text)
        });
        match built {
            Some(one) => nested.push(one),
            None => parts.push(part::part(
                board,
                &subject.object,
                role,
                &label,
                &shows,
                found,
                fills.marks,
            )),
        }
    }
    if let Some(unresolved) = role.unresolved() {
        for part in parts.iter_mut().filter(|part| part.end.under.is_none()) {
            part.end.under = Some(unresolved.into());
        }
    }
    if role.stack {
        parts = stack::gather(parts);
    }
    let empty = parts.is_empty() && nested.is_empty();
    if empty && fills.only_the_page {
        return None;
    }
    let elsewhere = (!fills.elsewhere.is_empty()).then_some(source_text.as_str());
    let nothing = match empty {
        true => Some(empty::nothing(
            subject,
            role,
            elsewhere,
            fill::derived(board, subject, role),
        )?),
        false => None,
    };
    let columns = grid.columns_of(&role.name).unwrap_or_default();
    let shape = grid.posed(&role.name, &role.drawn);
    let footprint = footprint(&shape, &parts, nothing.is_some(), &nested);
    let pose = Pose {
        ports: fills.wired,
        ..Pose::default()
    };
    let drawn = SketchRole {
        area: role.name.clone(),
        lean: Lean::of(columns, grid.columns()),
        contact: Contact::default(),
        abutting: shape.joins(Edge::Left) && shape.joins(Edge::Right),
        drawn: Drawn {
            drawing: Drawing::of(&shape, pose),
            stacked: stacked(&shape, pose),
            label,
            source,
            parts,
            nothing,
        },
        nested,
    };
    Some((drawn, footprint))
}

/// What the role's area needs in the grid. A part in its shape needs its
/// shape's narrowest width, and the room that a piece outside its box
/// takes on either side, because the part can stand in the middle of
/// its area. A part drawn as an elevation needs the narrowest width of
/// its grid and its name. An area with both kinds of part joins an edge
/// only when both join it, and keeps the wider of the two widths.
fn footprint(shape: &Shape, parts: &[Part], empty: bool, nested: &[NestedPart]) -> Footprint {
    let in_shape = (!parts.is_empty() || empty).then(|| {
        let [narrowest, _] = shape.size.width;
        let scale = shape
            .size
            .design
            .map_or(1.0, |[width, _]| narrowest / width);
        Footprint {
            joins: Joins {
                top: shape.joins(Edge::Top),
                bottom: shape.joins(Edge::Bottom),
                left: shape.joins(Edge::Left),
                right: shape.joins(Edge::Right),
            },
            narrowest: narrowest + 2.0 * shape.beyond().0 * scale,
        }
    });
    let as_elevation = match (nested.first(), nested.last()) {
        (Some(first), Some(last)) => Some(Footprint {
            joins: Joins {
                top: first.plan.joins.top,
                bottom: last.plan.joins.bottom,
                left: nested.iter().all(|n| n.plan.joins.left),
                right: nested.iter().all(|n| n.plan.joins.right),
            },
            narrowest: nested.iter().map(NestedPart::narrowest).fold(0.0, f64::max),
        }),
        _ => None,
    };
    match (in_shape, as_elevation) {
        (Some(a), Some(b)) => Footprint {
            joins: Joins {
                top: a.joins.top && b.joins.top,
                bottom: a.joins.bottom && b.joins.bottom,
                left: a.joins.left && b.joins.left,
                right: a.joins.right && b.joins.right,
            },
            narrowest: a.narrowest.max(b.narrowest),
        },
        (one, other) => one.or(other).unwrap_or_default(),
    }
}

/// Where the role's parts were read: "from Shelf spec.room", followed by
/// each object that an earlier role draws, as "except Shelf top, which
/// shelf draws", as words and links.
fn source(
    board: &Board,
    subject: &Subject,
    role: &Role,
    elsewhere: &[(Object, String)],
) -> Vec<Reference> {
    let mut line = vec![fields::words("from ")];
    line.extend(fields::fields(board, subject, role));
    let others: Vec<String> = elsewhere
        .iter()
        .map(|(object, label)| {
            let named = kind_and_name(&object.kind, &object.name);
            format!("{named}, which {label} draws")
        })
        .collect();
    if let Some((last, first)) = others.split_last() {
        let listed = match first {
            [] => last.clone(),
            _ => format!("{}, and {last}", first.join(", ")),
        };
        line.push(fields::words(&format!(", except {listed}")));
    }
    line
}
