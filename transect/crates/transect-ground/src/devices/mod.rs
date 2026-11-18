//! The devices that Dynamic Resource Allocation allocates to pods, and
//! the `Holds` edges from each workload to the devices that its pods
//! hold.
//!
//! A claim's allocation names each device by driver, pool, and device
//! name, and the `ResourceSlice` that lists the device names its
//! machine.  A driver can stop listing a device that a running pod still
//! holds, so when no slice lists the device, the machine comes from the
//! node selector of the allocation.

mod card;
pub mod knowledge;
mod reach;
mod read;
mod sentence;
mod strips;
#[cfg(test)]
pub mod tests;

use std::collections::{HashMap, HashSet};

use k8s_openapi::api::core::v1::{NodeSelector, Pod};
use k8s_openapi::api::resource::v1::{Device as SliceDevice, ResourceClaim, ResourceSlice};

pub use card::Holds;
pub use reach::{edges as device_edges, matched as device_matched};
pub use sentence::{all, pod_holds};
pub use strips::name_here;
use transect_cluster::ownership::Ownership;
use transect_graph::edges::{Device, Edge, Endpoint, Kind, Object, Provenance};
use transect_graph::pods::finished;

/// Why transect cannot read the holds edges.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Off {
    /// The cluster does not serve `resource.k8s.io/v1`.
    NotServed,
    /// The cluster serves it, but transect may not list
    /// `ResourceClaim`s or `ResourceSlice`s.
    Forbidden,
    /// API discovery has not returned a result yet, and transect is
    /// trying again.
    Retrying,
}

/// The devices of every allocated claim, each with its machine, or the
/// reason that there are none.
#[derive(Debug, Default)]
pub struct Devices {
    pub off: Option<Off>,
    /// The devices of each claim, by its namespace and name.
    claims: HashMap<(String, String), Vec<Device>>,
    /// The devices that the slices list on each machine, by the
    /// machine's name, sorted.
    listed: HashMap<String, Vec<Device>>,
    /// Each listed device's attributes, by driver, pool, and name, each
    /// value as text, in the order of their names.
    attributes: HashMap<(String, String, String), Vec<(String, String)>>,
}

/// One device a machine publishes, and the claims that hold it.
#[derive(Debug, PartialEq)]
pub struct Published<'a> {
    pub device: &'a Device,
    /// The `ResourceClaim`s that hold the device, by namespace and name.
    /// A shared device has several.
    pub claims: Vec<Object>,
}

/// The key that identifies a device in a slice: its driver, pool, and
/// name.
type Listed<'a> = (&'a str, &'a str, &'a str);

impl Devices {
    pub fn off(off: Off) -> Self {
        Self {
            off: Some(off),
            ..Self::default()
        }
    }

