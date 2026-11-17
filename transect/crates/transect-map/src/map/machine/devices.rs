//! A machine's devices as its page reads them: named hardware, by
//! driver, each with a few facts.  A driver's device knowledge names the
//! attribute that titles a device and the attributes to show.  Without
//! that knowledge, a device is titled by its own name and every
//! attribute is a fact.  Devices of one driver that share a PCI bus ID,
//! a Kubernetes standard attribute, are one piece of hardware, such as a
//! GPU's render and display nodes, and the page draws them as one.

use transect_graph::edges::{Device, Object};
use transect_graph::links::Reference;
use transect_ground::devices::Published;
use transect_ground::devices::knowledge::DeviceKnowledge;

/// The Kubernetes standard attribute that names a device's PCI address.
const PCI_BUS_ID: &str = "resource.kubernetes.io/pciBusID";

/// The most pieces of hardware no claim holds that one driver lists
/// unfolded.
const FREE_SHOWN: usize = 6;

/// The most pieces of hardware a driver's box shows in one column.
/// Past that count, the box spans the devices box and lays them out in
/// columns.
const ONE_COLUMN: usize = 6;

/// The hardware one driver publishes on the machine.
#[derive(Debug, PartialEq)]
pub struct Driver {
    pub name: String,
    pub devices: Vec<Hardware>,
}

/// One piece of hardware: a device, or the devices that share its PCI
/// bus ID.
#[derive(Debug, PartialEq)]
pub struct Hardware {
    pub title: String,
    /// Whether its driver's knowledge gave the title; without it, the
    /// title is the device's own name.
    pub titled: bool,
    /// The id of each device it represents, the first on its line, so a
    /// link to any of them opens here.
    pub anchors: Vec<String>,
    /// Every device it represents, by every part that names it.
    pub hint: String,
    /// The other devices drawn in it, by name.
    pub parts: Vec<String>,
    /// Each fact in words, such as "driver usbhid" or "connected".
    pub facts: Vec<String>,
    /// What its attributes name, each with the verb that names it.
    pub names: Vec<(String, Reference)>,
    /// The claims that hold its devices, each once, by namespace and
    /// name, each linked to its page.
    pub claims: Vec<Reference>,
}

impl Driver {
    /// The hardware a claim holds, which always shows.
    pub fn held(&self) -> Vec<&Hardware> {
        self.devices
            .iter()
            .filter(|d| !d.claims.is_empty())
            .collect()
    }

    /// The hardware no claim holds, which folds to its count past a
    /// few, as a board's long lists do.
    pub fn free(&self) -> Vec<&Hardware> {
        self.devices
            .iter()
            .filter(|d| d.claims.is_empty())
            .collect()
    }

    pub fn folds(&self) -> bool {
        self.free().len() > FREE_SHOWN
    }

    /// How much hardware it publishes and how much of it a claim
    /// holds: "6 devices · 2 held".
    pub fn count(&self) -> String {
        let all = self.devices.len();
        let noun = if all == 1 { "device" } else { "devices" };
        match self.held().len() {
            0 => format!("{all} {noun} · none held"),
            held => format!("{all} {noun} · {held} held"),
        }
    }

    /// Whether it shows more hardware unfolded than one column shows.
    pub fn wide(&self) -> bool {
        let free = if self.folds() { 0 } else { self.free().len() };
        self.held().len() + free > ONE_COLUMN
    }
}

impl Hardware {
    /// Its facts in one line: "driver usbhid · bus usb".
    pub fn facts_line(&self) -> String {
        self.facts.join(" · ")
    }

    /// What its attributes name, each verb once with everything it
    /// names.  The devices of one piece of hardware share a title, such
    /// as a GPU's render and display nodes, so the page writes a title
    /// once.
    pub fn references(&self) -> Vec<(&str, Vec<&Reference>)> {
        let mut gathered: Vec<(&str, Vec<&Reference>)> = Vec::new();
        for (verb, named) in &self.names {
            match gathered.iter_mut().find(|(seen, _)| seen == verb) {
                Some((_, things)) if things.iter().any(|t| t.text == named.text) => {}
                Some((_, things)) => things.push(named),
                None => gathered.push((verb, vec![named])),
            }
        }
        gathered
    }
}

