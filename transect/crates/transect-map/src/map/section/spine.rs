//! The rows of the section view's center column, which this module calls
//! the spine.  From top to bottom: the Ingresses and Services that reach
//! the workload, the workload in its layer's row, its claims with one box
//! per backend, the machines its pods run on, and the backends outside
//! the cluster.  `sides` places other boxes beside these rows.  A row
//! with no box is not drawn, so a layer's row appears only when it has
//! the workload or a side box.

mod links;
mod sides;

use std::collections::BTreeSet;

use super::layout::{Column, SIDE_TITLE_CHARS, Shape, Side};
use super::lines::Link;
use super::sentence;
pub use links::links;
pub use sides::{SideBox, Tie};
use transect_graph::edges::Provenance;
use transect_graph::links::Part;
use transect_ground::storage::{Backend, Type};

/// The most machines that the machines row draws as separate boxes.
/// With more machines it draws one box for all of them, so the section
/// of a DaemonSet fits on one screen.
pub const MOST_MACHINES: usize = 4;

/// The band of a row, which sets the row's label and color.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Band {
    Entry,
    Layer(String),
    Volumes,
    Machines,
    Ground,
}

impl Band {
    pub fn label(&self) -> &str {
        match self {
            Self::Entry => "Entry",
            Self::Layer(layer) => layer,
            Self::Volumes => "Volumes",
            Self::Machines => "Machines",
            Self::Ground => "Ground",
        }
    }

    /// The CSS classes of the row.  The Machines and Ground rows use the
    /// background colors of those bands on the cluster map.
    pub fn class(&self) -> &'static str {
        match self {
            Self::Entry | Self::Layer(_) => "section-row",
            Self::Volumes => "section-row section-row-volumes",
            Self::Machines => "section-row section-row-machines",
            Self::Ground => "section-row section-row-ground",
        }
    }
}

/// How a box is drawn.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Look {
    Plain,
    /// Drawn with a warning border: a Service with no endpoints or with
    /// an endpoint that is not ready, or a side box whose workload has
    /// fewer ready pods than it should run.
    Short,
    /// The workload that the section view shows.
    Workload,
    Machine,
    /// A backend outside the cluster, drawn with a dashed border.
    Ground,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct SectionBox {
    pub title: String,
    pub lines: Vec<String>,
    pub look: Look,
    pub href: Option<String>,
    /// The hover text of the title, for a box whose title is a count
    /// or a shortened name.
    pub hint: Option<String>,
    /// The source of the first line's text, such as an intent
    /// annotation, drawn as a mark before that line.
    pub mark: Option<Provenance>,
    /// For a box whose title is a count, such as "3 claims", the name and
    /// page of each counted object.  The template draws them as links in
    /// place of the box's last line.
    pub names: Vec<Part>,
    /// For an outside system's box, a link to the backend's own console, drawn
    /// beside the link to the box's page.
    pub external: Option<String>,
    /// The CSS class of a mark drawn before the title, such as the mark
    /// of a granted permission in the access drawings.
    pub block: Option<&'static str>,
    /// The CSS classes of marks drawn on a line of their own, under the
    /// other lines.
    pub blocks: Vec<&'static str>,
    /// The objects that a box counts, as links in a list that opens
    /// under the box when the reader clicks its title.
    pub folded: Vec<Part>,
}

impl SectionBox {
    pub fn new(title: String, lines: Vec<String>, look: Look) -> Self {
        Self {
            title,
            lines,
            look,
            href: None,
            hint: None,
            mark: None,
            names: Vec::new(),
            external: None,
            block: None,
            blocks: Vec::new(),
            folded: Vec::new(),
        }
    }

    pub fn marked(self, provenance: Provenance) -> Self {
        Self {
            mark: Some(provenance),
            ..self
        }
    }

    /// Appends `line`, and draws the box as `Look::Short` when `short`.
    pub fn with_line(mut self, line: String, short: bool) -> Self {
        self.lines.push(line);
        if short {
            self.look = Look::Short;
        }
        self
    }

    pub fn with_href(self, href: Option<String>) -> Self {
        Self { href, ..self }
    }

    pub fn with_block(self, block: &'static str) -> Self {
        Self {
            block: Some(block),
            ..self
        }
    }

    pub fn with_blocks(self, blocks: Vec<&'static str>) -> Self {
        Self { blocks, ..self }
    }

    pub fn with_names(self, names: Vec<Part>) -> Self {
        Self { names, ..self }
    }

    pub fn with_folded(self, folded: Vec<Part>) -> Self {
        Self { folded, ..self }
    }

    pub fn with_hint(self, hint: String) -> Self {
        Self {
            hint: Some(hint),
            ..self
        }
    }

    /// How many lines the title takes in a side box.  The CSS wraps a
    /// long title to a second line and cuts it off after that.
    pub fn title_lines(&self) -> usize {
        wrapped(&self.title, SIDE_TITLE_CHARS)
    }