    /// Follows each claim's allocation to the slice that lists each of
    /// its devices.  While a driver republishes a pool, a slice of the
    /// old pool generation can list a device beside a slice of the new
    /// one, so a device's machine and attributes come from the slice
    /// with the highest generation.
    pub fn build<'a>(
        claims: impl IntoIterator<Item = &'a ResourceClaim>,
        slices: impl IntoIterator<Item = &'a ResourceSlice>,
    ) -> Self {
        let mut machines: HashMap<Listed, (i64, Option<&str>)> = HashMap::new();
        let mut attributes: HashMap<Listed, &SliceDevice> = HashMap::new();
        for slice in slices {
            let spec = &slice.spec;
            let generation = spec.pool.generation;
            for device in spec.devices.iter().flatten() {
                let machine = device.node_name.as_deref().or(spec.node_name.as_deref());
                let key = (
                    spec.driver.as_str(),
                    spec.pool.name.as_str(),
                    device.name.as_str(),
                );
                let found = machines.entry(key).or_insert((generation, machine));
                if generation >= found.0 {
                    *found = (generation, machine);
                    attributes.insert(key, device);
                }
            }
        }
        let attributes = attributes
            .into_iter()
            .map(|((driver, pool, name), device)| {
                let key = (driver.to_string(), pool.to_string(), name.to_string());
                (key, texts(device))
            })
            .collect();
        let mut found = HashMap::new();
        for claim in claims {
            let (Some(namespace), Some(name)) = (&claim.metadata.namespace, &claim.metadata.name)
            else {
                continue;
            };
            let allocation = claim.status.as_ref().and_then(|s| s.allocation.as_ref());
            let results = allocation
                .and_then(|allocation| allocation.devices.as_ref())
                .and_then(|devices| devices.results.as_ref());
            let pinned = allocation
                .and_then(|allocation| allocation.node_selector.as_ref())
                .and_then(one_node);
            let devices: Vec<Device> = results
                .into_iter()
                .flatten()
                .map(|result| {
                    let key = (
                        result.driver.as_str(),
                        result.pool.as_str(),
                        result.device.as_str(),
                    );
                    Device {
                        driver: result.driver.clone(),
                        pool: result.pool.clone(),
                        name: result.device.clone(),
                        machine: match machines.get(&key) {
                            Some((_, machine)) => machine.map(Into::into),
                            None => pinned.map(Into::into),
                        },
                    }
                })
                .collect();
            if !devices.is_empty() {
                found.insert((namespace.clone(), name.clone()), devices);
            }
        }
        let mut listed: HashMap<String, Vec<Device>> = HashMap::new();
        for ((driver, pool, name), (_, machine)) in machines {
            let Some(machine) = machine else { continue };
            listed.entry(machine.into()).or_default().push(Device {
                driver: driver.into(),
                pool: pool.into(),
                name: name.into(),
                machine: Some(machine.into()),
            });
        }
        for devices in listed.values_mut() {
            devices.sort_by(|a, b| {
                transect_graph::dns::order(&a.driver, &b.driver).then_with(|| a.name.cmp(&b.name))
            });
        }
        Self {
            off: None,
            claims: found,
            listed,
            attributes,
        }
    }

    /// The attributes of `device`, from the slice that lists it, each
    /// value as text, in the order of their names.
    pub fn attributes(&self, device: &Device) -> &[(String, String)] {
        let key = (
            device.driver.clone(),
            device.pool.clone(),
            device.name.clone(),
        );
        self.attributes.get(&key).map_or(&[], Vec::as_slice)
    }

    /// Every device the slices list, on every machine.
    pub fn every_listed(&self) -> impl Iterator<Item = &Device> {
        self.listed.values().flatten()
    }

    /// The devices the slices list on `machine`, each with the
    /// allocated claims that hold it.
    pub fn published_on(&self, machine: &str) -> Vec<Published<'_>> {
        let mut held: HashMap<&Device, Vec<Object>> = HashMap::new();
        for ((namespace, name), devices) in &self.claims {
            let claim = Object::new("resource.k8s.io", "ResourceClaim", Some(namespace), name);
            for device in devices {
                held.entry(device).or_default().push(claim.clone());
            }
        }
        let listed = self.listed.get(machine).into_iter().flatten();
        listed
            .map(|device| {
                let mut claims = held.get(device).cloned().unwrap_or_default();
                claims.sort();
                Published { device, claims }
            })
            .collect()
    }

    /// The devices that the `ResourceClaim` `claim` holds, or `None`
    /// when transect has read no allocation for it.
    pub fn of_claim(&self, claim: &Object) -> Option<&[Device]> {
        let namespace = claim.namespace.clone()?;
        let devices = self.claims.get(&(namespace, claim.name.clone()))?;
        Some(devices.as_slice())
    }

    /// A `Devices` whose only allocated claim is `namespace/name`, which
    /// holds `devices`.  It is for a test that needs no slices.
    #[cfg(any(test, feature = "fixtures"))]
    pub fn with_claim(namespace: &str, name: &str, devices: Vec<Device>) -> Self {
        Self {
            off: None,
            claims: HashMap::from([((namespace.into(), name.into()), devices)]),
            listed: HashMap::new(),
            attributes: HashMap::new(),
        }
    }

    /// The devices `pod` holds, each once, in the order its claims and
    /// their allocations name them.
    pub fn of_pod(&self, pod: &Pod) -> Vec<Device> {
        let Some(namespace) = pod.metadata.namespace.as_deref() else {
            return Vec::new();
        };
        let mut held: Vec<Device> = Vec::new();
        for claim in claim_names(pod) {
            let devices = self.claims.get(&(namespace.to_owned(), claim.to_owned()));
            for device in devices.into_iter().flatten() {
                if !held.contains(device) {
                    held.push(device.clone());
                }
            }
        }
        held
    }
}

