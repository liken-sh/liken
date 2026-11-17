//! The machine page: one Node as the machine it is, drawn as a board.
//! A strip at the top shows its role, whether it is ready, and how full
//! it is.  Under it, the pods that run there are in a box per layer, in
//! the map's order, with a box per namespace and a row per pod.  Then
//! come the devices it publishes by driver, the volumes on its disk or
//! attached to it, its own logs, and the resource that describes it
//! below the cluster, when kind knowledge's `node` names one.  The card
//! beside them shows what its kubelet reports and its labels.  The
//! cluster map answers what losing the machine would take, so the page
//! links there instead of answering it here.

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
    /// Its strip's figures: what it allocates, and what its pods request
    /// and use.
    pub machine: Machine,
    /// Every machine, to switch between.
    pub switches: Vec<Switch>,
    /// Its pods, by band and namespace.
    pub bands: Vec<Band>,
    /// What its pods' bars run to, under them.
    pub legend: String,
    /// Why the bars show no usage, when they do not.
    pub usage_note: Option<transect_resources::resources::OffLine>,
    /// The hardware it publishes, by driver.
    pub drivers: Vec<Driver>,
    /// Why it lists no device, when the holds edge kind is turned off.
    pub devices_note: Option<&'static str>,
    pub volumes: Vec<VolumeLine>,
    /// What the machine's own services logged.
    pub logs: OwnLogs,
    /// The custom resource that describes the same machine below the
    /// cluster, when there is one.
    pub underneath: Option<Underneath>,
    /// Why there is none, when kind knowledge names a kind that describes
    /// Nodes and no object of it describes this one.
    pub underneath_none: Option<String>,
}

/// One volume on the machine: its claim, and how it is there.
#[derive(Debug, PartialEq)]
pub struct VolumeLine {
    pub claim: Reference,
    pub how: String,
}

impl VolumeLine {
    /// `pin`'s claim, linked by `href` to its page, and how it is here.
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

/// The custom resource that describes the machine below the cluster,
/// as its kind knowledge's `node` names it.
#[derive(Debug, PartialEq)]
pub struct Underneath {
    /// Its kind and name, as "Machine alpha".
    pub title: String,
    /// Its own page, where its conditions and fields are listed in full.
    pub href: Option<String>,
    pub chips: Vec<Chip>,
    /// Its kind's knowledge, which a card section draws.
    pub known: Option<KnownThrough>,
}

impl Underneath {
    /// Where every mark in the section came from.
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

    /// Its lenses: this page, and the cluster map as it would be
    /// without it.
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

    /// "up 4d · 2 restarts", or "not ready · up 4d · 0 restarts".
    pub fn row_line(&self, tile: &PodTile) -> String {
        sentence::row(tile)
    }

    /// What the marks on the page mean, when any line is marked.
    pub fn legend(&self) -> Option<String> {
        let marks = self.underneath.iter().flat_map(Underneath::provenances);
        transect_graph::edges::provenance::legend(&marks.collect::<Vec<_>>())
    }
}
