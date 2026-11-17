//! What changed over the last day, read from the snapshot's objects.

use super::Snapshot;
use transect_supply::changed::{self, Inputs, Recorded};
use transect_supply::forge::Forges;

impl Snapshot {
    /// Every change the objects still record.  `forges` are the forges
    /// the cluster serves, so a revision an applier applied links to its
    /// commit.
    pub fn recorded(&self, forges: &Forges) -> Recorded {
        let inputs = Inputs {
            replica_sets: &self.replica_sets,
            controller_revisions: &self.controller_revisions,
            jobs: &self.jobs,
            cron_jobs: &self.cron_jobs,
            pods: &self.pods,
            nodes: &self.nodes,
            revisions: self.gitops.revisions(),
            ownership: &self.ownership,
            plurals: &self.plurals,
            forges,
        };
        changed::read(&inputs)
    }
}
