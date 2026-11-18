//! The conditions for a `DeviceKnowledge` entry that matches nothing:
//! a driver that no `ResourceSlice` publishes a device of, or an
//! attribute that none of the driver's devices has.

use super::super::Snapshot;
use transect_resources::resources::{Condition, DocId};

impl Snapshot {
    pub(super) fn unmatched_devices(&self) -> Vec<(DocId, Condition)> {
        if self.devices.off.is_some() {
            return Vec::new();
        }
        let mut found = Vec::new();
        for known in &self.resources.devices {
            let devices: Vec<_> = self
                .devices
                .every_listed()
                .filter(|device| device.driver == known.driver)
                .collect();
            let mut report =
                |message: String| found.push((known.doc, Condition::unmatched(message)));
            if devices.is_empty() {
                report(format!(
                    "no ResourceSlice publishes a device of the driver {}",
                    known.driver
                ));
                continue;
            }
            let has = |attribute: &str| {
                devices.iter().any(|device| {
                    let attributes = self.devices.attributes(device);
                    attributes.iter().any(|(name, _)| name == attribute)
                })
            };
            let title = known.title.iter().map(|title| ("title".to_string(), title));
            let facts = known.facts.iter().enumerate();
            let facts = facts.map(|(index, fact)| (format!("facts[{index}]"), &fact.attribute));
            let references = known.references.iter().enumerate();
            let references =
                references.map(|(index, r)| (format!("references[{index}]"), &r.attribute));
            for (field, attribute) in title.chain(facts).chain(references) {
                if !has(attribute) {
                    report(format!(
                        "{field} refers to the attribute {attribute}, which no device of the driver has"
                    ));
                }
            }
        }
        found
    }
}
