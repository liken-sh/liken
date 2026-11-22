//! The section view draws one workload from its Ingresses and Services
//! down to its machines and the outside systems it stores data on, in
//! the map's bands.  The server computes each box's position with
//! `layout`, and the boxes are HTML elements with absolute positions,
//! so their text and links work as on every other page.

mod gather;
mod layout;
pub(super) mod lines;
mod sentence;
mod sides;
mod spine;

/// The section view's types that the access lens's drawings also use:
/// a box, its rectangle, and the path of a line.
pub mod drawing {
    pub use super::layout::Rect;
    pub use super::lines::{Line, Point, Style, rounded};
    pub use super::spine::{Look, SectionBox};
}

use std::sync::Arc;

use askama::Template;
use k8s_openapi::api::core::v1::Service;
use k8s_openapi::api::networking::v1::Ingress;

use super::ClusterMap;
use super::workload::find;
use crate::cards::WorkloadCard;
use layout::{Layout, Rect, SPINE, SPINE_BAND, layout};
use lines::{Drawn, Label, Line, draw};
use spine::{Band, Look, Parts, SectionBox};
use transect_cluster::workloads::Workloads;
use transect_frame::frame::crumbs::Crumb;
use transect_frame::frame::tabs::{Lens, Tabs};
use transect_graph::edges::{Endpoint, Graph, Kind, Object, Plurals};
use transect_ground::machines::Machine;
use transect_ground::storage::Storage;
use transect_network::entry::{Endpoints, Entry, Issuers, TrafficResults};
use transect_resources::intent::Intents;

/// The section view's page.  Its `main` block renders alone as the
/// fragment that a pushed update swaps in, as on every elevation.
#[derive(Template)]
#[template(path = "map/section/section.html", blocks = ["main"])]
pub struct SectionPage {
    pub namespace: String,
    pub layer: String,
    pub name: String,
    /// The workload's page, which the crumb and the workload's box link
    /// to.
    pub workload_path: String,
    /// The section view's own path, which the events URL ends with.
    pub path: String,
    pub width: i32,
    pub height: i32,
    pub rows: Vec<DrawnRow>,
    /// The lines between boxes, drawn under the boxes.
    pub lines: Vec<Line>,
    pub labels: Vec<Label>,
    /// The workload's card, drawn as on the workload's page, beside the
    /// workload's row.
    pub card: WorkloadCard,
    /// Whether the page shows the access tab, which the workload has when
    /// it serves a port.
    pub access: bool,
}

/// One row with its top and height, and each of its boxes with its
/// rectangle.
pub struct DrawnRow {
    pub band: Band,
    pub top: i32,
    pub height: i32,
    pub boxes: Vec<(SectionBox, Rect)>,
}

/// What the section view reads besides the map.
pub struct Contents<'a> {
    pub workloads: &'a Workloads<'a>,
    /// The namespace's top owners.
    pub owners: Vec<&'a Object>,
    pub plurals: &'a Plurals,
    pub graph: &'a Graph,
    pub services: &'a [Arc<Service>],
    pub ingresses: &'a [Arc<Ingress>],
    pub endpoints: &'a Endpoints,
    /// The issuer of each Ingress's certificate.
    pub issuers: &'a Issuers,
    pub storage: &'a Storage,
    pub machines: &'a [Machine],
    /// The recorded intent of the objects in the side columns.
    pub intents: &'a Intents,
    /// Whether an object in the side columns has a page to link to.
    pub has_page: &'a dyn Fn(&Object) -> bool,
    /// The traffic numbers that the ingress controllers' metrics report
    /// for each backend.
    pub traffic: TrafficResults<'a>,
    /// Returns the title of a device's box.
    pub device_title: &'a dyn Fn(&transect_graph::edges::Device) -> String,
}

impl SectionPage {
    /// Replaces the title, page link, and console link of each ground
    /// box, the box of an outside system, with what `pages` returns for
    /// its title.  For an NFS export that
    /// a pod names inline, the title is the server's address, and
    /// `pages` returns the title of the `Outside` for that address.
    pub fn with_ground_pages(
        mut self,
        pages: impl Fn(&str) -> (String, String, Option<String>),
    ) -> Self {
        let boxes = self.rows.iter_mut().flat_map(|row| row.boxes.iter_mut());
        for (boxed, _) in boxes.filter(|(boxed, _)| boxed.look == Look::Ground) {
            let (title, page, external) = pages(&boxed.title);
            boxed.title = title;
            boxed.href = Some(page);
            boxed.external = external;
        }
        self
    }

