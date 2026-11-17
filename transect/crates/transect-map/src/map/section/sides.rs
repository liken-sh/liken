//! What the section view draws beside the spine, read from the graph: the
//! workloads configured to call this one on the left, and on the right
//! what it is configured to call, the GitOps object it reconciles from,
//! the controller that reconciles it, and the devices its pods hold.
//! Each object is drawn in its own namespace's layer, a machine or a
//! device in the machines row, and a box outside the cluster in the
//! ground row.  An edge kind that is turned off has no edges, so its side
//! boxes and their lines are not drawn.
//!
//! A phrase such as "office file conversion" tells a reader more about
//! why the line is there than "Deployment in docs" does.  So the line
//! under an object's name gives what its intent states it provides, or
//! its intent's `why` when it states nothing it provides, or else what
//! the object is.

use super::layout::Side;
use super::lines::Style;
use super::sentence;
use super::spine::{Band, Look, SectionBox, SideBox, Tie};
use transect_graph::edges::{Device, Edge, Endpoint, Graph, Kind, Object, Plurals, Provenance};
use transect_resources::intent::Intents;

/// The most callers the section view draws one by one.  When there are
/// more, one box in the workload's own row is drawn in place of them all,
/// with one line, so a workload with many callers gets no long row of
/// dashed lines.
pub const MOST_CALLERS: usize = 6;

/// The kinds that put a box on the right, in the order they stack.
const RIGHT: [(Kind, &str); 5] = [
    (Kind::DependsOn, "depends on"),
    (Kind::AuthenticatesThrough, "authenticates through"),
    (Kind::ReconcilesFrom, "reconciles from"),
    (Kind::ControlledBy, "controlled by"),
    (Kind::Holds, "holds"),
];

/// What an object's side box reads besides the graph.
pub struct Objects<'a> {
    pub plurals: &'a Plurals,
    pub intents: &'a Intents,
    /// Whether an object has a page for its box to link.
    pub has_page: &'a dyn Fn(&Object) -> bool,
    /// A workload's ready line, in the same form as the spine's workload
    /// box, and whether fewer of its pods are ready than it asks for.
    /// `None` for an object that runs no pods.
    pub ready: &'a dyn Fn(&Object) -> Option<(String, bool)>,
    /// Returns a device's title from the knowledge for its driver.
    pub device_title: &'a dyn Fn(&Device) -> String,
}

/// The side boxes of `workload`'s section.  `layer_of` returns a
/// namespace's layer, and an object that it returns no layer for is drawn
/// in the workload's own `layer`.  An object's box links to its page only
/// when it has one, and a workload's box shows how many of its pods are
/// ready, as the spine's boxes do.
pub fn sides(
    graph: &Graph,
    workload: &Object,
    objects: &Objects,
    layer: &str,
    layer_of: impl Fn(&str) -> Option<String>,
) -> Vec<SideBox> {
    let Objects {
        plurals,
        intents,
        has_page,
        ready,
        device_title,
    } = objects;
    let end = Endpoint::Object(workload.clone());
    let here = workload.namespace.as_deref();
    let band = |object: &Object| {
        let found = object.namespace.as_deref().and_then(&layer_of);
        Band::Layer(found.unwrap_or_else(|| layer.into()))
    };
    let object_box = |object: &Object| {
        let said = intents.of(object);
        let stated = said.and_then(|said| said.provides.as_ref().or(said.why.as_ref()));
        let title = transect_graph::links::name_in(object, here);
        let boxed = match stated {
            Some(stated) => SectionBox::new(title, vec![stated.text.clone()], Look::Plain)
                .marked(stated.provenance.clone()),
            None => SectionBox::new(title, vec![object.kind.clone()], Look::Plain),
        };
        let boxed = match ready(object) {
            Some((line, short)) => boxed.with_line(line, short),
            None => boxed,
        };
        let href = has_page(object).then(|| transect_graph::links::page(object, plurals));
        boxed.with_href(href.flatten())
    };
    let mut sides = Vec::new();
    let callers = edges(graph, Kind::DependsOn, |edge| {
        edge.to == end && edge.from != end
    });
    let callers: Vec<(&Object, Style, &Provenance)> = callers
        .into_iter()
        .filter_map(|edge| match &edge.from {
            Endpoint::Object(caller) => {
                Some((caller, style(Kind::DependsOn, edge), &edge.provenance))
            }
            _ => None,
        })
        .collect();
    if callers.len() > MOST_CALLERS {
        sides.push(callers_box(&callers, layer));
    } else {
        for (caller, style, provenance) in callers {
            let side = SideBox::new(
                band(caller),
                Side::Left,
                object_box(caller),
                "depends on",
                style,
            )
            .of(caller);
            sides.push(side.inward().from(provenance));
        }
    }
    for (kind, label) in RIGHT {
        for edge in edges(graph, kind, |edge| edge.from == end && edge.to != end) {
            let style = style(kind, edge);
            let side = match &edge.to {
                Endpoint::Object(object) => {
                    SideBox::new(band(object), Side::Right, object_box(object), label, style)
                        .of(object)
                        .standing(edge.to.clone())
                }
                Endpoint::Machine(machine) => {
                    let line = sentence::machine_side_line();
                    let boxed = SectionBox::new(machine.clone(), vec![line], Look::Machine);
                    let boxed = boxed.with_href(Some(format!("/machines/{machine}")));
                    SideBox::new(Band::Machines, Side::Right, boxed, label, style).naming(machine)
                }
                Endpoint::Device(device) => {
                    let boxed = device_box(device, device_title(device));
                    SideBox::new(Band::Machines, Side::Right, boxed, label, style)
                        .on(device.machine.clone())
                        .standing(edge.to.clone())
                }
                Endpoint::Outside(name) => {
                    let line = sentence::outside_side_line();
                    let boxed = SectionBox::new(name.clone(), vec![line], Look::Ground);
                    let boxed = match &edge.provenance {
                        Provenance::Kubernetes => boxed,
                        provenance => boxed.marked(provenance.clone()),
                    };
                    SideBox::new(Band::Ground, Side::Right, boxed, label, style)
                }
            };
            sides.push(side.from(&edge.provenance));
        }
    }
    sides
}