    /// How many lines of text the box takes in a side box, counting each
    /// title or line that is too long for one line as two.
    pub fn text_lines(&self) -> usize {
        let under = self.lines.iter().map(|line| wrapped(line, LINE_CHARS));
        self.title_lines() + under.sum::<usize>()
    }

    /// The title split after each dot.  The template puts a `<wbr>`
    /// between the pieces, so a long host name wraps after a dot and not
    /// in the middle of a word.
    pub fn title_parts(&self) -> Vec<&str> {
        self.title.split_inclusive('.').collect()
    }

    pub fn tooltip(&self) -> &str {
        self.hint.as_deref().unwrap_or(&self.title)
    }
}

/// How many characters of a line under a title fit on one line of a
/// side box, at 13px Source Sans 3.  Its letters are narrower than the
/// monospace of the title, so more of them fit than `SIDE_TITLE_CHARS`.
const LINE_CHARS: usize = 26;

/// How many lines `text` takes, at most two, when one line holds `most`
/// characters.
fn wrapped(text: &str, most: usize) -> usize {
    match text.chars().count() > most {
        true => 2,
        false => 1,
    }
}

/// One row of the section view.  Each entry of `tiers` is a line of
/// boxes centered on the spine, and `left` and `right` hold the boxes of
/// the side columns.
#[derive(Debug, PartialEq, Eq)]
pub struct Row {
    pub band: Band,
    pub tiers: Vec<Vec<SectionBox>>,
    pub left: Vec<SectionBox>,
    pub right: Vec<SectionBox>,
}

/// One claim the workload mounts.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Held {
    pub name: String,
    /// Its page, which shows the volume bound to it.
    pub href: String,
    pub size: Option<String>,
    /// The kind of its volume, or `None` while the claim waits for a
    /// volume.
    pub kind: Option<Type>,
}

/// The workload's claims on one backend, and the machines whose pods
/// mount them.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Backing {
    /// Where the claims store their data, or `None` for claims that
    /// wait for a volume.
    pub backend: Option<Backend>,
    pub claims: Vec<Held>,
    /// The names of the NFS volumes that the pods define inline in
    /// their specs, with no claim.
    pub inline: Vec<String>,
    pub machines: BTreeSet<String>,
}

impl Backing {
    /// The kinds of the claims' volumes, each listed once, in claim
    /// order.
    pub fn claim_kinds(&self) -> Vec<&Type> {
        let mut kinds: Vec<&Type> = Vec::new();
        for kind in self.claims.iter().filter_map(|claim| claim.kind.as_ref()) {
            if !kinds.contains(&kind) {
                kinds.push(kind);
            }
        }
        kinds
    }

    /// `claim_kinds`, followed by NFS when the pods mount NFS volumes
    /// inline and no claim is NFS.
    pub fn kinds(&self) -> Vec<&Type> {
        let mut kinds = self.claim_kinds();
        if !self.inline.is_empty() && !kinds.contains(&&Type::Nfs) {
            kinds.push(&Type::Nfs);
        }
        kinds
    }

    /// The name of the outside system that shows its backend, or `None` for
    /// a machine's own disk or for claims that wait for a volume.
    pub fn outside(&self) -> Option<&str> {
        self.backend.as_ref()?.outside()
    }
}

/// A machine the workload's pods run on.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Host {
    pub name: String,
    pub pods: usize,
    pub control_plane: bool,
}

/// What `rows` draws the spine from.
#[derive(Debug, Default)]
pub struct Parts {
    pub layer: String,
    /// Every layer of the cluster map, in the map's order, which is the
    /// order of the layer rows.
    pub layers: Vec<String>,
    pub ingresses: Vec<SectionBox>,
    pub services: Vec<SectionBox>,
    /// The Service that each Ingress routes to, as a pair of indexes
    /// into `ingresses` and `services`.
    pub routes: Vec<(usize, usize)>,
    pub workload: Option<SectionBox>,
    pub backings: Vec<Backing>,
    /// Sorted by name.
    pub hosts: Vec<Host>,
    pub sides: Vec<SideBox>,
    /// The lines between side boxes in the right column.
    pub ties: Vec<Tie>,
}

impl Parts {
    pub fn pods(&self) -> usize {
        self.hosts.iter().map(|host| host.pods).sum()
    }

    /// Whether `side` is one of the workload's machines, which the
    /// machines row already draws.
    fn on_spine(&self, side: &SideBox) -> bool {
        let named = side.names.as_ref();
        named.is_some_and(|name| self.hosts.iter().any(|host| host.name == *name))
    }

    fn beside(&self) -> Vec<SideBox> {
        let sides = self.sides.iter().filter(|side| !self.on_spine(side));
        sides.cloned().collect()
    }
}