    pub fn with_access(self, access: bool) -> Self {
        Self { access, ..self }
    }

    /// Builds the section view of the workload `name`, whose kind has
    /// the resource name `resource`, with the card that `card` builds for
    /// it.  Returns `None` when `namespace` has no such workload.
    pub fn build(
        map: &ClusterMap,
        namespace: &str,
        resource: &str,
        name: &str,
        contents: Contents,
        card: impl FnOnce(&Object) -> WorkloadCard,
    ) -> Option<Self> {
        let layer = map.layer_of(namespace)?.to_owned();
        let object = find(&contents.owners, contents.plurals, resource, name)?;
        let workload_path = transect_graph::links::page(object, contents.plurals)?;
        let mut parts = parts(object, &layer, &workload_path, &contents);
        parts.layers = map.bands.iter().map(|band| band.name.clone()).collect();
        let layer_of = |namespace: &str| map.layer_of(namespace).map(String::from);
        let ready = |other: &Object| {
            let found = contents.workloads.get(other);
            if found.parts.is_empty() && found.pods.is_empty() {
                return None;
            }
            let running = found.running().len();
            let line = sentence::workload_line(found.replicas(), found.ready(), running);
            let short = found.ready().is_some_and(|(ready, wanted)| ready < wanted);
            Some((line, short))
        };
        let objects = sides::Objects {
            plurals: contents.plurals,
            intents: contents.intents,
            has_page: contents.has_page,
            ready: &ready,
            device_title: contents.device_title,
        };
        parts.sides = sides::sides(contents.graph, object, &objects, &layer, layer_of);
        parts.ties = sides::ties(contents.graph, &parts.sides);
        let rows = spine::rows(&parts);
        let links = spine::links(&parts, &rows);
        let placed = layout(&spine::shapes(&rows, &links));
        let Drawn { lines, labels } = draw(&placed, &links);
        let tied = links.iter().any(lines::is_tie);
        let width = placed.width + if tied { lines::OUTER } else { 0 };
        Some(Self {
            access: false,
            namespace: namespace.into(),
            layer,
            name: name.into(),
            path: format!("{workload_path}/section"),
            workload_path,
            width,
            height: placed.height,
            rows: drawn(rows, &placed),
            lines,
            labels,
            card: card(object),
        })
    }

    /// The crumbs of the workload's page, with the workload as a link
    /// and the section view as the current place.
    pub fn crumbs(&self) -> Vec<Crumb> {
        let select = format!("/?select={}", self.namespace);
        vec![
            Crumb::home(),
            Crumb::link(&self.layer, format!("{select}#{}", self.layer)),
            Crumb::link_name(&self.namespace, format!("/namespaces/{}", self.namespace)),
            Crumb::link_name(&self.name, self.workload_path.clone()),
            Crumb::current("section"),
        ]
    }

    pub fn tabs(&self) -> Tabs {
        let tabs = Tabs::workload(&self.workload_path, Lens::Section, self.access);
        match self.card.vulnerabilities.is_some() {
            true => tabs.with_vulnerabilities(&self.workload_path, false),
            false => tabs,
        }
    }

    pub fn legend(&self) -> &'static str {
        sentence::LEGEND
    }

    pub fn subtitle(&self) -> String {
        let entry = self.rows.iter().any(|row| row.band == Band::Entry);
        let storage = self
            .rows
            .iter()
            .any(|row| matches!(row.band, Band::Volumes | Band::Ground));
        sentence::subtitle(entry, storage, &self.name)
    }

    pub fn events_url(&self) -> String {
        format!("/events{}", self.path)
    }

    /// The left edge of the band drawn behind the spine, which runs from
    /// the first row to the last.
    pub fn spine_left(&self) -> i32 {
        SPINE - SPINE_BAND / 2
    }

    pub fn spine_width(&self) -> i32 {
        SPINE_BAND
    }

    pub fn place(&self, rect: &Rect) -> String {
        format!(
            "left: {}px; top: {}px; width: {}px; height: {}px",
            rect.x, rect.y, rect.width, rect.height
        )
    }

    pub fn box_class(&self, boxed: &SectionBox) -> &'static str {
        match boxed.look {
            Look::Plain => "section-box",
            Look::Short => "section-box section-box-short",
            Look::Workload => "section-box section-box-workload",
            Look::Machine => "section-box section-box-machine",
            Look::Ground => "section-box section-box-ground",
        }
    }
}