/// Each of `device`'s attributes by name, its value as text: a number
/// as its digits, a flag as true or false, and a version as written.
fn texts(device: &SliceDevice) -> Vec<(String, String)> {
    let attributes = device.attributes.iter().flatten();
    let texts = attributes.filter_map(|(name, value)| {
        let text = value
            .string
            .clone()
            .or_else(|| value.version.clone())
            .or_else(|| value.int.map(|int| int.to_string()))
            .or_else(|| value.bool.map(|flag| flag.to_string()))?;
        Some((name.clone(), text))
    });
    texts.collect()
}

/// The node that `selector` admits, when its only term matches
/// `metadata.name` In one node name and nothing else.  The scheduler
/// writes this selector when it allocates a device that only one node
/// has.
fn one_node(selector: &NodeSelector) -> Option<&str> {
    let [term] = selector.node_selector_terms.as_slice() else {
        return None;
    };
    if term
        .match_expressions
        .as_ref()
        .is_some_and(|e| !e.is_empty())
    {
        return None;
    }
    let [field] = term.match_fields.as_deref()? else {
        return None;
    };
    let values = field.values.as_deref()?;
    match (field.key.as_str(), field.operator.as_str(), values) {
        ("metadata.name", "In", [node]) => Some(node),
        _ => None,
    }
}

/// The names of the `ResourceClaim`s that `pod` uses.
/// `spec.resourceClaims` has the name of a claim made by hand, and only
/// `status.resourceClaimStatuses` has the name of a claim made from a
/// template.
fn claim_names(pod: &Pod) -> impl Iterator<Item = &str> {
    let named = pod
        .spec
        .iter()
        .flat_map(|spec| spec.resource_claims.iter().flatten());
    let named = named.filter_map(|claim| claim.resource_claim_name.as_deref());
    let made = pod
        .status
        .iter()
        .flat_map(|s| s.resource_claim_statuses.iter().flatten());
    let made = made.filter_map(|status| status.resource_claim_name.as_deref());
    named.chain(made)
}

/// An edge from the top owner of each pod to each device it holds, with
/// no repeats, so replicas that share a claim give one edge for each
/// device.  A pod that Succeeded or Failed is left out, because it no
/// longer runs.
pub fn holds<'a>(
    devices: &Devices,
    pods: impl IntoIterator<Item = &'a Pod>,
    ownership: &Ownership,
) -> Vec<Edge> {
    let mut seen: HashSet<(Object, Device)> = HashSet::new();
    let mut edges = Vec::new();
    for pod in pods.into_iter().filter(|pod| !finished(pod)) {
        let (Some(namespace), Some(name)) = (
            pod.metadata.namespace.as_deref(),
            pod.metadata.name.as_deref(),
        ) else {
            continue;
        };
        let owner = ownership.top_owner(&Object::new("", "Pod", Some(namespace), name));
        for device in devices.of_pod(pod) {
            if seen.insert((owner.clone(), device.clone())) {
                edges.push(Edge {
                    kind: Kind::Holds,
                    from: Endpoint::Object(owner.clone()),
                    to: Endpoint::Device(device),
                    guessed: false,
                    provenance: Provenance::Kubernetes,
                });
            }
        }
    }
    edges
}
