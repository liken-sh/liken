//! What the ownership walk reads from the cluster's stores: every
//! object an owner chain can pass through, as the metadata the walk
//! reads.

use std::sync::Arc;

use k8s_openapi::apimachinery::pkg::apis::meta::v1::OwnerReference;
use kube::Resource;

use super::Cluster;
use kube::api::ObjectMeta;

use crate::ownership::{Entry, Ownership, StatusManagers};
use transect_graph::edges::Object;

impl Cluster {
    /// Every pod and workload walked to its top owner, as of now, and
    /// what `marks` reads of the same objects' labels and annotations,
    /// given each object that has some, with the ownership.
    pub fn ownership<R>(
        &self,
        marks: impl FnOnce(&mut dyn Iterator<Item = (Object, &ObjectMeta)>, &Ownership) -> R,
    ) -> (Ownership, R) {
        let custom = self.custom.snapshot();
        let (pods, replica_sets, jobs) = (self.pods(), self.replica_sets(), self.jobs());
        let (deployments, stateful_sets) = (self.deployments(), self.stateful_sets());
        let (daemon_sets, cron_jobs) = (self.daemon_sets(), self.cron_jobs());
        let services = self.services();
        let built_in = [
            entries("", "Pod", &pods),
            entries("apps", "ReplicaSet", &replica_sets),
            entries("apps", "Deployment", &deployments),
            entries("apps", "StatefulSet", &stateful_sets),
            entries("apps", "DaemonSet", &daemon_sets),
            entries("batch", "Job", &jobs),
            entries("batch", "CronJob", &cron_jobs),
            // A `Service` runs no pods, but one an operator made is
            // owned by the operator's resource, so `depends-on` treats
            // an address that names it as the workload's own.
            entries("", "Service", &services),
        ];
        let custom = custom
            .iter()
            .map(|(resource, metas)| entries(&resource.group, &resource.kind, metas));
        let entries: Vec<Entry> = built_in.into_iter().chain(custom).flatten().collect();
        let ownership = Ownership::build(entries.iter().copied());
        let mut marked = entries
            .iter()
            .filter(|entry| entry.meta.labels.is_some() || entry.meta.annotations.is_some())
            .filter_map(|entry| Some((entry.object()?, entry.meta)));
        let read = marks(&mut marked, &ownership);
        (ownership, read)
    }

    /// The managers that write each custom owner's status, which the
    /// custom kinds' watches keep from its managed fields.
    pub fn status_managers(&self) -> StatusManagers {
        let mut managers = StatusManagers::new();
        for (resource, metas) in self.custom.snapshot() {
            for meta in &metas {
                let entry = Entry {
                    group: &resource.group,
                    kind: &resource.kind,
                    meta: &meta.metadata,
                };
                let names: Vec<String> = meta
                    .metadata
                    .managed_fields
                    .iter()
                    .flatten()
                    .filter_map(|field| field.manager.clone())
                    .collect();
                if let (Some(object), false) = (entry.object(), names.is_empty()) {
                    managers.insert(object, names);
                }
            }
        }
        managers
    }

    /// Every owner reference on the pods, the workloads, and the custom
    /// kinds watched whole, which is where a new custom owner kind first
    /// appears.  A trimmed kind's owners are left out, because an object
    /// that owns only other custom objects owns no pod, so nothing reads
    /// its fields.
    pub(super) fn owner_references(&self) -> Vec<OwnerReference> {
        fn of<K: Resource>(objects: Vec<Arc<K>>) -> Vec<OwnerReference> {
            objects
                .iter()
                .flat_map(|object| object.meta().owner_references.clone().unwrap_or_default())
                .collect()
        }
        let custom = self.custom.whole().into_iter().map(of);
        [
            of(self.pods()),
            of(self.replica_sets()),
            of(self.jobs()),
            of(self.deployments()),
            of(self.stateful_sets()),
            of(self.daemon_sets()),
            of(self.cron_jobs()),
        ]
        .into_iter()
        .chain(custom)
        .flatten()
        .collect()
    }
}

fn entries<'a, K: Resource>(
    group: &'a str,
    kind: &'a str,
    objects: &'a [Arc<K>],
) -> Vec<Entry<'a>> {
    objects
        .iter()
        .map(|object| Entry {
            group,
            kind,
            meta: object.meta(),
        })
        .collect()
}
