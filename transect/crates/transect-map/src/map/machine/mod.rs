//! The page of one `Node`. A strip at the top shows the machine's role,
//! readiness, and capacity in use. Below it are the machine's pods,
//! grouped by layer in the cluster map's order and then by namespace.
//! Then come the devices the machine publishes, its volumes, the logs of
//! its own services, and the custom resource that describes the machine
//! below the cluster, when kind knowledge's `node` names a kind for it.
//!
//! The cluster map shows what the cluster would lose without this
//! machine, so this page links there instead of repeating it.

mod devices;
mod layered;
mod logs;
mod sentence;
mod switches;

use askama::Template;

use super::bar;
pub use super::workload::sentence::legend;
use super::workload::tiles::PodTile;
use crate::map::known::Chip;
pub use devices::{Driver, Read as DevicesRead, by_driver};
pub use layered::{Band, Reads, runs_here};
pub use logs::OwnLogs;
pub use switches::{Switch, switches};
use transect_frame::frame::crumbs::Crumb;
use transect_frame::frame::tabs::{Lens, Tabs};
use transect_graph::edges::Provenance;
use transect_graph::links::Reference;
use transect_ground::machines::{Facts, Machine};
use transect_ground::storage::Pin;
use transect_resources::knowledge::KnownThrough;

#[derive(Template)]
#[template(path = "map/machine/machine.html", blocks = ["main"])]
pub struct MachinePage {
    pub name: String,
    pub path: String,
    pub facts: Facts,
    /// What the machine can allocate, and what its pods request and use.
    pub machine: Machine,
    /// Links to every machine's page.
    pub switches: Vec<Switch>,
    /// The pods on this machine, grouped by layer and namespace.
    pub bands: Vec<Band>,
    /// The caption under the pods' usage bars that says what a full bar
    /// represents.
    pub legend: String,
    /// Why the bars show no usage, when they show none.
    pub usage_note: Option<transect_resources::resources::OffLine>,
    pub drivers: Vec<Driver>,
    /// Why no device is listed, when `transect` cannot read the
    /// cluster's devices.
    pub devices_note: Option<&'static str>,
    pub volumes: Vec<VolumeLine>,
    pub logs: OwnLogs,
    pub underneath: Option<Underneath>,
    /// Why `underneath` is empty, when kind knowledge names a kind that
    /// describes nodes and no object of that kind describes this one.
    pub underneath_none: Option<String>,
}

/// One volume on the machine: its `PersistentVolumeClaim`, and how the
/// volume is attached.
#[derive(Debug, PartialEq)]
pub struct VolumeLine {
    pub claim: Reference,
    pub how: String,
}

impl VolumeLine {
    pub fn of(pin: &Pin, href: impl Fn(&transect_graph::edges::Object) -> Option<String>) -> Self {
        let claim = pin.claim();
        let object = transect_graph::edges::Object::new(
            "",
            "PersistentVolumeClaim",
            Some(&claim.namespace),
            &claim.name,
        );
        Self {
            claim: Reference {
                text: format!("{}/{}", claim.namespace, claim.name),
                href: href(&object),
            },
            how: sentence::how(pin),
        }
    }
}

/// The custom resource that describes the machine below the cluster, of
/// the kind that kind knowledge's `node` names.
#[derive(Debug, PartialEq)]
pub struct Underneath {
    /// The resource's kind and name, such as "Machine alpha".
    pub title: String,
    pub href: Option<String>,
    pub chips: Vec<Chip>,
    /// What kind knowledge says about the kind.
    pub known: Option<KnownThrough>,
}

impl Underneath {
    /// The provenance of each mark in the section.
    pub fn provenances(&self) -> Vec<Provenance> {
        let known = self.known.iter();
        known
            .flat_map(|known| known.provenances().cloned())
            .collect()
    }
}

impl MachinePage {
    pub fn crumbs(&self) -> Vec<Crumb> {
        vec![
            Crumb::home(),
            Crumb::current("machines"),
            Crumb::current_name(&self.name),
        ]
    }

    pub fn events_url(&self) -> String {
        format!("/events{}", self.path)
    }

    /// The tabs: this page, and the cluster map with this machine lost.
    pub fn tabs(&self) -> Tabs {
        Tabs::machine(
            &transect_graph::links::machine(&self.name),
            &self.name,
            Lens::Overview,
        )
    }

    pub fn status_text(&self) -> String {
        sentence::status(&self.facts, self.machine.ready)
    }

    pub fn dot_class(&self) -> &'static str {
        match self.machine.ready {
            true => "dot dot-clear",
            false => "dot concern-failing",
        }
    }

    pub fn pods_text(&self) -> String {
        let namespaces = self.bands.iter().map(|band| band.namespaces.len()).sum();
        sentence::pods(self.machine.pods, namespaces)
    }

    pub fn memory_title(&self, tile: &PodTile) -> String {
        bar::memory_title(&tile.memory)
    }

    pub fn cpu_title(&self, tile: &PodTile) -> String {
        bar::cpu_title(&tile.cpu)
    }

    pub fn unscaled_note(&self, tile: &PodTile) -> Option<String> {
        bar::unscaled_note(&tile.memory, &tile.cpu)
    }

    /// The pod row's summary, such as "up 4d · 2 restarts" or "not ready
    /// · up 4d · 0 restarts".
    pub fn row_line(&self, tile: &PodTile) -> String {
        sentence::row(tile)
    }

    /// The key to the page's provenance marks, when the page has any.
    pub fn legend(&self) -> Option<String> {
        let marks = self.underneath.iter().flat_map(Underneath::provenances);
        transect_graph::edges::provenance::legend(&marks.collect::<Vec<_>>())
    }
}
