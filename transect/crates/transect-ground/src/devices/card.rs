//! A card's "Holds" lists the devices that the card's workloads hold,
//! with one holder for each workload and each device that it holds under
//! it.  When every device of a holder is on one machine, the holder's
//! line names that machine once.  When a holder's devices are on several
//! machines, as a driver's `DaemonSet`'s often are, each device's line
//! names the device's own machine.

use std::collections::BTreeMap;

use transect_graph::edges::{Device, Endpoint, Graph, Kind, Object};
use transect_graph::links::Reference;

#[derive(Debug, Default, PartialEq)]
pub struct Holds {
    pub rows: Vec<HoldRow>,
}

#[derive(Debug, PartialEq)]
pub struct HoldRow {
    /// The workload, as "DaemonSet radio", linked to its page.
    pub workload: Reference,
    /// The one machine every device is on, linked to its page.
    pub machine: Option<Reference>,
    /// How many machines the devices are on, when more than one.
    pub machines: Option<usize>,
    pub devices: Vec<Held>,
}

/// One device a holder holds.
#[derive(Debug, PartialEq)]
pub struct Held {
    /// The first label of the device's driver name, such as "radio",
    /// which the line shows in gray before the device.
    pub driver: String,
    /// The device by its title, linked to its line on its machine's
    /// page.
    pub device: Reference,
    /// The device's machine, when the holder's devices are on several
    /// machines.
    pub machine: Option<Reference>,
    /// Every part of the device's name, for the line's title.
    pub full: String,
}

impl Holds {
    /// The devices that `namespace`'s workloads hold, by workload name.
    /// Each workload links to the page that `href` returns for it, and
    /// each device has the name that `named` returns.
    pub fn of_namespace(
        graph: &Graph,
        namespace: &str,
        href: impl Fn(&Object) -> Option<String>,
        named: impl Fn(&Device) -> String,
    ) -> Self {
        let mut by_workload: BTreeMap<&Object, Vec<&Device>> = BTreeMap::new();
        for edge in graph.of_kind(Kind::Holds) {
            let (Endpoint::Object(from), Endpoint::Device(device)) = (&edge.from, &edge.to) else {
                continue;
            };
            if from.namespace.as_deref() == Some(namespace) {
                by_workload.entry(from).or_default().push(device);
            }
        }
        let rows = by_workload.into_iter().map(|(workload, mut devices)| {
            devices.sort_by(|a, b| {
                a.machine
                    .cmp(&b.machine)
                    .then_with(|| transect_graph::dns::order(&a.driver, &b.driver))
                    .then_with(|| a.name.cmp(&b.name))
            });
            devices.dedup();
            let mut machines: Vec<&str> = devices
                .iter()
                .filter_map(|d| d.machine.as_deref())
                .collect();
            machines.sort();
            machines.dedup();
            let spread = machines.len() > 1;
            let held = devices.iter().map(|device| {
                let driver = device.driver.split('.').next().unwrap_or(&device.driver);
                let text = named(device);
                let title = text.strip_prefix(&format!("{driver} ")).unwrap_or(&text);
                Held {
                    driver: driver.to_string(),
                    device: Reference {
                        text: title.to_string(),
                        href: transect_graph::links::device(device),
                    },
                    machine: device.machine.as_deref().filter(|_| spread).map(machine),
                    full: device.full(),
                }
            });
            HoldRow {
                workload: Reference {
                    text: transect_graph::edges::kind_and_name(&workload.kind, &workload.name),
                    href: href(workload),
                },
                machine: match machines.as_slice() {
                    [one] => Some(machine(one)),
                    _ => None,
                },
                machines: spread.then_some(machines.len()),
                devices: held.collect(),
            }
        });
        Self {
            rows: rows.collect(),
        }
    }
}

/// A machine, linked to its page.
fn machine(name: &str) -> Reference {
    Reference {
        text: name.to_string(),
        href: Some(transect_graph::links::machine(name)),
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;
    use transect_graph::edges::Edge;
    use transect_graph::edges::fixtures::device;

    /// The row as the card draws it: the holder and its machine, then
    /// each device indented on its own line.
    fn text(row: &HoldRow) -> Vec<String> {
        let place = match (&row.machine, row.machines) {
            (Some(machine), _) => format!(" on {}", machine.text),
            (None, Some(count)) => format!(" on {count} machines"),
            (None, None) => String::new(),
        };
        let devices = row.devices.iter().map(|held| match &held.machine {
            Some(machine) => format!("  {} {} on {}", held.driver, held.device.text, machine.text),
            None => format!("  {} {}", held.driver, held.device.text),
        });
        std::iter::once(format!("{}{place}", row.workload.text))
            .chain(devices)
            .collect()
    }

    fn held(namespace: &str, workload: &str, device: Device) -> Edge {
        Edge {
            kind: Kind::Holds,
            from: Endpoint::Object(Object::new("apps", "DaemonSet", Some(namespace), workload)),
            to: Endpoint::Device(device),
            guessed: false,
            provenance: transect_graph::edges::Provenance::Kubernetes,
        }
    }

    #[rstest]
    #[case::one_device(
        vec![held("shop", "radio", device("radio.example.com", "alpha", "usb-1"))],
        vec!["DaemonSet radio on alpha", "  radio usb-1"]
    )]
    #[case::devices_on_one_machine(
        vec![
            held("shop", "gpu", device("gpu.example.com", "alpha", "card-1")),
            held("shop", "gpu", device("gpu.example.com", "alpha", "card-0")),
        ],
        vec!["DaemonSet gpu on alpha", "  gpu card-0", "  gpu card-1"]
    )]
    #[case::devices_on_machines(
        vec![
            held("shop", "gpu", device("gpu.example.com", "beta", "card-0")),
            held("shop", "gpu", device("gpu.example.com", "alpha", "card-0")),
        ],
        vec!["DaemonSet gpu on 2 machines", "  gpu card-0 on alpha", "  gpu card-0 on beta"]
    )]
    #[case::a_holder_per_workload(
        vec![
            held("shop", "radio", device("radio.example.com", "alpha", "usb-1")),
            held("shop", "gpu", device("gpu.example.com", "beta", "card-0")),
        ],
        vec!["DaemonSet gpu on beta", "  gpu card-0", "DaemonSet radio on alpha", "  radio usb-1"]
    )]
    #[case::another_namespace(
        vec![held("blog", "radio", device("radio.example.com", "alpha", "usb-1"))],
        vec![]
    )]
    fn lists_each_holder_with_its_devices_under_it(
        #[case] edges: Vec<Edge>,
        #[case] lines: Vec<&str>,
    ) {
        let named = |device: &Device| device.short();
        let holds = Holds::of_namespace(&Graph::new([edges]), "shop", |_| None, named);
        let text: Vec<String> = holds.rows.iter().flat_map(text).collect();
        assert_eq!(text, lines);
    }

    #[rstest]
    fn links_each_device_to_its_line_on_its_machine() {
        let edges = vec![held(
            "shop",
            "radio",
            device("radio.example.com", "alpha", "usb-1"),
        )];
        let holds = Holds::of_namespace(
            &Graph::new([edges]),
            "shop",
            |_| None,
            |_| "Desk remote".into(),
        );
        let held = &holds.rows[0].devices[0];
        assert_eq!(held.device.text, "Desk remote");
        assert_eq!(
            held.device.href.as_deref(),
            Some("/machines/alpha#device-radio-example-com-usb-1")
        );
    }
}
