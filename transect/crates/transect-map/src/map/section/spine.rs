//! The spine's boxes, row by row: the entry, the workload in its
//! layer's row, its claims one box per backend, the machines its pods
//! run on, and the backends outside the cluster, with the side boxes of
//! `sides` beside them.  A row with no box is left out, so a workload
//! with no Service starts at its own row, and a layer's row appears only
//! when it contains the workload or a side box.

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

/// The most machines the machines row draws one by one.  When there are
/// more, one box is drawn in place of them all, so a DaemonSet's section
/// fits on one screen.
pub const MOST_MACHINES: usize = 4;

/// The map's band that a row belongs to, which gives the row its label
/// and its color.
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

    /// Each band takes its color from the cluster map, so the section
    /// view shows the same colors from the top down to the ground as the
    /// map.
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
    /// A Service with an endpoint that is not ready, or none.
    Short,
    /// The workload the section view is of.
    Workload,
    Machine,
    /// A backend outside the cluster, dashed as the ground's boxes are.
    Ground,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct SectionBox {
    pub title: String,
    pub lines: Vec<String>,
    pub look: Look,
    pub href: Option<String>,
    /// What the title shows on hover in place of itself, for a box
    /// whose title is a count or a shortened name.
    pub hint: Option<String>,
    /// Where its first line was written, when it is stored text.
    pub mark: Option<Provenance>,
    /// The boxes or claims that a box counts, each by its title and the
    /// page it opens.  The box's last line lists them, linked, in place
    /// of its text.
    pub names: Vec<Part>,
    /// The link from a ground box to the backend's own console, drawn
    /// beside the link to its page.
    pub external: Option<String>,
    /// The class of a mark drawn before the title, such as the block of
    /// a capability an access drawing names.
    pub block: Option<&'static str>,
    /// The classes of the marks drawn in a line of their own under the
    /// others.
    pub blocks: Vec<&'static str>,
    /// The names a box counts, which open in a list under it on a
    /// click, each linked.
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

    /// The box with its first line marked as stored text.
    pub fn marked(self, provenance: Provenance) -> Self {
        Self {
            mark: Some(provenance),
            ..self
        }
    }

    /// The box with `line` under its others, drawn short when `short`.
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

    /// The box with `block` drawn before its title.
    pub fn with_block(self, block: &'static str) -> Self {
        Self {
            block: Some(block),
            ..self
        }
    }

    /// The box with a line of `blocks` under its others.
    pub fn with_blocks(self, blocks: Vec<&'static str>) -> Self {
        Self { blocks, ..self }
    }

    pub fn with_names(self, names: Vec<Part>) -> Self {
        Self { names, ..self }
    }

    /// The box with `folded`, the names it counts, opening under it.
    pub fn with_folded(self, folded: Vec<Part>) -> Self {
        Self { folded, ..self }
    }

    pub fn with_hint(self, hint: String) -> Self {
        Self {
            hint: Some(hint),
            ..self
        }
    }

    /// How many lines its title takes in a side box: a title too long
    /// for one wraps to a second before it is cut short.
    pub fn title_lines(&self) -> usize {
        wrapped(&self.title, SIDE_TITLE_CHARS)
    }

    /// How many lines its title and the lines under it take, each line
    /// too long for one taking a second.
    pub fn text_lines(&self) -> usize {
        let under = self.lines.iter().map(|line| wrapped(line, LINE_CHARS));
        self.title_lines() + under.sum::<usize>()
    }

    /// The title in pieces that end at each dot, so that a host name
    /// breaks after a dot, where a reader splits it, before mid-word.
    /// Each piece is text, and the page puts the break between pieces.
    pub fn title_parts(&self) -> Vec<&str> {
        self.title.split_inclusive('.').collect()
    }

    /// The title's hover text: its hint, or the title itself.
    pub fn tooltip(&self) -> &str {
        self.hint.as_deref().unwrap_or(&self.title)
    }
}

