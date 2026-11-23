//! A small hand-written cluster.  Tests render every page from it, and
//! `--fixture` serves it with no real cluster.  The comments in each
//! file say what its objects are for:
//!
//! - `cluster.yaml`: the namespaces, workloads, and machines.
//! - `hardware.yaml`: the machines' devices.
//! - `storage.yaml`: a CSI driver with a console template.
//! - `parts.yaml`: a stock room's nested parts.
//! - `stores.yaml`: two stores that fill the pattern of a primary with
//!   replicas, one with replicas and one with one instance.
//! - `flux.yaml`: a GitOps tool and what it applies from.
//! - `entry.yaml`: the way in through an ingress controller.
//! - `network.yaml`: `NetworkPolicy` objects, a workload on its
//!   machine's network that no `NetworkPolicy` applies to, and a store
//!   whose ports three policies admit callers to.
//! - `access.yaml`: the accounts, roles, and bindings that
//!   `/access/accounts` draws.
//! - `ways.yaml`: the ways in besides the storefront's `Ingress`.
//! - `scans.yaml`: a vulnerability scanner's reports, current and
//!   stale.
//! - `resources.yaml`: `transect`'s own resources for the cluster.
//! - `gitops.yaml`: the knowledge the GitOps tool ships.
//! - `signals.yaml`: the firing alerts and the Prometheus responses.

mod load;
mod signals;

use std::sync::Arc;

use super::model::Model;
use super::snapshot::Snapshot;
use transect_cluster::cluster::Watch;
use transect_cluster::ownership::{Entry, Ownership};
#[cfg(test)]
use transect_graph::edges::Object;
use transect_ground::outside::Outsides;
use transect_ground::storage::Storage;
use transect_network::addresses::Addresses;
use transect_resources::knowledge::Kinds;
use transect_resources::resources::Overlaid;
use transect_supply::gitops::{Applied, GitOps};

/// The time at which tests read the fixture's objects and signals: 12
/// hours after 2026-01-01T00:00:00Z, the date that the fixture's
/// timestamps are written against.  A pinned time makes the goldens
/// draw the same ages on every run.
#[cfg(test)]
pub const NOW: &str = "2026-01-01T12:00:00Z";

/// The YAML of `transect`'s own resources for the fixture cluster,
/// followed by the knowledge objects of its GitOps tool.
const RESOURCES: &str = concat!(
    include_str!("resources.yaml"),
    "---\n",
    include_str!("gitops.yaml")
);

fn resources() -> crate::web::loaded::Loaded {
    transect_resources::resources::from_text("resources.yaml", RESOURCES)
}

/// The log tail that the fixture's log store returns for the pod
/// `namespace/pod`, read through the declared `LogSource`.
#[cfg(test)]
pub fn tails(namespace: &str, pod: &str) -> Option<Arc<transect_observe::logs::Tails>> {
    signals::tails(&resources(), namespace, pod)
}

/// The resource usage of every machine, pod, and container in the
/// fixture.
pub fn usage() -> transect_observe::usage::Reading {
    signals::usage(&resources())
}

pub fn snapshot() -> Snapshot {
    snapshot_with(resources())
}

/// The model and usage of the fixture cluster, built after `edit`
/// rewrites the text of its resources.  A test uses it to see what a
/// page shows when a resource is different.
#[cfg(test)]
pub fn edited(edit: impl Fn(&str) -> String) -> (Model, transect_observe::usage::Reading) {
    let loaded = transect_resources::resources::from_text("resources.yaml", &edit(RESOURCES));
    let usage = signals::usage(&loaded);
    (Model::build(snapshot_with(loaded)), usage)
}