impl DrawnRow {
    pub fn style(&self) -> String {
        format!("top: {}px; height: {}px", self.top, self.height)
    }
}

/// Collects the boxes of the spine.  When the stores-on edge kind is
/// off, the section has no claim or outside-system boxes, and when runs-on is
/// off, it has no machines.  `Entry::build` finds the Ingresses and
/// Services through the routes-to and selects edges of
/// `contents.graph`, so a graph without those edges gives no entry
/// boxes.
fn parts(object: &Object, layer: &str, href: &str, contents: &Contents) -> Parts {
    let entry = Entry::build(
        contents.graph,
        object,
        contents.services,
        contents.ingresses,
        contents.endpoints,
        contents.issuers,
    )
    .with_traffic(contents.traffic);
    let workload = contents.workloads.get(object);
    let running = workload.running();
    let ingresses = entry.ingresses.iter().map(|ingress| {
        let title = sentence::ingress_title(ingress);
        let mut lines = vec![sentence::ingress_line(ingress)];
        lines.extend(sentence::traffic_lines(ingress));
        SectionBox::new(title, lines, Look::Plain).with_href(ingress.href())
    });
    let services = entry.services.iter().map(|service| {
        let look = if service.short() {
            Look::Short
        } else {
            Look::Plain
        };
        let title = format!("Service {}", service.name);
        SectionBox::new(title, vec![sentence::service_line(service)], look)
            .with_href(service.href())
    });
    let line = sentence::workload_line(workload.replicas(), workload.ready(), running.len());
    let title = transect_graph::edges::kind_and_name(&object.kind, &object.name);
    let workload_box = SectionBox::new(title, vec![line], Look::Workload);
    let backings = gather::backings(
        contents.storage,
        object.namespace.as_deref().unwrap_or_default(),
        &running,
        &workload.templates(),
    );
    let hosts = gather::hosts(&running, contents.machines);
    Parts {
        layer: layer.into(),
        routes: routes(contents.graph, object, &entry),
        ingresses: ingresses.collect(),
        services: services.collect(),
        workload: Some(workload_box.with_href(Some(href.into()))),
        backings,
        hosts,
        ..Parts::default()
    }
}

/// Returns which Service each Ingress routes to, from the routes-to
/// edges, as pairs of indexes into the entry's lists of Ingresses and
/// Services.
fn routes(graph: &Graph, workload: &Object, entry: &Entry) -> Vec<(usize, usize)> {
    let namespace = workload.namespace.as_deref();
    let mut routes = Vec::new();
    for (j, service) in entry.services.iter().enumerate() {
        let to = Endpoint::Object(Object::new("", "Service", namespace, &service.name));
        let from = graph.reaching(Kind::RoutesTo, &to);
        for (i, ingress) in entry.ingresses.iter().enumerate() {
            let routed = from.iter().any(|end| {
                matches!(end, Endpoint::Object(o) if o.kind == "Ingress" && o.name == ingress.name)
            });
            if routed {
                routes.push((i, j));
            }
        }
    }
    routes.sort();
    routes
}

fn drawn(rows: Vec<spine::Row>, placed: &Layout) -> Vec<DrawnRow> {
    rows.into_iter()
        .zip(&placed.rows)
        .map(|(row, at)| DrawnRow {
            band: row.band,
            top: at.top,
            height: at.height,
            boxes: row
                .tiers
                .into_iter()
                .zip(&at.boxes)
                .flat_map(|(tier, rects)| tier.into_iter().zip(rects.iter().copied()))
                .chain(row.left.into_iter().zip(at.left.iter().copied()))
                .chain(row.right.into_iter().zip(at.right.iter().copied()))
                .collect(),
        })
        .collect()
}
