use super::Snapshot;
use transect_supply::changed::{self, Inputs, Recorded};
use transect_supply::forge::Forges;

impl Snapshot {
    /// Every change that the snapshot's objects still record, at any
    /// age. A GitOps revision links to its commit when its repository
    /// is on a host of one of `forges`.
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
