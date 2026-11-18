//! The edges that device knowledge declares.  A reference in device
//! knowledge draws an edge from a device to each device whose attribute,
//! or each object whose field, has the same value as one of the first
//! device's attributes, such as from an HDMI sound output to the screen
//! whose speakers it is.  When a matching device is on the same
//! machine, the edge goes only to it, because two machines connected to
//! the same monitor model report the same monitor identifier.

use std::collections::{BTreeSet, HashMap};

use super::Devices;
use super::knowledge::{DeviceKnowledge, ReferenceTarget};
use transect_graph::edges::{Device, Edge, Endpoint, Object, Provenance};
use transect_resources::knowledge::{Key, Matches};

/// The group, kind, and field of the target object of each reference
/// to an object.  These are the keys of the index that `Matches` builds.
pub fn matched(knowledge: &[DeviceKnowledge]) -> impl Iterator<Item = Key> + '_ {
    let references = knowledge.iter().flat_map(|known| &known.references);
    references.filter_map(|reference| match &reference.target {
        ReferenceTarget::Object {
            group,
            kind,
            matched,
        } => Some((group.clone(), kind.clone(), matched.clone())),
        ReferenceTarget::Device { .. } => None,
    })
}

/// Every edge that `knowledge` declares between the devices that
/// `devices` lists, and from those devices to the objects of `matches`.
/// Each target object ends at the endpoints that `endpoints_of`
/// returns for it.
pub fn edges(
    devices: &Devices,
    knowledge: &[DeviceKnowledge],
    matches: &Matches,
    endpoints_of: impl Fn(&Object) -> Vec<Endpoint>,
) -> Vec<Edge> {
    let by_value = index(devices, knowledge);
    let mut edges = Vec::new();
    for device in devices.every_listed() {
        let Some(known) = knowledge.iter().find(|known| known.driver == device.driver) else {
            continue;
        };
        let attributes = devices.attributes(device);
        for reference in &known.references {
            let value = attributes
                .iter()
                .find(|(name, _)| *name == reference.attribute);
            let Some((_, value)) = value else { continue };
            let ends: Vec<Endpoint> = match &reference.target {
                ReferenceTarget::Device { driver, matched } => {
                    let key = (driver.clone(), matched.clone(), value.clone());
                    let far = by_value.get(&key).map(Vec::as_slice).unwrap_or_default();
                    nearest(device, far)
                        .into_iter()
                        .map(|far| Endpoint::Device(far.clone()))
                        .collect()
                }
                ReferenceTarget::Object {
                    group,
                    kind,
                    matched,
                } => {
                    let far = matches.named(group, kind, matched, value).iter();
                    let ends = far.flat_map(&endpoints_of).collect::<BTreeSet<_>>();
                    ends.into_iter().collect()
                }
            };
            let from = Endpoint::Device(device.clone());
            for to in ends.into_iter().filter(|to| *to != from) {
                edges.push(Edge {
                    kind: reference.edge,
                    from: from.clone(),
                    to,
                    guessed: false,
                    provenance: Provenance::Devices {
                        driver: device.driver.clone(),
                        attribute: reference.attribute.clone(),
                        label: reference.label.clone(),
                    },
                });
            }
        }
    }
    edges
}

/// The devices of each driver that a reference targets, by driver,
/// attribute name, and attribute value.
type ByValue<'a> = HashMap<(String, String, String), Vec<&'a Device>>;

fn index<'a>(devices: &'a Devices, knowledge: &[DeviceKnowledge]) -> ByValue<'a> {
    let wanted: BTreeSet<(&str, &str)> = knowledge
        .iter()
        .flat_map(|known| &known.references)
        .filter_map(|reference| match &reference.target {
            ReferenceTarget::Device { driver, matched } => {
                Some((driver.as_str(), matched.as_str()))
            }
            ReferenceTarget::Object { .. } => None,
        })
        .collect();
    let mut found: ByValue = HashMap::new();
    for device in devices.every_listed() {
        for (name, value) in devices.attributes(device) {
            if wanted.contains(&(device.driver.as_str(), name.as_str())) {
                let key = (device.driver.clone(), name.clone(), value.clone());
                found.entry(key).or_default().push(device);
            }
        }
    }
    found
}