/// The lines from each device in the right column to the other boxes in
/// that column that the device's attributes name, as device knowledge
/// reads the attributes: the radio that a paired remote pairs through, or
/// the screen whose speakers an HDMI sound output is.  Only a device's
/// edges are tied, because otherwise a device box is joined only to its
/// machine.
pub fn ties(graph: &Graph, sides: &[SideBox]) -> Vec<Tie> {
    let right: Vec<&Endpoint> = sides
        .iter()
        .filter(|side| side.side == Side::Right)
        .filter_map(|side| side.end.as_ref())
        .collect();
    let mut found: Vec<Tie> = Vec::new();
    let devices = right
        .iter()
        .filter(|end| matches!(end, Endpoint::Device(_)));
    for from in devices {
        for (kind, words) in RIGHT {
            for edge in graph.edges_leaving(kind, from) {
                let tied = found.iter().any(|t| t.from == edge.from && t.to == edge.to);
                if !right.contains(&&edge.to) || edge.to == edge.from || tied {
                    continue;
                }
                let label = match &edge.provenance {
                    Provenance::Devices { label, .. } => label.clone(),
                    _ => words.to_string(),
                };
                let title =
                    (edge.provenance != Provenance::Kubernetes).then(|| edge.provenance.title());
                found.push(Tie {
                    from: edge.from.clone(),
                    to: edge.to.clone(),
                    label,
                    style: style(kind, edge),
                    title,
                });
            }
        }
    }
    found
}

/// The box for a held device.  Its title is `title`, because the line
/// under it names the driver, and the title wraps to two lines when it is
/// long.  On hover, the box shows every part that names the device.
fn device_box(device: &Device, title: String) -> SectionBox {
    let boxed = SectionBox::new(title, vec![device.driver.clone()], Look::Plain);
    boxed
        .with_hint(device.full())
        .with_href(transect_graph::links::device(device))
}

/// The one box for `callers`, in the workload's `layer`, naming each of
/// them in its hint.  Its line is dashed unless every caller's edge is a
/// guess.
fn callers_box(callers: &[(&Object, Style, &Provenance)], layer: &str) -> SideBox {
    let mut namespaces: Vec<&str> = callers
        .iter()
        .filter_map(|(caller, _, _)| caller.namespace.as_deref())
        .collect();
    namespaces.sort_unstable();
    namespaces.dedup();
    let names: Vec<String> = callers
        .iter()
        .map(|(caller, _, _)| transect_graph::links::name_in(caller, None))
        .collect();
    let title = sentence::callers_title(callers.len());
    let line = sentence::callers_line(&namespaces);
    let boxed = SectionBox::new(title, vec![line], Look::Plain).with_hint(names.join("\n"));
    let style = match callers.iter().all(|(_, style, _)| *style == Style::Dotted) {
        true => Style::Dotted,
        false => Style::Dashed,
    };
    let band = Band::Layer(layer.into());
    SideBox::new(band, Side::Left, boxed, "depends on", style).inward()
}

/// The `kind` edges that `keep` accepts, each pair of ends once, sorted
/// by the far end's name so that a column keeps its order from one push
/// to the next.
fn edges(graph: &Graph, kind: Kind, keep: impl Fn(&Edge) -> bool) -> Vec<&Edge> {
    let mut found: Vec<&Edge> = graph.of_kind(kind).filter(|edge| keep(edge)).collect();
    found.sort_by(|a, b| (&a.from, &a.to).cmp(&(&b.from, &b.to)));
    found.dedup_by(|a, b| a.from == b.from && a.to == b.to);
    found
}

/// Dashed for a caller, as the section view's legend explains, solid for
/// structure, and dotted for an edge transect guessed.
fn style(kind: Kind, edge: &Edge) -> Style {
    match (edge.guessed, kind) {
        (true, _) => Style::Dotted,
        (false, Kind::DependsOn) => Style::Dashed,
        (false, _) => Style::Solid,
    }
}

#[cfg(test)]
mod tests;