/// What a machine's page reads of its devices.
pub struct Read<'a> {
    /// The attributes of a device the slices list.
    pub attributes: &'a dyn Fn(&Device) -> &'a [(String, String)],
    pub knowledge: &'a [DeviceKnowledge],
    /// What a device's attributes name, each with its verb.
    pub names: &'a dyn Fn(&Device) -> Vec<(String, Reference)>,
    /// The page of a ResourceClaim.
    pub href: &'a dyn Fn(&transect_graph::edges::Object) -> Option<String>,
}

/// `published` by driver, in the order of the drivers' names, each
/// driver's hardware in the order the slices list it.
pub fn by_driver(published: Vec<Published>, read: &Read) -> Vec<Driver> {
    let mut drivers: Vec<Driver> = Vec::new();
    let mut seen: Vec<(String, String)> = Vec::new();
    for one in &published {
        let driver = &one.device.driver;
        let pci = value(read, one.device, PCI_BUS_ID);
        if let Some(pci) = &pci {
            let key = (driver.clone(), pci.clone());
            if seen.contains(&key) {
                continue;
            }
            seen.push(key);
        }
        let group: Vec<&Published> = match &pci {
            Some(pci) => published
                .iter()
                .filter(|other| {
                    other.device.driver == *driver
                        && value(read, other.device, PCI_BUS_ID).as_ref() == Some(pci)
                })
                .collect(),
            None => vec![one],
        };
        let hardware = hardware(&group, read);
        match drivers.iter_mut().find(|d| d.name == *driver) {
            Some(found) => found.devices.push(hardware),
            None => drivers.push(Driver {
                name: driver.clone(),
                devices: vec![hardware],
            }),
        }
    }
    drivers.sort_by(|a, b| transect_graph::dns::order(&a.name, &b.name));
    drivers
}

/// One piece of hardware from the devices of `group`, titled and
/// described by the first of them.
fn hardware(group: &[&Published], read: &Read) -> Hardware {
    let mut group = group.to_vec();
    group.sort_by(|a, b| a.device.name.cmp(&b.device.name));
    let first = group[0].device;
    let known = read.knowledge.iter().find(|k| k.driver == first.driver);
    let attributes = (read.attributes)(first);
    let facts: Vec<String> = match known {
        Some(known) => known
            .facts
            .iter()
            .filter_map(|fact| {
                let (_, value) = attributes
                    .iter()
                    .find(|(name, _)| *name == fact.attribute)?;
                fact.said(value)
            })
            .collect(),
        None => attributes
            .iter()
            .map(|(name, value)| format!("{name} {value}"))
            .collect(),
    };
    let mut names: Vec<(String, Reference)> = Vec::new();
    for one in &group {
        for named in (read.names)(one.device) {
            if !names.contains(&named) {
                names.push(named);
            }
        }
    }
    let mut claims: Vec<&Object> = group.iter().flat_map(|one| &one.claims).collect();
    claims.sort();
    claims.dedup();
    let known_title = transect_ground::devices::knowledge::known_title(
        read.knowledge,
        (read.attributes)(first),
        first,
    );
    Hardware {
        titled: known_title.is_some(),
        title: known_title.unwrap_or_else(|| first.name.clone()),
        anchors: group
            .iter()
            .map(|one| transect_graph::links::device_anchor(&one.device.driver, &one.device.name))
            .collect(),
        hint: group
            .iter()
            .map(|one| one.device.full())
            .collect::<Vec<_>>()
            .join("\n"),
        parts: group[1..]
            .iter()
            .map(|one| one.device.name.clone())
            .collect(),
        facts,
        names,
        claims: claims
            .into_iter()
            .map(|claim| Reference {
                text: format!(
                    "{}/{}",
                    claim.namespace.as_deref().unwrap_or_default(),
                    claim.name
                ),
                href: (read.href)(claim),
            })
            .collect(),
    }
}

fn value(read: &Read, device: &Device, attribute: &str) -> Option<String> {
    let attributes = (read.attributes)(device);
    let found = attributes.iter().find(|(name, _)| name == attribute);
    found.map(|(_, value)| value.clone())
}

#[cfg(test)]
mod tests;
