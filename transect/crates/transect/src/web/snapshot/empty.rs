//! The snapshot of a cluster with nothing in it, which tests and the
//! fixture fill.

use super::*;

impl Snapshot {
    /// A snapshot of a cluster with nothing in it, for a test or the
    /// fixture to fill.
    #[cfg(any(test, debug_assertions))]
    pub fn empty() -> Self {
        Self {
            namespaces: Vec::new(),
            nodes: Vec::new(),
            pods: Vec::new(),
            deployments: Vec::new(),
            stateful_sets: Vec::new(),
            daemon_sets: Vec::new(),
            cron_jobs: Vec::new(),
            budgets: Vec::new(),
            network_policies: Vec::new(),
            services: Vec::new(),
            ingresses: Vec::new(),
            endpoint_slices: Vec::new(),
            replica_sets: Vec::new(),
            controller_revisions: Vec::new(),
            jobs: Vec::new(),
            config_maps: Vec::new(),
            secrets: None,
            ownership: Ownership::default(),
            applied: Applied::default(),
            gitops: GitOps::off(transect_supply::gitops::Off::NoKnowledge),
            plurals: Plurals::default(),
            storage: Storage::default(),
            csi_drivers: Vec::new(),
            outsides: Outsides::default(),
            addresses: Addresses::default(),
            grants: Grants::default(),
            status_managers: StatusManagers::default(),
            devices: Devices::default(),
            images: Arc::default(),
            logs: Counted::default(),
            alerts: alerts::Reading::Off,
            entry: Default::default(),
            volumes_used: Default::default(),
            prometheus_pod: None,
            custom: Vec::new(),
            resources: Arc::default(),
            overlaid: Overlaid::default(),
            intents: Intents::default(),
            kinds: Kinds::default(),
            programs: Programs::default(),
            cluster_scoped: ClusterScoped::default(),
            relations: BTreeMap::new(),
            definitions: Definitions::default(),
            issuers: Default::default(),
            paged: Paged::default(),
            with_pages: HashSet::new(),
            presents: BTreeMap::new(),
            presented: Vec::new(),
            runners: Vec::new(),
        }
    }
}
