//! Why a pod on a lost machine cannot start on another machine.
//!
//! When Kubernetes evicts a lost machine's pods, their controllers
//! create replacements on other machines.  `Stranded` lists the reasons
//! that a pod's workload stays down anyway, and the sidebar shows the
//! reason for each workload.

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
    /// The pod holds a device on the machine.  `device` is the device's
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
    /// The reason's position in the order that the sidebar lists the
    /// reasons.
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

/// The cluster state that `Stranding::stranded` reads.
pub struct Stranding<'r> {
    pub storage: &'r Storage,
    pub devices: &'r Devices,
    pub nodes: &'r [Arc<Node>],
}

impl Stranding<'_> {
    /// Why `pod` cannot start on another machine while the machines in
    /// `lost` are down, or `None` when it can.  It returns the first
    /// reason that applies, in the order of `rank`, except that a disk
    /// and an attached volume come in the order of the pod's volumes.
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
    /// pod's tolerations of the taint that Kubernetes puts on a lost
    /// machine.  `None` when the pod has no spec.
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

/// `Stranded::StatefulSet` for a pod whose controlling owner is a
/// `StatefulSet` and that is assigned to a machine.
fn stateful_set(pod: &Pod) -> Option<Stranded> {
    let owners = pod.metadata.owner_references.iter().flatten();
    let mut controllers = owners.filter(|owner| owner.controller == Some(true));
    controllers.find(|owner| owner.kind == "StatefulSet")?;
    let machine = pod.spec.as_ref()?.node_name.clone()?;
    Some(Stranded::StatefulSet { machine })
}

#[cfg(test)]
mod tests;
