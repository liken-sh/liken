//! The `/shapes` page draws every shape that `transect` has loaded, in
//! every state, with the same texts on every shape: the library's
//! shapes by name, then each author's, under the file it came from. A
//! reader compares shapes with the same words, and an author sees a new
//! shape in each state before a sketch uses it. Each shape lists the
//! kinds whose knowledge states it, so a reader sees what it draws in
//! this cluster. The last section lists each `Shape` document that was
//! left out, with its condition.

mod samples;

use std::collections::BTreeMap;

use askama::Template;

use super::text::Drawn;
use super::{Drawing, Pose, stacked};
use samples::{CELLS, Sample};
use transect_graph::links::{self, Reference};
use transect_resources::knowledge::Kinds;
use transect_resources::resources::Directory;
use transect_resources::shapes::{Origin, Shape, Shapes};

#[derive(Template)]
#[template(path = "shapes/specimen.html", blocks = ["main"])]
pub struct ShapesPage {
    pub groups: Vec<Group>,
    /// Each `Shape` document that did not load.
    pub left_out: Vec<LeftOut>,
}

/// The shapes of one origin: the library, or one file.
#[derive(Debug, PartialEq)]
pub struct Group {
    /// "the library", or the path of an author's file.
    pub heading: String,
    /// Whether the heading is a file's path, which the page writes as
    /// it is.
    pub file: bool,
    pub shapes: Vec<Specimen>,
}

/// One shape, drawn in each cell.
#[derive(Debug, PartialEq)]
pub struct Specimen {
    pub name: String,
    pub summary: String,
    /// Its joins, ports, display, and whether it turns, in words.
    pub facts: String,
    /// Each kind whose knowledge states the shape, by name, linked to
    /// the page of the kind.
    pub kinds: Vec<Reference>,
    pub cells: Vec<Cell>,
}

/// One drawing of a shape, with the state or the pose it shows.
#[derive(Debug, PartialEq)]
pub struct Cell {
    pub drawn: Drawn,
    pub caption: &'static str,
    /// The cell's width, in pixels.
    pub width: f64,
    /// The room that the cell leaves beside and below its part, in
    /// pixels.
    pub room: String,
}

#[derive(Debug, PartialEq)]
pub struct LeftOut {
    pub path: String,
    pub name: String,
    pub condition: String,
}

impl ShapesPage {
    /// The page for `shapes`. `directory` gives the path of each
    /// author's file and the condition of each `Shape` that did not
    /// load, and `kinds` the knowledge of each kind that the cluster
    /// serves, with its shape found.
    pub fn build(shapes: &Shapes, directory: &Directory, kinds: &Kinds) -> Self {
        let mut stating = stating(kinds);
        let mut groups: Vec<Group> = Vec::new();
        for shape in shapes.listed() {
            let heading = match shape.origin {
                Origin::Library => "the library".to_string(),
                Origin::Document(doc) => directory
                    .files
                    .get(doc.file)
                    .map_or_else(String::new, |file| file.path.clone()),
            };
            let stated = stating.remove(&shape.name).unwrap_or_default();
            let specimen = specimen(shape, stated);
            match groups.last_mut() {
                Some(group) if group.heading == heading => group.shapes.push(specimen),
                _ => groups.push(Group {
                    file: shape.origin != Origin::Library,
                    heading,
                    shapes: vec![specimen],
                }),
            }
        }
        Self {
            groups,
            left_out: left_out(directory),
        }
    }

    pub fn events_url(&self) -> &'static str {
        "/events/shapes"
    }
}

/// Each kind whose knowledge states a shape that is loaded, by the
/// shape's name, in the order of the kinds' names.
fn stating(kinds: &Kinds) -> BTreeMap<String, Vec<Reference>> {
    let mut stating: BTreeMap<String, Vec<Reference>> = BTreeMap::new();
    for (group, kind) in kinds.kinds() {
        let Some(known) = kinds.of(group, kind) else {
            continue;
        };
        let Some(shape) = &known.drawn else {
            continue;
        };
        stating
            .entry(shape.name.clone())
            .or_default()
            .push(Reference {
                text: kind.clone(),
                href: Some(links::kind(&known.group, &known.plural)),
            });
    }
    for named in stating.values_mut() {
        named.sort_by(|a, b| (&a.text, &a.href).cmp(&(&b.text, &b.href)));
    }
    stating
}

