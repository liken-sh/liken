//! Resolves what a declared `Relation` of an object points at, to the
//! ends of the edges that the relation draws.
//!
//! A relation that names an object resolves to the ends that
//! `StatedReading::endpoints_of` gives. A `ResourceClaim` with an
//! allocation resolves to its allocated devices instead, which are the
//! same ends that the pods that use the claim draw edges to. A relation
//! that names an address resolves like an address in a pod spec, so it
//! reaches the same end that it would from a pod.

use super::StatedReading;
use transect_graph::edges::{Endpoint, Object};
use transect_ground::devices::Devices;
use transect_resources::knowledge::{Names, Reached, Relation};

/// The ends that `relation` of `from` points at.
pub fn reached(
    relation: &Relation,
    from: &Object,
    reading: &StatedReading,
    devices: &Devices,
) -> Reached {
    match &relation.names {
        Names::Object(object) => {
            let held = claim(object).then(|| devices.of_claim(object)).flatten();
            let ends = match held {
                Some(held) => held.iter().cloned().map(Endpoint::Device).collect(),
                None => reading.endpoints_of(object),
            };
            Reached {
                ends,
                guessed: false,
            }
        }
        Names::Address => {
            let namespace = from.namespace.as_deref();
            let found = reading.address(&relation.value, namespace);
            let (ends, guessed) = found.unwrap_or_default();
            Reached { ends, guessed }
        }
        Names::Value | Names::Ambiguous(_) => Reached::default(),
    }
}

fn claim(object: &Object) -> bool {
    object.group == "resource.k8s.io" && object.kind == "ResourceClaim"
}

#[cfg(test)]
mod tests;
