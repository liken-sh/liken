//! A namespace whose pods mostly belong to `DaemonSet`s is drawn as a
//! matrix, with one row per `DaemonSet` and one column per machine. A
//! box per `DaemonSet` that says "on 10 machines" does not show which
//! machine is missing its pod, and a matrix does. The namespace's other
//! top owners are listed by name below the matrix.

mod sentence;

use std::collections::HashMap;
use std::sync::Arc;

use k8s_openapi::api::apps::v1::DaemonSet;
use k8s_openapi::api::core::v1::{Node, Pod};
use k8s_openapi::jiff::Timestamp;
use kube::ResourceExt;

pub use sentence::{DAEMON_SETS, LEGEND, OTHERS};
pub use transect_ground::placement::Skip;

use transect_graph::edges::{Object, Plurals};
use transect_graph::pods::finished;
use transect_ground::placement;
use transect_observe::health::{self, Health, Level, Reason};

#[derive(Debug, PartialEq)]
pub struct Matrix {
    /// The columns, one per machine: control plane machines first, then
    /// by name.
    pub machines: Vec<Column>,
    pub rows: Vec<Row>,
    /// How many unfinished pods the `DaemonSet`s have, for the header.
    pub pods: usize,
    pub others: Vec<Other>,
}

#[derive(Debug, PartialEq)]
pub struct Column {
    pub name: String,
    pub control_plane: bool,
}

#[derive(Debug, PartialEq)]
pub struct Row {
    pub name: String,
    pub href: Option<String>,
    /// One cell per machine, in the same order as the columns.
    pub cells: Vec<Cell>,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Cell {
    Ready,
    /// A container restarted within the last hour or is crash looping.
    /// `restarts` is the total of the pod's containers' restart counts.
    Restarting {
        restarts: i32,
    },
    NotReady,
    /// No pod runs on this machine, and the `DaemonSet`'s template does
    /// not exclude the machine.
    Missing,
    /// The `DaemonSet`'s template excludes this machine by node selector,
    /// node affinity, or an untolerated taint, so no pod is expected.
    Skipped(Skip),
}

/// A top owner that is not a `DaemonSet`, listed by name below the
/// matrix.
#[derive(Debug, PartialEq)]
pub struct Other {
    pub kind: String,
    pub name: String,
    pub href: Option<String>,
    /// Whether its health is `Warning` or worse. Its name then uses the
    /// `--warm` color, as its box does on the namespace page.
    pub warm: bool,
}

pub struct Sources<'a> {
    pub by_owner: &'a HashMap<Object, Vec<&'a Pod>>,
    pub daemon_sets: &'a [Arc<DaemonSet>],
    pub nodes: &'a [Arc<Node>],
    pub by_workload: &'a HashMap<Object, Health>,
    pub plurals: &'a Plurals,
    pub now: Timestamp,
}

/// Whether a namespace with these top owners is drawn as a matrix: it
/// has at least two `DaemonSet`s, and they run more than half of its
/// unfinished pods. The test counts pods, not owners, because a
/// namespace of per-node agents often also has a few one-replica
/// controllers, which can outnumber the `DaemonSet`s. A matrix with one
/// row shows less than the `DaemonSet`'s box does.
pub fn draws_as_matrix(workloads: &[&Object], by_owner: &HashMap<Object, Vec<&Pod>>) -> bool {
    let running = |workload: &Object| {
        let pods = by_owner.get(workload).into_iter().flatten();
        pods.filter(|pod| !finished(pod)).count()
    };
    let daemon_sets: Vec<&Object> = workloads
        .iter()
        .copied()
        .filter(|workload| is_daemon_set(workload))
        .collect();
    let on_daemon_sets: usize = daemon_sets.iter().map(|w| running(w)).sum();
    let all: usize = workloads.iter().map(|w| running(w)).sum();
    daemon_sets.len() >= 2 && on_daemon_sets * 2 > all
}

