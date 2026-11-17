//! The conditions `transect`'s resources get when what they name
//! matches nothing in this reading of the cluster.  A resource that
//! loads but names a label, a path, a `Service`, or a driver that
//! nothing here holds fills nothing, and without a condition the
//! section it would fill is empty with no reason given.

mod devices;
mod entry;
mod forges;
mod kinds;
mod metrics;

use super::Snapshot;
use transect_resources::resources::{Condition, DocId};
use transect_supply::forge::Forges;

impl Snapshot {
    /// The conditions of each resource whose names match nothing here.
    /// `usage` is the last reading of the usage queries, and `forges`
    /// the forges the resources declare with those the cluster serves.
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
