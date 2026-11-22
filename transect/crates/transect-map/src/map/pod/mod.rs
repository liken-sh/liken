//! The pod elevation: what runs inside one pod and where it runs.  A
//! row at the top switches between the pods of the pod's workload.
//! Below it is a block for each container, then the pod's mounts, the
//! names of the Secrets and ConfigMaps it reads, and the machine it
//! runs on with the devices it holds.

mod containers;
mod mounts;
mod placement;
mod sentence;

use std::collections::HashMap;
use std::sync::Arc;

use askama::Template;
use k8s_openapi::api::core::v1::Pod;
use k8s_openapi::jiff::Timestamp;
use kube::ResourceExt;

use containers::{ContainerBlock, blocks};
use mounts::{MountRow, rows};
use placement::{Placement, placement};
use sentence::Chip;

use super::ClusterMap;
use super::bar;
use super::workload::tiles::{State, tiles};
use transect_cluster::ownership::Ownership;
use transect_cluster::workloads::Workloads;
use transect_frame::frame::crumbs::Crumb;
use transect_frame::frame::tabs::{Lens, Tabs};
use transect_graph::edges::{Object, Plurals};
use transect_graph::environment::{self, Environment, Kind as SourceKind, Source};
use transect_graph::links::page;
use transect_graph::pods::finished;
use transect_ground::devices::{self, Devices};
use transect_ground::machines::Machine;
use transect_ground::storage::Storage;
use transect_observe::logs::{self, Tails};
use transect_observe::usage::Usage;
use transect_resources::intent::SecretIntent;
use transect_supply::images::Origins;

/// How many log lines the page shows for each container, as a preview
/// of the logs lens.
const PREVIEW: usize = 5;

/// The pod page.  Its `main` block renders alone as the fragment that a
/// pushed update swaps in, as on every elevation.
#[derive(Template)]
#[template(path = "map/pod/pod.html", blocks = ["main"])]
pub struct PodPage {
    pub namespace: String,
    /// The map band that contains the namespace.
    pub layer: String,
    pub name: String,
    /// The page's own path, which the events URL is built from.
    pub path: String,
    /// The pod's top owner, or `None` for a pod that has no owner.
    pub workload: Option<WorkloadLink>,
    pub switches: Vec<Switch>,
    pub containers: Vec<ContainerBlock>,
    /// The pod's QoS class, which the chips of each non-init container
    /// show.
    pub qos: Option<String>,
    pub mounts: Vec<MountRow>,
    pub environment: Environment,
    /// The title of each `Secret` the pod reads, by name, which says
    /// how transect reads that `Secret`'s intent.
    pub secret_titles: HashMap<String, String>,
    pub placement: Placement,
    /// Why the usage bars are empty.  It is `None` for a finished pod.
    pub usage_note: Option<transect_resources::resources::OffLine>,
    /// The last log lines read for this page, or `None` before the first
    /// read.
    pub logs: Option<Arc<Tails>>,
}

/// The pod's top owner, which comes before the pod in the crumb.
#[derive(Debug, PartialEq)]
pub struct WorkloadLink {
    pub name: String,
    /// Its page, or `None` for a kind that has no workload page.
    pub href: Option<String>,
}

#[derive(Debug, PartialEq)]
pub struct Switch {
    pub label: String,
    pub href: String,
    pub current: bool,
    /// Whether the pod is running or waiting to run, and is not ready.
    pub not_ready: bool,
}

impl Switch {
    pub fn class(&self) -> String {
        let mut class = String::from("switch");
        if self.current {
            class.push_str(" switch-current");
        }
        if self.not_ready {
            class.push_str(" switch-warning");
        }
        class
    }
}

/// What the page reads besides the map.
pub struct Contents<'a> {
    pub pods: &'a [Arc<Pod>],
    pub workloads: &'a Workloads<'a>,
    pub ownership: &'a Ownership,
    pub plurals: &'a Plurals,
    pub machines: &'a [Machine],
    pub storage: &'a Storage,
    pub devices: &'a Devices,
    pub usage: Option<&'a Usage>,
    pub usage_note: Option<transect_resources::resources::OffLine>,
    pub origins: Origins<'a>,
    pub secrets: &'a SecretIntent,
    pub now: Timestamp,
}

impl PodPage {
    pub fn with_logs(self, logs: Option<Arc<Tails>>) -> Self {
        Self { logs, ..self }
    }