fn specimen(shape: &Shape, kinds: Vec<Reference>) -> Specimen {
    let cells = CELLS.iter().enumerate();
    let cells = cells.filter(|(_, sample)| !sample.turned || shape.turns);
    Specimen {
        name: shape.name.clone(),
        summary: shape.summary.clone(),
        facts: facts(shape),
        kinds,
        cells: cells
            .map(|(at, sample)| cell(shape, sample, at == 0))
            .collect(),
    }
}

/// The joins, ports, display, and turning of `shape`, in words.
fn facts(shape: &Shape) -> String {
    let mut facts = Vec::new();
    if !shape.joins.is_empty() {
        let edges: Vec<&str> = shape.joins.iter().map(|edge| edge.word()).collect();
        facts.push(format!("joins on its {}", edges.join(", ")));
    }
    let ports = shape
        .ports
        .iter()
        .map(|port| format!("{} {}% to {}%", port.edge.word(), port.from, port.to));
    let ports: Vec<String> = ports.collect();
    facts.push(match ports.is_empty() {
        true => "takes no wire".into(),
        false => format!("wires at {}", ports.join(", ")),
    });
    if let Some(display) = shape.display {
        facts.push(format!("display: {}", display.word()));
    }
    if shape.turns {
        facts.push("lies on its side in a wide area".into());
    }
    facts.join("; ")
}

/// The widest that a cell is drawn, in pixels, before the room beside
/// its part. A part with a small share of the sketch still fits.
const MAX_CELL: f64 = 1200.0;

/// The padding at each side of a cell, which `specimen.css` sets.
const CELL_PADDING: f64 = 16.0;

/// One drawing of `shape` with `sample`. The first cell marks the ports.
/// A cell leaves the room that a piece past a side or below the bottom
/// needs, because a part alone has no neighbor to leave it.
fn cell(shape: &Shape, sample: &Sample, first: bool) -> Cell {
    let shape = match sample.turned {
        true => shape.turned(),
        false => shape.clone(),
    };
    let widest = shape.size.width[1];
    let scale = shape.size.design.map_or(1.0, |[width, _]| widest / width);
    let (side, below) = shape.beyond();
    let (side, below) = ((side * scale).ceil(), (below * scale).ceil());
    // A part with a share of the sketch's width takes that share of the
    // cell, so the cell of such a part is wider by the same factor.
    let share = shape.size.share.map_or(1.0, |share| share / 100.0);
    let width = (widest.clamp(200.0, 420.0) / share).min(MAX_CELL) + 2.0 * side;
    let pose = Pose {
        stack: 0,
        ports: first,
    };
    let (parts, nothing) = sample.content();
    Cell {
        drawn: Drawn {
            drawing: Drawing::of(&shape, pose),
            stacked: stacked(&shape, pose),
            label: sample.label.into(),
            source: vec![Reference {
                text: sample.source.into(),
                href: None,
            }],
            parts,
            nothing,
        },
        caption: sample.caption,
        width: width + 2.0 * CELL_PADDING,
        room: format!("padding: 0 {side}px {below}px"),
    }
}

fn left_out(directory: &Directory) -> Vec<LeftOut> {
    let files = directory.files.iter();
    let documents = files.flat_map(|file| file.documents.iter().map(move |d| (file, d)));
    let shapes = documents.filter(|(_, document)| document.kind == "Shape");
    let troubled = shapes.flat_map(|(file, document)| {
        let trouble = document.conditions.iter().filter(|c| c.trouble());
        trouble.map(move |condition| LeftOut {
            path: file.path.clone(),
            name: document.name.clone(),
            condition: condition.text(),
        })
    });
    troubled.collect()
}

#[cfg(test)]
mod tests;
