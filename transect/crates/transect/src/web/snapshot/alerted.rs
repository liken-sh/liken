//! Matches the labels of the firing alerts against the namespaces and
//! nodes of this snapshot, for the health reasons, the alerts on each
//! machine, and the lines at the top of "Now". An alert whose namespace
//! label names a namespace that is not in the snapshot attaches to the
//! machine its labels name, or to the whole cluster when they name
//! none.

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

    pub fn firing(&self, now: Timestamp) -> Vec<Firing> {
        alerts::firing(self.alerts.alerts(), &self.known_to_alerts(), now)
    }
}
