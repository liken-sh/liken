//! Finds the workload that runs each `IngressClass`'s controller: the
//! workload that runs the image an `IngressController` declares for
//! it. When no `IngressController` declares one, `Serving` guesses
//! among the workloads that a `LoadBalancer` `Service` sends web
//! traffic to.

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
        // `vital` returns a `HashMap`. Sort so the guess does not
        // depend on its iteration order.
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