/// The most characters of a line under a title that fit on one line of
/// a side box, in 13px Source Sans 3, whose letters are narrower than
/// the title's monospace.
const LINE_CHARS: usize = 26;

/// How many lines `text` takes, at most two, when one holds `most`
/// characters.
fn wrapped(text: &str, most: usize) -> usize {
    match text.chars().count() > most {
        true => 2,
        false => 1,
    }
}

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
    /// The kind of its volume, or `None` while it waits for one.
    pub kind: Option<Type>,
}

/// The workload's claims on one backend, and the machines whose pods
/// mount them.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Backing {
    /// Where their data is stored, or `None` for claims that wait for a
    /// volume.
    pub backend: Option<Backend>,
    pub claims: Vec<Held>,
    /// The NFS volumes its pods name inline, with no claim, by the
    /// names the pod spec gives them.
    pub inline: Vec<String>,
    pub machines: BTreeSet<String>,
}

impl Backing {
    /// The kinds of its claims' volumes, each once, in claim order.
    pub fn claim_kinds(&self) -> Vec<&Type> {
        let mut kinds: Vec<&Type> = Vec::new();
        for kind in self.claims.iter().filter_map(|claim| claim.kind.as_ref()) {
            if !kinds.contains(&kind) {
                kinds.push(kind);
            }
        }
        kinds
    }

    /// The kinds of all its volumes, with its inline NFS volumes last.
    pub fn kinds(&self) -> Vec<&Type> {
        let mut kinds = self.claim_kinds();
        if !self.inline.is_empty() && !kinds.contains(&&Type::Nfs) {
            kinds.push(&Type::Nfs);
        }
        kinds
    }

    /// The ground box it draws as, or `None` for a machine's own disk or
    /// claims that wait.
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

/// What the spine is drawn from.
#[derive(Debug, Default)]
pub struct Parts {
    pub layer: String,
    /// Every layer of the map, in its order, which orders the rows of
    /// the layers that side boxes are in.
    pub layers: Vec<String>,
    pub ingresses: Vec<SectionBox>,
    pub services: Vec<SectionBox>,
    /// Which Service each Ingress routes to, as indexes into
    /// `ingresses` and `services`.
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

    /// Whether `side` is a machine the spine already draws, as one of
    /// the hosts or among those that one box is drawn in place of.
    fn on_spine(&self, side: &SideBox) -> bool {
        let named = side.names.as_ref();
        named.is_some_and(|name| self.hosts.iter().any(|host| host.name == *name))
    }

    /// The side boxes drawn beside the spine: every one but those the
    /// spine already draws.
    fn beside(&self) -> Vec<SideBox> {
        let sides = self.sides.iter().filter(|side| !self.on_spine(side));
        sides.cloned().collect()
    }
}

/// The spine's rows, top to bottom.
pub fn rows(parts: &Parts) -> Vec<Row> {
    let mut rows = Vec::new();
    let door = vec![parts.ingresses.clone(), parts.services.clone()];
    push(&mut rows, parts, Band::Entry, door);
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
/// order.
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

/// Adds the row of `band` with its non-empty `tiers` and its side
/// columns, unless it has no box at all.
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

/// The box for one claim takes the claim's name as its title and opens
/// its page.  The box for several claims takes their count as its title,
/// and the line under the count names each claim, linked.
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

/// Each machine by name, or past `MOST_MACHINES`, one box for them all.
fn machine_boxes(parts: &Parts) -> Vec<SectionBox> {
    let of = parts.pods();
    if parts.hosts.len() > MOST_MACHINES {
        let title = sentence::machines_title(parts.hosts.len());
        let line = sentence::machines_line(of);
        return vec![SectionBox::new(title, vec![line], Look::Machine)];
    }
    let hosts = parts.hosts.iter();
    hosts
        .map(|host| {
            let line = sentence::machine_line(host.pods, of, host.control_plane);
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

/// Each row's shape for the layout, where a box's count of lines is its
/// title's lines and the lines under the title.  When a line from a side
/// box runs to another row, each row from one end to the other keeps that
/// side clear.
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
