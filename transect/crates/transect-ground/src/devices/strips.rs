//! The devices that the selected namespace's workloads hold on each
//! machine, for the machine strips.

use super::sentence;
use crate::machines::Machine;
use transect_graph::edges::{Endpoint, Graph, Kind};

/// Fills each machine's `held` with the devices that `namespace`'s
/// workloads hold on it, from the `Holds` edges in `graph`, each once and
/// sorted by short name.
pub fn name_here(machines: &mut [Machine], graph: &Graph, namespace: &str) {
    for edge in graph.touching_namespace(namespace) {
        let (Kind::Holds, Endpoint::Device(device)) = (edge.kind, &edge.to) else {
            continue;
        };
        let Some(on) = &device.machine else { continue };
        let Some(machine) = machines.iter_mut().find(|machine| &machine.name == on) else {
            continue;
        };
        if !machine.held.contains(device) {
            machine.held.push(device.clone());
        }
    }
    for machine in machines {
        machine.held.sort_by_key(|device| device.short());
    }
}

impl Machine {
    /// The strip's line of devices held on this machine, or `None` when
    /// the selected namespace holds none here or no namespace is selected.
    pub fn held_text(&self) -> Option<String> {
        (!self.held.is_empty()).then(|| format!("holds {}", sentence::few(&self.held)))
    }

    /// Every device held here by its full name, for the line's title.
    pub fn held_title(&self) -> String {
        sentence::all(&self.held)
    }
}
