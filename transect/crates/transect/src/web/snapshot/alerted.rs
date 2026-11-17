//! What the firing alerts add to a reading: health's reasons, the marks
//! on the machines, and the lines of "Now".  Each matches what an alert
//! is about against the namespaces and machines of this same reading,
//! so an alert about a namespace that is gone counts as cluster-wide.

use std::collections::HashMap;

use k8s_openapi::jiff::Timestamp;

use super::Snapshot;
use transect_observe::alerts::{self, Alerted, Firing, Known};
use transect_observe::health::Reason;

impl Snapshot {
    fn known_to_alerts(&self) -> Known {
        let namespaces = self.namespaces.iter();
        let nodes = self.nodes.iter().filter_map(|node| {
            let name = node.metadata.name.clone()?;
            let status = node.status.as_ref();
            let addresses = status.and_then(|status| status.addresses.as_ref());
            let addresses = addresses.into_iter().flatten();
            Some((name, addresses.map(|a| a.address.clone()).collect()))
        });
        let source = self.resources.metrics_source.as_ref();
        Known {
            namespaces: namespaces
                .filter_map(|namespace| namespace.metadata.name.clone())
                .collect(),
            nodes: nodes.collect(),
            source: source
                .and_then(|source| source.alerts.clone())
                .unwrap_or_default(),
        }
    }

    pub(super) fn alert_reasons(&self, now: Timestamp) -> Vec<(String, Reason)> {
        alerts::reasons(self.alerts.alerts(), &self.known_to_alerts(), now)
    }

    pub(super) fn machine_alerts(&self) -> HashMap<String, Alerted> {
        alerts::on_machines(self.alerts.alerts(), &self.known_to_alerts())
    }

    /// Every firing alert as the lines "Now" opens with.
    pub fn firing(&self, now: Timestamp) -> Vec<Firing> {
        alerts::firing(self.alerts.alerts(), &self.known_to_alerts(), now)
    }
}
