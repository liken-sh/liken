//! Boxes that each say "on 10 machines" hide the one machine that a pod
//! is missing from, so a namespace of DaemonSets draws as a matrix, with
//! one row per DaemonSet and one column per machine.  Each cell shows
//! whether the pod there is ready, restarting, or not ready, or why
//! there is none.  The namespace's other top owners show below as a row
//! of names.

mod sentence;

use std::collections::HashMap;
use std::sync::Arc;

use k8s_openapi::api::apps::v1::DaemonSet;
use k8s_openapi::api::core::v1::{Node, Pod};
use k8s_openapi::jiff::Timestamp;
use kube::ResourceExt;

pub use sentence::{DAEMON_SETS, LEGEND, OTHERS};
pub use transect_ground::placement::Skip;

use transect_cluster::workloads::pod_ready;
use transect_graph::edges::{Object, Plurals};
use transect_graph::pods::finished;
use transect_ground::placement;
use transect_observe::health::{self, Health, Level, Reason};

#[derive(Debug, PartialEq)]
pub struct Matrix {
    /// The columns: every machine, the control plane first and then by
    /// name.
    pub machines: Vec<Column>,
    pub rows: Vec<Row>,
    /// How many pods the DaemonSets run, for the matrix's header.
    pub pods: usize,
    /// The namespace's top owners that are not DaemonSets.
    pub others: Vec<Other>,
}

/// One machine's column.
#[derive(Debug, PartialEq)]
pub struct Column {
    pub name: String,
    pub control_plane: bool,
}

#[derive(Debug, PartialEq)]
pub struct Row {
    pub name: String,
    pub href: Option<String>,
    /// One per machine, in the columns' order.
    pub cells: Vec<Cell>,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Cell {
    Ready,
    /// A container restarted within the hour or is crash looping.  The
    /// count is the pod's total, as Kubernetes keeps it.
    Restarting {
        restarts: i32,
    },
    NotReady,
    /// Nothing keeps a pod off this machine, yet none runs here.
    Missing,
    /// The DaemonSet keeps its pod off this machine by design.
    Skipped(Skip),
}

/// A top owner that is not a DaemonSet, named in the row below.
#[derive(Debug, PartialEq)]
pub struct Other {
    pub kind: String,
    pub name: String,
    pub href: Option<String>,
    /// Whether its health is at `Warning` or worse, so its name takes
    /// --warm as its box would.
    pub warm: bool,
}

/// What the matrix is read from.
pub struct Sources<'a> {
    pub by_owner: &'a HashMap<Object, Vec<&'a Pod>>,
    pub daemon_sets: &'a [Arc<DaemonSet>],
    pub nodes: &'a [Arc<Node>],
    pub by_workload: &'a HashMap<Object, Health>,
    pub plurals: &'a Plurals,
    pub now: Timestamp,
}

/// Whether a namespace with these top owners draws as a matrix: its
/// DaemonSets run more than half of its running pods, and there are at
/// least two of them.  Pods, not owners, are counted, since a namespace
/// of agents often keeps a handful of one-replica controllers beside
/// them, and those would outnumber the DaemonSets while running a tenth
/// of the pods.  A matrix of one row shows less than its box does.
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

/// The matrix of `workloads`, the namespace's top owners.  The rows and
/// the names below are sorted by name, and the columns put the control
/// plane first, as the machines band does, then sort by machine name.
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

/// The worst state among the pods on one machine.  A DaemonSet runs
/// two there only while a rollout replaces one, and the cell shows the
/// one that needs attention.
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
    if pods.iter().all(|pod| pod_ready(pod)) {
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