/// The spine's rows, top to bottom.
pub fn rows(parts: &Parts) -> Vec<Row> {
    let mut rows = Vec::new();
    let entry = vec![parts.ingresses.clone(), parts.services.clone()];
    push(&mut rows, parts, Band::Entry, entry);
    for layer in layers(parts) {
        let workload = parts.workload.clone().filter(|_| layer == parts.layer);
        let tiers = vec![workload.into_iter().collect()];
        push(&mut rows, parts, Band::Layer(layer), tiers);
    }
    let claims = parts.backings.iter().filter(|b| !b.claims.is_empty());
    let claims = vec![claims.map(claims_box).collect()];
    push(&mut rows, parts, Band::Volumes, claims);
    push(&mut rows, parts, Band::Machines, vec![machine_boxes(parts)]);
    let ground = parts.backings.iter().filter_map(ground_box);
    push(&mut rows, parts, Band::Ground, vec![ground.collect()]);
    rows
}

/// The layers that contain the workload or a side box, in the map's
/// order.  A layer that the map does not list sorts last.
fn layers(parts: &Parts) -> Vec<String> {
    let mut used = vec![parts.layer.clone()];
    for side in &parts.sides {
        if let Band::Layer(layer) = &side.band
            && !used.contains(layer)
        {
            used.push(layer.clone());
        }
    }
    let place = |layer: &String| parts.layers.iter().position(|l| l == layer);
    used.sort_by_key(|layer| place(layer).unwrap_or(usize::MAX));
    used
}

/// Appends the row of `band` with its empty tiers removed.  A row with
/// no tier and no side box is not appended.
fn push(rows: &mut Vec<Row>, parts: &Parts, band: Band, tiers: Vec<Vec<SectionBox>>) {
    let tiers: Vec<Vec<SectionBox>> = tiers.into_iter().filter(|t| !t.is_empty()).collect();
    let side = |side: Side| -> Vec<SectionBox> {
        let column = sides::column(&parts.beside(), &band, side);
        column.into_iter().map(|s| s.boxed).collect()
    };
    let (left, right) = (side(Side::Left), side(Side::Right));
    if tiers.is_empty() && left.is_empty() && right.is_empty() {
        return;
    }
    rows.push(Row {
        band,
        tiers,
        left,
        right,
    });
}

/// The box for the claims of one backend.  A single claim is titled
/// with its name and links to its page.  Several claims are titled with
/// their count, and the last line links each claim by name.
fn claims_box(backing: &Backing) -> SectionBox {
    let line = sentence::claims_line(backing);
    match backing.claims.as_slice() {
        [one] => SectionBox::new(one.name.clone(), vec![line], Look::Plain)
            .with_hint(sentence::claims_title(1))
            .with_href(Some(one.href.clone())),
        claims => {
            let names: Vec<&str> = claims.iter().map(|claim| claim.name.as_str()).collect();
            let linked = claims.iter().map(|claim| Part {
                text: claim.name.clone(),
                href: Some(claim.href.clone()),
            });
            SectionBox::new(
                sentence::claims_title(claims.len()),
                vec![line, names.join(", ")],
                Look::Plain,
            )
            .with_hint(names.join("\n"))
            .with_names(linked.collect())
        }
    }
}

/// One box for each machine, or one box for all of them when there are
/// more than `MOST_MACHINES`.
fn machine_boxes(parts: &Parts) -> Vec<SectionBox> {
    let total_pods = parts.pods();
    if parts.hosts.len() > MOST_MACHINES {
        let title = sentence::machines_title(parts.hosts.len());
        let line = sentence::machines_line(total_pods);
        return vec![SectionBox::new(title, vec![line], Look::Machine)];
    }
    let hosts = parts.hosts.iter();
    hosts
        .map(|host| {
            let line = sentence::machine_line(host.pods, total_pods, host.control_plane);
            let href = format!("/machines/{}", host.name);
            SectionBox::new(host.name.clone(), vec![line], Look::Machine).with_href(Some(href))
        })
        .collect()
}

fn ground_box(backing: &Backing) -> Option<SectionBox> {
    let name = backing.outside()?;
    let lines = sentence::ground_lines(backing);
    Some(SectionBox::new(name.into(), lines, Look::Ground))
}

/// The shape of each row for the layout: the number of text lines in
/// each box.  When a line runs from a side box to a box in another row,
/// each row from one end to the other gets that side in `crossed`, so
/// the layout keeps the row's tiers out of that column.
pub fn shapes(rows: &[Row], links: &[Link]) -> Vec<Shape> {
    let lines =
        |boxes: &[SectionBox]| -> Vec<usize> { boxes.iter().map(SectionBox::text_lines).collect() };
    let mut shapes: Vec<Shape> = rows
        .iter()
        .map(|row| Shape {
            tiers: row.tiers.iter().map(|tier| lines(tier)).collect(),
            left: lines(&row.left),
            right: lines(&row.right),
            ..Shape::default()
        })
        .collect();
    for link in links {
        let (side, rows) = match (link.from.column, link.to.column) {
            (Column::Side(side), _) | (_, Column::Side(side)) => {
                (side, (link.from.row, link.to.row))
            }
            _ => continue,
        };
        for row in rows.0.min(rows.1)..=rows.0.max(rows.1) {
            if let Some(shape) = shapes.get_mut(row) {
                shape.crossed.set(side);
            }
        }
    }
    shapes
}

#[cfg(test)]
mod tests;
