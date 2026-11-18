//! The rule for one workload: the number of its ready pods outside the
//! lost set, compared with the replicas it declares.

use k8s_openapi::api::core::v1::Pod;

use transect_cluster::workloads::{Controller, Workload, pod_ready};

/// What a loss leaves of a workload that had a pod on the lost set.  A
/// workload that keeps as many ready pods as it declares is not
/// affected and has no state.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum State {
    /// No ready pod outside the lost set.
    Down { wanted: i32 },
    /// Some ready pods outside the lost set, but fewer than declared.
    Short { ready: i32, wanted: i32 },
    /// At least one ready pod outside the lost set, but the primary is
    /// inside it.  The primary is the pod that accepts writes, and the
    /// workload's kind knowledge names it in `primary`.  Writes stop
    /// until the workload's operator promotes a replica.
    Primary { ready: i32, wanted: i32 },
}

impl State {
    /// Whether the workloads that depend on this workload lose it,
    /// because it is down or accepts no writes until a replica is
    /// promoted.
    pub fn stops(&self) -> bool {
        matches!(self, Self::Down { .. } | Self::Primary { .. })
    }

    /// Whether the workload is exactly one replica short.  After its
    /// first few lines, the sidebar shows only a count of these.
    pub fn one_short(&self) -> bool {
        matches!(self, Self::Short { ready, wanted } if wanted - ready == 1)
    }
}

/// How the rule treats a workload.
#[derive(Debug, PartialEq, Eq)]
pub enum Role {
    /// A `DaemonSet` runs one pod per machine by design, so the loss of
    /// a machine never leaves it short.
    DaemonSet,
    /// A `CronJob` or a `Job`.  Its pods run to completion and are not
    /// replicas, so the loss gives it no state.
    Scheduled,
    /// Any other workload, with the replicas it declares.  For a
    /// workload that sets no count, such as a pod with no owner, the
    /// count is the number of pods it runs.
    Replicas(i32),
}

pub fn role(workload: &Workload) -> Role {
    let parts = workload.parts;
    let daemon = parts
        .iter()
        .all(|part| part.controller == Controller::DaemonSet);
    if !parts.is_empty() && daemon {
        return Role::DaemonSet;
    }
    if workload.scheduled() || workload.object.kind == "Job" {
        return Role::Scheduled;
    }
    let wanted = workload.replicas();
    Role::Replicas(wanted.unwrap_or(workload.running().len() as i32))
}

/// The state of a workload that declares `wanted` replicas and runs the
/// pods in `running`.  `lost` returns whether a pod is on the lost set,
/// and `primary_lost` is whether the workload's primary is among those
/// pods.  A workload scaled to zero loses nothing.  The loss of the
/// primary stops writes however many replicas are left, so the function
/// checks it before the count.
pub fn state(
    wanted: i32,
    running: &[&Pod],
    lost: impl Fn(&Pod) -> bool,
    primary_lost: bool,
) -> Option<State> {
    let outside = running.iter().filter(|pod| !lost(pod) && pod_ready(pod));
    let ready = outside.count() as i32;
    if wanted <= 0 {
        None
    } else if ready == 0 {
        Some(State::Down { wanted })
    } else if primary_lost {
        Some(State::Primary { ready, wanted })
    } else if ready >= wanted {
        None
    } else {
        Some(State::Short { ready, wanted })
    }
}
