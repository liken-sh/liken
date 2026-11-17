//! Why a pod on a lost machine cannot start anywhere else until the
//! machine returns.
//!
//! When Kubernetes evicts a lost machine's pods, a `Deployment` starts
//! replacements on other machines.  A replacement that needs a volume
//! or a device on the lost machine waits for that machine.  A
//! replacement that the scheduler can place on no other machine,
//! because of its node selector, its node affinity, or a taint, waits
//! until a machine that accepts it exists.  A pod that Kubernetes never
//! evicts or never replaces waits for its own machine.  In each case
//! the pod's workload stays down until the machine returns, and the
//! sidebar shows which of these reasons the workload waits on.

use std::sync::Arc;

use k8s_openapi::api::core::v1::{Node, Pod};

use super::Lost;
use crate::devices::Devices;
use crate::placement::{self, Eviction};
use crate::storage::{Pin, Storage, Type};

/// The reason a pod cannot start on any machine but the lost machine.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Stranded {
    /// The pod's volume is on the machine's own disk.
    Disk { machine: String },
    /// The pod's volume, of `kind`, is attached to the machine.
    Attached { kind: Type, machine: String },
    /// The pod holds a device on the machine, named by the device's
    /// short name.
    Device { device: String, machine: String },
    /// The scheduler can place the pod on no machine outside the lost
    /// set, because of its node selector, its required node affinity,
    /// or a taint that it does not tolerate.
    NoFit,
    /// The pod belongs to a `StatefulSet`.  A `StatefulSet` creates a
    /// pod's replacement only after the pod is deleted.  The deletion
    /// needs the kubelet on the pod's machine to confirm it, and the
    /// kubelet on a lost machine never does.
    StatefulSet { machine: String },
    /// The pod tolerates the loss of its machine with no time limit, so
    /// Kubernetes never evicts it and nothing replaces it.
    NeverEvicted,
}

impl Stranded {
    /// The position of the reason in the order that the sidebar groups
    /// the reasons by.
    pub fn rank(&self) -> usize {
        match self {
            Self::Disk { .. } => 0,
            Self::Attached { .. } => 1,
            Self::Device { .. } => 2,
            Self::NoFit => 3,
            Self::StatefulSet { .. } => 4,
            Self::NeverEvicted => 5,
        }
    }
}

/// The stores that transect compares a pod with to find whether the pod
/// is stranded.
pub struct Stranding<'r> {
    pub storage: &'r Storage,
    pub devices: &'r Devices,
    pub nodes: &'r [Arc<Node>],
}

impl Stranding<'_> {
    /// Why `pod` cannot start on another machine while the machines in
    /// `lost` are down, or `None` when it can.  The function checks the
    /// volumes and devices on the lost machine first, in `rank`'s order,
    /// because a volume on a lost machine is the more common reason and
    /// the harder one to recover from.
    pub fn stranded(&self, pod: &Pod, lost: &Lost) -> Option<Stranded> {
        let pins = self.storage.pins(pod).into_iter();
        let pinned = pins
            .filter(|pin| lost.contains(pin.machine()))
            .map(|pin| match pin {
                Pin::Disk { machine, .. } => Stranded::Disk { machine },
                Pin::Attached { kind, machine, .. } => Stranded::Attached { kind, machine },
            });
        let devices = self.devices.of_pod(pod).into_iter();
        let held = devices.filter_map(|device| {
            let machine = device.machine.clone().filter(|m| lost.contains(m))?;
            Some(Stranded::Device {
                device: device.short(),
                machine,
            })
        });
        let found = pinned.chain(held).next();
        found
            .or_else(|| self.no_fit(pod, lost))
            .or_else(|| stateful_set(pod))
            .or_else(|| self.never_evicted(pod))
    }

    fn no_fit(&self, pod: &Pod, lost: &Lost) -> Option<Stranded> {
        let spec = pod.spec.as_ref()?;
        let mut outside = self.nodes.iter().filter(|node| {
            let name = node.metadata.name.as_deref().unwrap_or_default();
            !lost.contains(name)
        });
        (!outside.any(|node| placement::fits(spec, node))).then_some(Stranded::NoFit)
    }

    fn never_evicted(&self, pod: &Pod) -> Option<Stranded> {
        (self.eviction(pod)? == Eviction::Never).then_some(Stranded::NeverEvicted)
    }

    /// When Kubernetes evicts `pod` after its machine is lost, from the
    /// taint that Kubernetes puts on a lost machine.
    pub fn eviction(&self, pod: &Pod) -> Option<Eviction> {
        let spec = pod.spec.as_ref()?;
        let node = spec.node_name.as_deref().and_then(|name| self.node(name));
        Some(placement::eviction(spec, placement::lost_taint(node)))
    }

    /// Whether every machine in `lost` is really down, which means that
    /// its `Node`'s `Ready` condition is not `True`.  A cordoned machine
    /// that is still ready is not lost.
    pub fn really_lost(&self, lost: &Lost) -> bool {
        lost.names().all(|name| {
            let node = self.node(name);
            let conditions = node.and_then(|n| n.status.as_ref()?.conditions.as_ref());
            let mut conditions = conditions.into_iter().flatten();
            !conditions.any(|c| c.type_ == "Ready" && c.status == "True")
        })
    }

    /// The `Node` named `name`, if the cluster has it.
    pub fn node(&self, name: &str) -> Option<&Node> {
        let mut nodes = self.nodes.iter().map(AsRef::as_ref);
        nodes.find(|node| node.metadata.name.as_deref() == Some(name))
    }
}

/// The reason for a pod whose controlling owner reference names a
/// `StatefulSet`.
fn stateful_set(pod: &Pod) -> Option<Stranded> {
    let owners = pod.metadata.owner_references.iter().flatten();
    let mut controllers = owners.filter(|owner| owner.controller == Some(true));
    controllers.find(|owner| owner.kind == "StatefulSet")?;
    let machine = pod.spec.as_ref()?.node_name.clone()?;
    Some(Stranded::StatefulSet { machine })
}

#[cfg(test)]
mod tests;
