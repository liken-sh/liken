//! Builders of edges and ends for tests.

use super::{Device, Edge, Endpoint, Kind, Object, Provenance};

pub fn pod(namespace: &str, name: &str) -> Endpoint {
    Endpoint::Object(Object::new("", "Pod", Some(namespace), name))
}

pub fn runs_on(namespace: &str, pod_name: &str, machine: &str) -> Edge {
    Edge {
        kind: Kind::RunsOn,
        from: pod(namespace, pod_name),
        to: Endpoint::Machine(machine.into()),
        guessed: false,
        provenance: Provenance::Kubernetes,
    }
}

/// A device in a pool named for its machine, as a node-local driver
/// names its pools.
pub fn device(driver: &str, machine: &str, name: &str) -> Device {
    Device {
        driver: driver.into(),
        pool: machine.into(),
        name: name.into(),
        machine: Some(machine.into()),
    }
}
