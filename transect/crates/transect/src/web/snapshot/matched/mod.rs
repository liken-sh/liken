//! Conditions for `transect`'s own resources that refer to something
//! this snapshot of the cluster does not have. A resource can load
//! without error and still refer to a label, a path, a `Service`, or a
//! driver that does not exist. The page section it feeds is then empty,
//! and without a condition nothing says why.

mod devices;
mod entry;
mod forges;
mod kinds;
mod metrics;

use super::Snapshot;
use transect_resources::resources::{Condition, DocId};
use transect_supply::forge::Forges;

impl Snapshot {
    /// The conditions of each resource whose references match nothing in
    /// this snapshot. `usage` is the latest reading of the usage queries.
    /// `forges` is the forges that resources declare, together with those
    /// that the cluster serves.
    pub fn unmatched(
        &self,
        usage: &transect_observe::usage::Reading,
        forges: &Forges,
    ) -> Vec<(DocId, Condition)> {
        let mut found = self.unmatched_kinds();
        found.extend(self.unmatched_controllers());
        found.extend(self.unmatched_load_balancers());
        found.extend(self.unmatched_forges(forges));
        found.extend(self.unmatched_metrics(usage));
        found.extend(self.unmatched_devices());
        found
    }
}