fn snapshot_with(resources: crate::web::loaded::Loaded) -> Snapshot {
    let mut snapshot = Snapshot::empty();
    snapshot.resources = Arc::new(resources);
    snapshot.overlaid = Overlaid::new(snapshot.resources.directory.overlays.clone());
    snapshot.outsides = Outsides::new(&snapshot.resources.outsides);
    let yaml = concat!(
        include_str!("cluster.yaml"),
        "---\n",
        include_str!("hardware.yaml"),
        "---\n",
        include_str!("storage.yaml"),
        "---\n",
        include_str!("parts.yaml"),
        "---\n",
        include_str!("stores.yaml"),
        "---\n",
        include_str!("flux.yaml"),
        "---\n",
        include_str!("entry.yaml"),
        "---\n",
        include_str!("network.yaml"),
        "---\n",
        include_str!("access.yaml"),
        "---\n",
        include_str!("ways.yaml"),
        "---\n",
        include_str!("scans.yaml")
    );
    let slices = load::sort(&mut snapshot, yaml);
    let claims = snapshot.paged.resource_claims.iter().map(AsRef::as_ref);
    snapshot.devices = transect_ground::devices::Devices::build(claims, slices.iter());
    let kinds = Kinds::resolve(&snapshot.resources.kinds, &snapshot.plurals, &[]);
    snapshot.gitops = GitOps::build(&kinds, &snapshot.custom, |_, _| Watch::Listed)
        .with_unloaded(snapshot.resources.directory.unloaded("KindKnowledge"));
    (snapshot.ownership, snapshot.applied) = ownership(&snapshot);
    snapshot.volumes_used = signals::volumes_used(&snapshot.resources);
    snapshot.alerts = signals::alerts(&snapshot.resources);
    // Record that the declared log store was searched and found nothing.
    let source = snapshot.resources.log_source.as_ref();
    snapshot.logs.searched = Arc::new(transect_observe::logs::Searched {
        source: source.map(|source| source.name.clone()),
        ..Default::default()
    });
    snapshot.entry = traffic(&snapshot);
    snapshot.addresses = Addresses::build(
        &snapshot.services,
        &snapshot.ingresses,
        &snapshot.nodes,
        transect_network::addresses::DEFAULT_DOMAIN,
    );
    let paged = &snapshot.paged;
    let (claims, volumes) = (paged.claims.clone(), paged.volumes.clone());
    let classes = paged.storage_classes.clone();
    snapshot.derive(|on_machines| {
        Storage::placed(
            claims.iter().map(AsRef::as_ref),
            volumes.iter().map(AsRef::as_ref),
            classes.iter().map(AsRef::as_ref),
            on_machines,
        )
    });
    snapshot
}

/// The storefront's traffic figures, as the ingress controller's entry
/// queries in `resources.yaml` would return them.
fn traffic(snapshot: &Snapshot) -> transect_network::entry::poll::Reading {
    use transect_network::entry::traffic::{Figures, Measured};
    let controllers = snapshot.resources.ingress_controllers.iter();
    let measured = controllers.filter_map(|controller| {
        let figures = Figures {
            requests: 3.2,
            p95: Some(0.12),
            errors: 0.0,
        };
        Some(Measured {
            controller: controller.name.clone(),
            queries: controller.entry.clone()?,
            backends: [("acme-shop-storefront-svc-80".into(), figures)].into(),
        })
    });
    Arc::new(measured.collect())
}

pub fn model() -> Model {
    Model::build(snapshot())
}

/// The objects of kinds that no store holds, which the fixture serves
/// as already read: a `Lease` as a leader election writes it.
pub fn reads() -> transect_map::objects::reads::Reads {
    use transect_map::objects::reads::{Key, Read, Reads};
    let lease: kube::core::DynamicObject = serde_yaml_ng::from_str(
        "apiVersion: coordination.k8s.io/v1
kind: Lease
metadata: {name: storefront-leader, namespace: acme-shop, creationTimestamp: '2026-01-01T00:00:00Z'}
spec:
  holderIdentity: storefront-rs-one
  leaseDurationSeconds: 15
  acquireTime: '2026-01-01T00:00:00.000000Z'
  renewTime: '2026-01-01T12:00:00.000000Z'
  leaseTransitions: 2",
    )
    .unwrap();
    let resource = kube::core::ApiResource::erase::<k8s_openapi::api::coordination::v1::Lease>(&());
    let key = Key::of(&resource, Some("acme-shop"), "storefront-leader").unwrap();
    Reads::fixed([(key, Read::Found(Arc::new(lease)))])
}