/// The devices of `far` that are on `device`'s machine, or all of
/// `far` when none is.
fn nearest<'a>(device: &Device, far: &[&'a Device]) -> Vec<&'a Device> {
    let here: Vec<&Device> = far
        .iter()
        .copied()
        .filter(|other| other.machine == device.machine)
        .collect();
    match here.is_empty() {
        true => far.to_vec(),
        false => here,
    }
}

#[cfg(test)]
mod tests {
    use std::sync::Arc;

    use k8s_openapi::api::resource::v1::ResourceSlice;
    use kube::core::ApiResource;
    use rstest::rstest;

    use super::*;
    use crate::devices::knowledge::fixtures::knowledge;

    /// A slice of `driver` on `machine` listing each device with its one
    /// attribute.
    fn slice(driver: &str, machine: &str, devices: &[(&str, &str, &str)]) -> ResourceSlice {
        let devices: Vec<String> = devices
            .iter()
            .map(|(name, key, value)| {
                format!("{{name: {name}, attributes: {{{key}: {{string: '{value}'}}}}}}")
            })
            .collect();
        serde_yaml::from_str(&format!(
            "metadata: {{name: {driver}-{machine}}}\nspec: {{driver: {driver}, nodeName: {machine}, pool: {{name: {machine}, generation: 1, resourceSliceCount: 1}}, devices: [{}]}}",
            devices.join(", ")
        ))
        .unwrap()
    }

    fn devices() -> Devices {
        let slices = [
            slice(
                "sound.example.com",
                "alpha",
                &[("hdmi-0", "monitor", "tv-1")],
            ),
            slice(
                "screen.example.com",
                "alpha",
                &[("hdmi-a-1", "monitor", "tv-1")],
            ),
            slice(
                "screen.example.com",
                "beta",
                &[("hdmi-a-2", "monitor", "tv-1")],
            ),
            slice(
                "radio.example.com",
                "alpha",
                &[("aa-bb", "address", "AA:BB")],
            ),
        ];
        Devices::build([], slices.iter())
    }

    /// The Pairings of radio.example.com, each with its `name` and
    /// the `address` in its status.
    fn pairings(pairs: &[(&str, &str)]) -> Matches {
        let resource = ApiResource {
            group: "radio.example.com".into(),
            version: "v1".into(),
            api_version: "radio.example.com/v1".into(),
            kind: "Pairing".into(),
            plural: "pairings".into(),
        };
        let objects = pairs.iter().map(|(name, address)| {
            let text = format!("metadata: {{name: {name}}}\nstatus: {{address: '{address}'}}");
            Arc::new(serde_yaml::from_str(&text).unwrap())
        });
        let key = (
            "radio.example.com".to_string(),
            "Pairing".to_string(),
            "status.address".to_string(),
        );
        Matches::build([key], &[(resource, objects.collect())])
    }

    /// Each edge as its from device's name and its far end's name.
    fn read(edges: &[Edge]) -> Vec<(String, String)> {
        let name = |end: &Endpoint| match end {
            Endpoint::Device(device) => device.name.clone(),
            Endpoint::Object(object) => object.name.clone(),
            _ => String::new(),
        };
        edges.iter().map(|e| (name(&e.from), name(&e.to))).collect()
    }

    #[rstest]
    #[case::to_the_device_on_its_own_machine(
        "sound.example.com",
        "{references: [{attribute: monitor, driver: screen.example.com, match: monitor, label: speakers of}]}",
        vec![("hdmi-0".into(), "hdmi-a-1".into())]
    )]
    #[case::to_the_object_whose_field_holds_it(
        "radio.example.com",
        "{references: [{attribute: address, kind: Pairing, group: radio.example.com, match: status.address, label: pairs as}]}",
        vec![("aa-bb".into(), "remote".into())]
    )]
    #[case::to_nothing_when_nothing_holds_it(
        "radio.example.com",
        "{references: [{attribute: address, driver: screen.example.com, match: monitor, label: shows on}]}",
        vec![]
    )]
    fn draws_an_edge_where_a_value_names_something(
        #[case] driver: &str,
        #[case] spec: &str,
        #[case] expected: Vec<(String, String)>,
    ) {
        let known = [knowledge(driver, spec)];
        let matches = pairings(&[("remote", "AA:BB")]);
        let endpoints_of = |object: &Object| vec![Endpoint::Object(object.clone())];
        let found = edges(&devices(), &known, &matches, endpoints_of);
        assert_eq!(read(&found), expected);
    }
}