    /// Builds the page for the pod `namespace/name`.  Returns `None`
    /// when the cluster has no such pod or `map` has no such namespace.
    pub fn build(
        map: &ClusterMap,
        namespace: &str,
        name: &str,
        contents: Contents,
    ) -> Option<Self> {
        let layer = map.layer_of(namespace)?.to_owned();
        let pod = contents
            .pods
            .iter()
            .find(|pod| pod.namespace().as_deref() == Some(namespace) && pod.name_any() == name)?;
        let object = Object::new("", "Pod", Some(namespace), name);
        let owner = contents.ownership.top_owner(&object);
        let (workload, siblings): (Option<WorkloadLink>, Vec<&Pod>) = if owner == object {
            (None, vec![pod])
        } else {
            let link = WorkloadLink {
                name: owner.name.clone(),
                href: page(&owner, contents.plurals),
            };
            (Some(link), contents.workloads.get(&owner).pods.to_vec())
        };
        let reading = !finished(pod);
        let running: Vec<&Pod> = contents
            .pods
            .iter()
            .map(AsRef::as_ref)
            .filter(|other| other.namespace().as_deref() == Some(namespace))
            .filter(|other| !finished(other))
            .collect();
        let environment: Environment = pod.spec.as_ref().map(Environment::read).unwrap_or_default();
        let secret_titles = environment
            .sources
            .iter()
            .filter(|source| source.kind == SourceKind::Secret)
            .map(|source| {
                let title = contents.secrets.title(namespace, &source.name);
                (source.name.clone(), title)
            })
            .collect();
        Some(Self {
            namespace: namespace.into(),
            layer,
            name: name.into(),
            path: format!("/namespaces/{namespace}/pods/{name}"),
            switches: switches(&siblings, name, contents.now),
            workload,
            containers: blocks(pod, contents.usage, contents.origins),
            qos: pod.status.as_ref().and_then(|s| s.qos_class.clone()),
            mounts: rows(
                contents.storage,
                pod,
                &running,
                (contents.ownership, contents.plurals),
            ),
            environment,
            secret_titles,
            placement: placement(
                pod,
                contents.machines,
                contents.devices.of_pod(pod),
                contents.now,
            ),
            usage_note: contents.usage_note.clone().filter(|_| reading),
            logs: None,
        })
    }

    /// The layer's crumb links to the cluster map with this namespace
    /// selected.
    pub fn crumbs(&self) -> Vec<Crumb> {
        let select = format!("/?select={}", self.namespace);
        let mut crumbs = vec![
            Crumb::home(),
            Crumb::link(&self.layer, format!("{select}#{}", self.layer)),
            Crumb::link_name(&self.namespace, format!("/namespaces/{}", self.namespace)),
        ];
        if let Some(workload) = &self.workload {
            crumbs.push(match &workload.href {
                Some(href) => Crumb::link_name(&workload.name, href.clone()),
                None => Crumb::current_name(&workload.name),
            });
        }
        crumbs.push(Crumb::current_name(&self.name));
        crumbs
    }

    pub fn tabs(&self) -> Tabs {
        Tabs::pod(&self.path, Lens::Overview)
    }

    pub fn events_url(&self) -> String {
        format!("/events{}", self.path)
    }

    pub fn role(&self, block: &ContainerBlock) -> &'static str {
        sentence::role(block.role)
    }

    pub fn command(&self, block: &ContainerBlock) -> Option<String> {
        block.command.as_deref().map(sentence::command)
    }

    pub fn ports(&self, block: &ContainerBlock) -> Option<String> {
        sentence::ports(&block.ports)
    }

    pub fn chips(&self, block: &ContainerBlock) -> Vec<Chip> {
        sentence::chips(block, self.qos.as_deref())
    }

    pub fn memory_title(&self, block: &ContainerBlock) -> String {
        bar::memory_title(&block.memory)
    }

    pub fn cpu_title(&self, block: &ContainerBlock) -> String {
        bar::cpu_title(&block.cpu)
    }

    pub fn memory_figures(&self, block: &ContainerBlock) -> (String, String) {
        sentence::memory_figures(&block.memory)
    }

    pub fn cpu_figures(&self, block: &ContainerBlock) -> (String, String) {
        sentence::cpu_figures(&block.cpu)
    }

    pub fn preview_note(&self) -> String {
        format!("the last {PREVIEW} lines of each container")
    }

    pub fn preview<'l>(&self, lines: &'l [logs::Line]) -> &'l [logs::Line] {
        &lines[lines.len().saturating_sub(PREVIEW)..]
    }

    pub fn logs_href(&self) -> String {
        format!("{}/logs", self.path)
    }

    pub fn logs_forbidden(&self) -> &'static str {
        logs::FORBIDDEN
    }

    pub fn logs_empty(&self) -> &'static str {
        logs::EMPTY
    }

    /// The title of `source` when it is a `Secret`.
    pub fn source_title(&self, source: &Source) -> Option<&str> {
        match source.kind {
            SourceKind::Secret => self.secret_titles.get(&source.name).map(String::as_str),
            SourceKind::ConfigMap => None,
        }
    }

    pub fn names_only(&self) -> &'static str {
        environment::NAMES_ONLY
    }

    pub fn machine_line(&self) -> Option<String> {
        self.placement.machine.as_ref().map(sentence::machine_line)
    }

    pub fn placement_line(&self) -> String {
        sentence::placement_line(&self.placement)
    }

    /// What the pod holds, or `None` when it holds no device.
    pub fn holds_line(&self) -> Option<String> {
        let runs_on = self.placement.machine.as_ref().map(|m| m.name.as_str());
        devices::pod_holds(&self.placement.held, runs_on)
    }

    /// Every device the pod holds, by its full name, for the line's
    /// title.
    pub fn holds_title(&self) -> String {
        devices::all(&self.placement.held)
    }
}

/// Returns the row of pods in the order of the tiles on the workload's
/// page, so every pod page of the workload shows the same order.
fn switches(pods: &[&Pod], current: &str, now: Timestamp) -> Vec<Switch> {
    let namespace = pods.first().and_then(|pod| pod.namespace());
    tiles(pods, None, now)
        .into_iter()
        .map(|tile| Switch {
            label: sentence::switch_label(&tile.short_name, tile.machine.as_deref()),
            href: format!(
                "/namespaces/{}/pods/{}",
                namespace.as_deref().unwrap_or_default(),
                tile.name
            ),
            current: tile.name == current,
            not_ready: tile.state == State::NotReady,
        })
        .collect()
}