/// Every object in the fixture whose page a test may look for.
#[cfg(test)]
pub fn objects() -> Vec<Object> {
    let snapshot = snapshot();
    let mut objects: Vec<Object> = snapshot.ownership.all_workloads().cloned().collect();
    objects.extend(snapshot.pods.iter().map(|pod| {
        let meta = &pod.metadata;
        Object::new(
            "",
            "Pod",
            meta.namespace.as_deref(),
            meta.name.as_deref().unwrap(),
        )
    }));
    let named =
        |group: &str,
         kind: &str,
         meta: &k8s_openapi::apimachinery::pkg::apis::meta::v1::ObjectMeta| {
            Object::new(
                group,
                kind,
                meta.namespace.as_deref(),
                meta.name.as_deref().unwrap(),
            )
        };
    objects.extend(
        snapshot
            .services
            .iter()
            .map(|o| named("", "Service", &o.metadata)),
    );
    objects.extend(
        snapshot
            .ingresses
            .iter()
            .map(|o| named("networking.k8s.io", "Ingress", &o.metadata)),
    );
    objects.extend(snapshot.storage.claim_objects());
    let gizmos = snapshot.custom.iter().flat_map(|(_, objects)| objects);
    objects.extend(gizmos.map(|o| named("gizmos.example.test", "Gizmo", &o.metadata)));
    let accounts = snapshot.paged.service_accounts.iter();
    objects.extend(accounts.map(|o| named("", "ServiceAccount", &o.metadata)));
    let rbac = &snapshot.paged.rbac;
    let group = "rbac.authorization.k8s.io";
    objects.extend(
        rbac.cluster_roles
            .iter()
            .map(|o| named(group, "ClusterRole", &o.metadata)),
    );
    objects.extend(rbac.roles.iter().map(|o| named(group, "Role", &o.metadata)));
    objects.extend(
        snapshot
            .nodes
            .iter()
            .map(|node| Object::new("", "Node", None, node.metadata.name.as_deref().unwrap())),
    );
    objects
}

/// Resolves every pod and workload to its top owner, as the live
/// cluster does, and finds the GitOps applier of each from its labels
/// and annotations.
fn ownership(snapshot: &Snapshot) -> (Ownership, Applied) {
    let entry = |group, kind, meta| Entry { group, kind, meta };
    let pods = snapshot.pods.iter().map(|o| entry("", "Pod", &o.metadata));
    let sets = snapshot.replica_sets.iter();
    let sets = sets.map(|o| entry("apps", "ReplicaSet", &o.metadata));
    let deployments = snapshot.deployments.iter();
    let deployments = deployments.map(|o| entry("apps", "Deployment", &o.metadata));
    let stateful = snapshot.stateful_sets.iter();
    let stateful = stateful.map(|o| entry("apps", "StatefulSet", &o.metadata));
    let daemons = snapshot.daemon_sets.iter();
    let daemons = daemons.map(|o| entry("apps", "DaemonSet", &o.metadata));
    let crons = snapshot.cron_jobs.iter();
    let crons = crons.map(|o| entry("batch", "CronJob", &o.metadata));
    let services = snapshot.services.iter();
    let services = services.map(|o| entry("", "Service", &o.metadata));
    let custom = snapshot.custom.iter().flat_map(|(resource, objects)| {
        let (group, kind) = (resource.group.as_str(), resource.kind.as_str());
        objects.iter().map(move |o| entry(group, kind, &o.metadata))
    });
    let entries: Vec<Entry> = pods
        .chain(sets)
        .chain(deployments)
        .chain(stateful)
        .chain(daemons)
        .chain(crons)
        .chain(services)
        .chain(custom)
        .collect();
    let ownership = Ownership::build(entries.iter().copied());
    let marked = entries
        .iter()
        .filter_map(|entry| Some((entry.object()?, entry.meta)));
    let applied = Applied::build(marked, &ownership, &snapshot.gitops.appliers());
    (ownership, applied)
}

#[cfg(test)]
mod tests;
