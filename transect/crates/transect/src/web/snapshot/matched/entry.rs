//! The conditions of an `IngressController` whose controller no
//! `IngressClass` names, whose image no pod runs, or whose entry
//! queries answer with no series that has its backend label, and of a
//! `LoadBalancerController` whose listener keys no pod has.

use super::super::Snapshot;
use transect_graph::pods::finished;
use transect_network::entry::runs;
use transect_resources::resources::{Condition, DocId};

impl Snapshot {
    pub(super) fn unmatched_controllers(&self) -> Vec<(DocId, Condition)> {
        let mut found = Vec::new();
        for controller in &self.resources.ingress_controllers {
            let mut say =
                |message: String| found.push((controller.doc, Condition::unmatched(message)));
            let classes = self.paged.ingress_classes.iter();
            let named = classes
                .filter_map(|class| class.spec.as_ref()?.controller.as_deref())
                .any(|named| named == controller.controller);
            if !named {
                say(format!(
                    "no IngressClass names the controller {}",
                    controller.controller
                ));
            }
            let pods = self.pods.iter().filter(|pod| !finished(pod));
            if !pods.into_iter().any(|pod| runs(pod, &controller.image)) {
                say(format!(
                    "no running pod runs the image {}",
                    controller.image
                ));
            }
            let measured = self.entry.iter().find(|m| m.controller == controller.name);
            if let Some(measured) = measured
                && measured.backends.is_empty()
            {
                say(format!(
                    "the entry queries return no series with the label {}",
                    measured.queries.label
                ));
            }
        }
        found
    }
}

impl Snapshot {
    pub(super) fn unmatched_load_balancers(&self) -> Vec<(DocId, Condition)> {
        let controllers = self.resources.load_balancers.iter();
        let unmatched = controllers
            .filter(|c| !transect_access::access::ways::listeners::matches_any(c, &self.pods));
        unmatched
            .map(|controller| {
                let message = format!(
                    "no pod has the keys that LoadBalancerController {} reads a listener's Service from",
                    controller.name
                );
                (controller.doc, Condition::unmatched(message))
            })
            .collect()
    }
}
