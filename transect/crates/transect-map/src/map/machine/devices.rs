//! A machine's devices as its page shows them: the hardware that each
//! driver publishes, with a few facts for each piece. A driver's
//! `DeviceKnowledge` says which attribute gives a device's title and
//! which attributes to show as facts. Without it, the title is the
//! device's name and every attribute is a fact. Devices of one driver
//! that have the same PCI bus ID are one piece of hardware, such as a
//! GPU's render and display nodes, and the page draws them together.

use transect_graph::edges::{Device, Object};
use transect_graph::links::Reference;
use transect_ground::devices::Published;
use transect_ground::devices::knowledge::DeviceKnowledge;

/// The standard Kubernetes attribute for a device's PCI address.
const PCI_BUS_ID: &str = "resource.kubernetes.io/pciBusID";

/// A driver lists up to this many pieces of hardware that no claim
/// holds. When it has more, the page shows only how many there are.
const FREE_SHOWN: usize = 6;

/// A driver's box shows up to this many pieces of hardware in one
/// column. When it shows more, the box spans the whole devices box and
/// lays the hardware out in several columns.
const ONE_COLUMN: usize = 6;

/// The hardware that one driver publishes on the machine.
#[derive(Debug, PartialEq)]
pub struct Driver {
    pub name: String,
    pub devices: Vec<Hardware>,
}

/// One piece of hardware: a device, or the devices that share a PCI bus
/// ID.
#[derive(Debug, PartialEq)]
pub struct Hardware {
    pub title: String,
    /// Whether the title came from the driver's `DeviceKnowledge`.
    /// When false, the title is the device's name.
    pub titled: bool,
    /// The anchor id of each device in it, so a link to any of them
    /// points to this piece of hardware.
    pub anchors: Vec<String>,
    /// The full name of each device in it, one per line, for the
    /// tooltip.
    pub hint: String,
    /// The names of the devices drawn with the first one.
    pub parts: Vec<String>,
    /// Each fact in words, such as "driver usbhid" or "connected".
    pub facts: Vec<String>,
    /// What its devices' attributes refer to, each with the verb that
    /// describes the link.
    pub referenced: Vec<(String, Reference)>,
    /// The claims that hold its devices, each listed once as
    /// namespace/name and linked to its page.
    pub claims: Vec<Reference>,
}

impl Driver {
    /// The hardware that a claim holds. The page always shows it.
    pub fn held(&self) -> Vec<&Hardware> {
        self.devices
            .iter()
            .filter(|d| !d.claims.is_empty())
            .collect()
    }

    /// The hardware that no claim holds. When there is more than
    /// `FREE_SHOWN` of it, `folds` is true and the page shows a count.
    pub fn free(&self) -> Vec<&Hardware> {
        self.devices
            .iter()
            .filter(|d| d.claims.is_empty())
            .collect()
    }

    pub fn folds(&self) -> bool {
        self.free().len() > FREE_SHOWN
    }

    /// How much hardware it publishes and how much of that a claim
    /// holds, such as "6 devices · 2 held".
    pub fn count(&self) -> String {
        let all = self.devices.len();
        let noun = if all == 1 { "device" } else { "devices" };
        match self.held().len() {
            0 => format!("{all} {noun} · none held"),
            held => format!("{all} {noun} · {held} held"),
        }
    }

    /// Whether it shows more than `ONE_COLUMN` pieces of hardware.
    /// Hardware that the page shows only as a count does not count.
    pub fn wide(&self) -> bool {
        let free = if self.folds() { 0 } else { self.free().len() };
        self.held().len() + free > ONE_COLUMN
    }
}

impl Hardware {
    pub fn facts_line(&self) -> String {
        self.facts.join(" · ")
    }

    /// `referenced` grouped by verb, with each target once for its verb. The
    /// devices of one piece of hardware, such as a GPU's render and
    /// display nodes, often refer to the same targets.
    pub fn references(&self) -> Vec<(&str, Vec<&Reference>)> {
        let mut gathered: Vec<(&str, Vec<&Reference>)> = Vec::new();
        for (verb, named) in &self.referenced {
            match gathered.iter_mut().find(|(seen, _)| seen == verb) {
                Some((_, things)) if things.iter().any(|t| t.text == named.text) => {}
                Some((_, things)) => things.push(named),
                None => gathered.push((verb, vec![named])),
            }
        }
        gathered
    }
}

/// The lookups that `by_driver` uses to read each device.
pub struct Read<'a> {
    /// The attributes of a device, as the ResourceSlices list them.
    pub attributes: &'a dyn Fn(&Device) -> &'a [(String, String)],
    pub knowledge: &'a [DeviceKnowledge],
    /// What a device's attributes refer to, each with its verb.
    pub referenced: &'a dyn Fn(&Device) -> Vec<(String, Reference)>,
    /// The page link of a ResourceClaim.
    pub href: &'a dyn Fn(&transect_graph::edges::Object) -> Option<String>,
}

/// Groups `published` by driver, with the drivers in DNS name order and
/// each driver's hardware in the order the slices list it.
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

/// Builds one piece of hardware from the devices of `group`. The device
/// with the lowest name gives the title and the facts. The references
/// and the claims come from every device in the group.
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
                fact.text_for(value)
            })
            .collect(),
        None => attributes
            .iter()
            .map(|(name, value)| format!("{name} {value}"))
            .collect(),
    };
    let mut referenced: Vec<(String, Reference)> = Vec::new();
    for one in &group {
        for named in (read.referenced)(one.device) {
            if !referenced.contains(&named) {
                referenced.push(named);
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
        referenced,
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
