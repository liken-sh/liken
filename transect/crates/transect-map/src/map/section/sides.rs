//! The boxes the section view draws beside the spine, read from the
//! graph. On the left are the workloads configured to call this one. On
//! the right are what it is configured to call, the GitOps object it
//! reconciles from, the controller that reconciles it, and the devices
//! its pods hold. Each object is drawn in its own namespace's layer, a
//! machine or a device in the machines row, and an address outside the
//! cluster in the ground row.
//!
//! A phrase such as "office file conversion" tells a reader more about
//! why a box is there than "Deployment in docs" does. So the line under
//! an object's name is what its intent says it provides, or else its
//! intent's `why`, or else the object's kind.

use super::layout::Side;
use super::lines::Style;
use super::sentence;
use super::spine::{Band, Look, SectionBox, SideBox, Tie};
use transect_graph::edges::{Device, Edge, Endpoint, Graph, Kind, Object, Plurals, Provenance};
use transect_resources::intent::Intents;

/// The most callers the section view draws as separate boxes. When there
/// are more, one box in the workload's own layer stands for all of them,
/// with one line, so a workload with many callers does not get a long
/// row of dashed lines.
pub const MOST_CALLERS: usize = 6;

/// The kinds that put a box on the right, in the order they stack.
const RIGHT: [(Kind, &str); 5] = [
    (Kind::DependsOn, "depends on"),
    (Kind::AuthenticatesThrough, "authenticates through"),
    (Kind::ReconcilesFrom, "reconciles from"),
    (Kind::ControlledBy, "controlled by"),
    (Kind::Holds, "holds"),
];

/// What `sides` needs besides the graph to fill in an object's side box.
pub struct Objects<'a> {
    pub plurals: &'a Plurals,
    pub intents: &'a Intents,
    /// Whether an object has a page that its box can link to.
    pub has_page: &'a dyn Fn(&Object) -> bool,
    /// A workload's ready line, in the same form as the spine's workload
    /// box, and whether fewer pods are ready than it should run. `None` for
    /// an object that runs no pods.
    pub ready: &'a dyn Fn(&Object) -> Option<(String, bool)>,
    /// Returns a device's title, from the device knowledge for its
    /// driver.
    pub device_title: &'a dyn Fn(&Device) -> String,
}

/// The side boxes of `workload`'s section. `layer_of` returns the layer
/// of a namespace. An object that it returns no layer for is drawn in
/// the workload's own `layer`. An object's box links to its page only
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
        let intent = intents.of(object);
        let summary = intent.and_then(|intent| intent.provides.as_ref().or(intent.why.as_ref()));
        let title = transect_graph::links::name_in(object, here);
        let boxed = match summary {
            Some(summary) => SectionBox::new(title, vec![summary.text.clone()], Look::Plain)
                .marked(summary.provenance.clone()),
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
/// that column that the device's attributes refer to, such as the radio
/// that a remote pairs through, or the screen that an HDMI sound output
/// plays through. A device's side line joins its machine, so without
/// these lines the section would not show how the device relates to the
/// other boxes. Only edges that leave a device become ties.
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

/// The box for a held device. The line under the title is the driver,
/// so `title` is the device's own name, and it wraps to two lines when
/// it is long. On hover, the box shows every part of the device's name.
fn device_box(device: &Device, title: String) -> SectionBox {
    let boxed = SectionBox::new(title, vec![device.driver.clone()], Look::Plain);
    boxed
        .with_hint(device.full())
        .with_href(transect_graph::links::device(device))
}

/// The box drawn in place of the callers when there are more than
/// `MOST_CALLERS`. Its hover text lists every caller. Its line is
/// dashed, or dotted when every caller's edge is a guess.
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

/// The `kind` edges that `keep` accepts, with one edge for each pair of
/// ends. They are sorted by their ends, so a column keeps the same order
/// from one pushed update to the next.
fn edges(graph: &Graph, kind: Kind, keep: impl Fn(&Edge) -> bool) -> Vec<&Edge> {
    let mut found: Vec<&Edge> = graph.of_kind(kind).filter(|edge| keep(edge)).collect();
    found.sort_by(|a, b| (&a.from, &a.to).cmp(&(&b.from, &b.to)));
    found.dedup_by(|a, b| a.from == b.from && a.to == b.to);
    found
}

/// The line style for an edge, as the section view's legend explains:
/// dotted for an edge that transect guessed, dashed for a caller, and
/// solid for anything else.
fn style(kind: Kind, edge: &Edge) -> Style {
    match (edge.guessed, kind) {
        (true, _) => Style::Dotted,
        (false, Kind::DependsOn) => Style::Dashed,
        (false, _) => Style::Solid,
    }
}

#[cfg(test)]
mod tests;