/// The matrix for `workloads`, the namespace's top owners. The columns
/// are in the order of the cluster map's machines band.
pub fn matrix(workloads: &[&Object], sources: &Sources) -> Matrix {
    let mut nodes: Vec<&Node> = sources.nodes.iter().map(AsRef::as_ref).collect();
    nodes.sort_by_key(|node| {
        (
            !transect_ground::machines::control_plane(node),
            node.name_any(),
        )
    });
    let no_pods = Vec::new();
    let mut rows = Vec::new();
    let mut others = Vec::new();
    let mut pods = 0;
    for &workload in workloads {
        if !is_daemon_set(workload) {
            others.push(Other {
                kind: workload.kind.clone(),
                name: workload.name.clone(),
                href: transect_graph::links::page(workload, sources.plurals),
                warm: sources
                    .by_workload
                    .get(workload)
                    .is_some_and(|health| health.level >= Level::Warning),
            });
            continue;
        }
        let running: Vec<&Pod> = sources
            .by_owner
            .get(workload)
            .unwrap_or(&no_pods)
            .iter()
            .copied()
            .filter(|pod| !finished(pod))
            .collect();
        pods += running.len();
        let template = sources
            .daemon_sets
            .iter()
            .find(|ds| ds.namespace() == workload.namespace && ds.name_any() == workload.name)
            .and_then(|ds| ds.spec.template.spec.as_ref());
        let cells = nodes
            .iter()
            .map(|node| {
                let here: Vec<&Pod> = running
                    .iter()
                    .copied()
                    .filter(|pod| on_node(pod, node))
                    .collect();
                match (here.is_empty(), template) {
                    (false, _) => worst(&here, sources.now),
                    (true, Some(template)) => {
                        placement::skip(template, node).map_or(Cell::Missing, Cell::Skipped)
                    }
                    (true, None) => Cell::Missing,
                }
            })
            .collect();
        rows.push(Row {
            name: workload.name.clone(),
            href: transect_graph::links::page(workload, sources.plurals),
            cells,
        });
    }
    rows.sort_by(|a, b| a.name.cmp(&b.name));
    others.sort_by(|a, b| a.name.cmp(&b.name).then_with(|| a.kind.cmp(&b.kind)));
    Matrix {
        machines: nodes
            .iter()
            .map(|node| Column {
                name: node.name_any(),
                control_plane: transect_ground::machines::control_plane(node),
            })
            .collect(),
        rows,
        pods,
        others,
    }
}

fn is_daemon_set(workload: &Object) -> bool {
    workload.group == "apps" && workload.kind == "DaemonSet"
}

fn on_node(pod: &Pod, node: &Node) -> bool {
    let name = pod.spec.as_ref().and_then(|spec| spec.node_name.as_deref());
    name.is_some() && name == node.metadata.name.as_deref()
}

/// The worst state among the pods on one machine. A `DaemonSet` has two
/// pods on a machine while a rollout replaces one, and the cell shows
/// the one that needs attention.
fn worst(pods: &[&Pod], now: Timestamp) -> Cell {
    let restarting = pods.iter().find(|pod| {
        health::pod_reasons(pod, now).iter().any(|reason| {
            matches!(
                reason,
                Reason::Restarting { .. } | Reason::CrashLooping { .. }
            )
        })
    });
    if let Some(pod) = restarting {
        return Cell::Restarting {
            restarts: restarts(pod),
        };
    }
    if pods.iter().all(|pod| transect_graph::pods::ready(pod)) {
        Cell::Ready
    } else {
        Cell::NotReady
    }
}

fn restarts(pod: &Pod) -> i32 {
    let status = pod.status.as_ref();
    let statuses = status.and_then(|s| s.container_statuses.as_ref());
    statuses
        .into_iter()
        .flatten()
        .map(|container| container.restart_count)
        .sum()
}

#[cfg(test)]
mod tests;
