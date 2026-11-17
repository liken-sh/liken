//! What a kind knowledge relation reaches on the map.  An object
//! resolves as an object entry of intent does, to a machine for a
//! `Node` and to the workloads behind a `Service`.  In addition, a
//! `ResourceClaim` that an object holds resolves to the devices its
//! allocation names, as the pods that hold one draw it.  An object of
//! a kind transect does not watch also resolves to itself, because an
//! operator's status names a real object.  An address resolves as a
//! configured one does, so an address in another namespace or outside
//! the cluster resolves to the same end as a pod's address would.

use super::StatedReading;
use transect_graph::edges::{Endpoint, Object};
use transect_ground::devices::Devices;
use transect_resources::knowledge::{Names, Reached, Relation};

/// What `relation` of `from` reaches.
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
                None => reading.stands_for(object),
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
