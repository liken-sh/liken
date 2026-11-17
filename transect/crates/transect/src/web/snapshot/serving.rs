//! The workload that serves each `IngressClass`'s Ingresses: the one
//! that runs the image an `IngressController` declares for the class's
//! controller, or a guess from the workloads that take the cluster's
//! web traffic.

use super::Snapshot;
use transect_graph::edges::Object;
use transect_ground::loss;
use transect_network::entry::Serving;

impl Snapshot {
    pub fn serving(&self) -> Serving {
        let vital = loss::vital(
            &self.pods,
            &self.services,
            &self.ownership,
            self.alerts_service(),
        );
        let mut way_in: Vec<Object> = vital
            .into_iter()
            .filter(|(_, vital)| *vital == loss::Vital::WayIn)
            .map(|(object, _)| object)
            .collect();
        way_in.sort();
        Serving::build(
            &self.paged.ingress_classes,
            &self.resources.ingress_controllers,
            &self.pods,
            &self.ownership,
            way_in,
        )
    }
}
