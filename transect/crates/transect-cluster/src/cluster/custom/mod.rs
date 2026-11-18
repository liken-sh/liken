//! Watches on custom resource kinds. No list of custom kinds is
//! configured. `discover_custom_kinds` collects them after the
//! cluster's first list and again after each change, so an operator
//! installed later gets a watch without a restart.
//!
//! A kind is watched whole when an owner reference on a pod, a
//! workload, or an object of a whole kind names it, when kind knowledge
//! reads a field that a trimmed object drops, or when device knowledge
//! reads it. Every other kind that a CRD defines is watched trimmed. So
//! is a cluster-scoped custom kind whose status some `ServiceAccount`
//! may write, because its controller's card lists it. A trimmed object
//! keeps its metadata, its conditions, the values of its printer
//! columns, and the fields that kind knowledge reads. So every kind
//! gets a `Ready` dot and object pages, and no spec is stored.
//!
//! Kind knowledge writes its field paths against the group's preferred
//! version, so a whole kind, and a trimmed kind that keeps fields, is
//! watched at the version that discovery prefers. Any other trimmed
//! kind is watched at its CRD's storage version, which needs no
//! discovery call and no conversion webhook. An object still stored at
//! an older version, which the CRD lists in `status.storedVersions`
//! until a migration rewrites it, does need the webhook.
//!
//! Before a kind's watch starts, a probe lists one object of it. The
//! probe runs in the background and runs again after a transient
//! failure. At most `PROBES` run at once, because the first scan finds
//! every CRD together and would otherwise open a connection to the API
//! server for each of them at the same moment. When `transect` may not
//! list a kind, the kind gets no store, and the ownership walk ends at
//! the owner reference, whose kind and name are enough to count the
//! workload. Nothing waits for these stores, so a slow or missing kind
//! never delays a page.
//!
//! When nothing refers to a kind any more, its watch stops. When a kind
//! changes from trimmed to whole, or its CRD changes its storage version
//! or printer columns, a new watch starts, and pages read the old store
//! until the new store has its first list. A kind watched whole stays
//! whole until `transect` restarts. A kind whose objects owned pods
//! once, such as one that starts Jobs, will likely own pods again, and
//! each switch between whole and trimmed costs a full list.

use std::collections::BTreeMap;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex};
use std::time::Duration;

use kube::api::ListParams;
use kube::core::{ApiResource, DynamicObject};
use kube::runtime::reflector::Store;
use kube::{Api, Client};
use tokio::sync::{Semaphore, watch};
use tokio::task::AbortHandle;

use super::Cluster;
use super::discovery::{Optional, Ready, Refusal, State};
use super::reflect::{Changes, CustomWatch, Stored, reflect_custom};
use crate::ownership::kinds_to_watch;
use transect_graph::edges::Plurals;
use transect_resources::knowledge::KindKnowledge;
use transect_resources::resources::Field;

mod named;
mod want;
use named::{controlled, described, kept};
use want::{Want, served, wants};

/// The wait after a change before the next scan for custom kinds.
/// During a rollout pods change many times a second, and a new owner
/// kind appears only when an operator is installed, so one scan per
/// `RESCAN` is enough.
const RESCAN: Duration = Duration::from_secs(10);

/// The most probes that run at once. See the module comment.
const PROBES: usize = 8;

/// A custom kind by its group and kind.
pub type GroupKind = (String, String);

/// How far one kind's watch has progressed.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Watch {
    Listed,
    /// Discovery or the first list has not finished.
    Listing,
    /// `transect` may not list the kind.
    Refused,
}

/// One custom resource object, as a whole-kind watch stores it.
pub type Custom = DynamicObject;

/// One custom kind's running watch. The watch task stops when the last
/// clone is dropped.
#[derive(Clone)]
struct Watched {
    resource: ApiResource,
    stored: Stored,
    store: Store<Custom>,
    /// Whether the first list has completed.
    listed: Arc<AtomicBool>,
    _running: Arc<Running>,
}

impl Watched {
    fn listed(&self) -> bool {
        self.listed.load(Ordering::Acquire)
    }
}

/// Aborts the watch task when dropped.
struct Running(AbortHandle);

impl Drop for Running {
    fn drop(&mut self) {
        self.0.abort();
    }
}

impl Ready for Watched {
    async fn ready(&self) {
        self.store.ready().await;
    }
}

/// The watches on custom kinds, shared by every clone of `Cluster`, and
/// the semaphore that limits probes.
#[derive(Clone)]
pub struct CustomOwners {
    kinds: Arc<Mutex<BTreeMap<GroupKind, Tracked>>>,
    probes: Arc<Semaphore>,
}

impl Default for CustomOwners {
    fn default() -> Self {
        Self {
            kinds: Arc::default(),
            probes: Arc::new(Semaphore::new(PROBES)),
        }
    }
}

/// One kind's watch, how it is watched (`want`), and the watch it
/// replaced. Pages read `previous` until the new watch has its first
/// list.
struct Tracked {
    want: Want,
    watch: Optional<Watched>,
    previous: Option<Watched>,
}

impl Tracked {
    /// The watch whose store pages read: the current watch once it has
    /// its first list, until then the previous watch, or the current one
    /// when there is no previous watch.
    fn serving(&mut self) -> Option<Watched> {
        let current = match self.watch.state() {
            State::On(watched) => Some(watched),
            _ => None,
        };
        match (current, &self.previous) {
            (Some(current), _) if current.listed() => {
                self.previous = None;
                Some(current)
            }
            (_, Some(previous)) => Some(previous.clone()),
            (current, None) => current,
        }
    }
}

impl CustomOwners {
    /// Every watched kind's resource and the objects in its store.
    pub fn snapshot(&self) -> Vec<(ApiResource, Vec<Arc<Custom>>)> {
        self.watched()
            .into_iter()
            .map(|(_, watched)| (watched.resource, watched.store.state()))
            .collect()
    }

    /// The plural name of every watched kind, by group and kind.
    pub fn plurals(&self) -> Plurals {
        self.watched()
            .into_iter()
            .map(|(group_kind, watched)| (group_kind, watched.resource.plural))
            .collect()
    }

    /// The objects of every kind watched whole.
    pub fn whole(&self) -> Vec<Vec<Arc<Custom>>> {
        let whole = self.watched().into_iter();
        let whole = whole.filter(|(_, watched)| watched.stored == Stored::Whole);
        whole.map(|(_, watched)| watched.store.state()).collect()
    }

    /// The kinds watched whole, by group and kind.
    pub fn whole_kinds(&self) -> std::collections::HashSet<GroupKind> {
        let whole = self.watched().into_iter();
        let whole = whole.filter(|(_, watched)| watched.stored == Stored::Whole);
        whole.map(|(group_kind, _)| group_kind).collect()
    }

    /// The progress of each kind's watch, by group and kind.
    pub fn watched_kinds(&self) -> std::collections::HashMap<GroupKind, Watch> {
        let mut kinds = self.kinds.lock().unwrap();
        kinds
            .iter_mut()
            .map(|(group_kind, tracked)| {
                let listed = tracked.serving().is_some_and(|watched| watched.listed());
                let watched = match (listed, tracked.watch.state()) {
                    (true, _) => Watch::Listed,
                    (false, State::Off(_)) => Watch::Refused,
                    (false, _) => Watch::Listing,
                };
                (group_kind.clone(), watched)
            })
            .collect()
    }

    /// Each kind with the watch that pages read.
    fn watched(&self) -> Vec<(GroupKind, Watched)> {
        let mut kinds = self.kinds.lock().unwrap();
        kinds
            .iter_mut()
            .filter_map(|(group_kind, tracked)| Some((group_kind.clone(), tracked.serving()?)))
            .collect()
    }

    /// Makes the watches match `found`: starts a watch with `start` for
    /// each new kind, stops the watch of each kind missing from `found`,
    /// and replaces the watch of each kind whose `Want` changed. A kind
    /// watched whole is never changed to trimmed. When a probe is still
    /// running for a kind that was removed, its watch is dropped, and so
    /// stops, when the probe finishes.
    fn track(
        &self,
        found: BTreeMap<GroupKind, Want>,
        start: impl Fn(&GroupKind, Want) -> Optional<Watched>,
        changed: &watch::Sender<()>,
    ) {
        let mut kinds = self.kinds.lock().unwrap();
        let stopped = kinds
            .keys()
            .filter(|kind| !found.contains_key(*kind))
            .count();
        kinds.retain(|group_kind, _| found.contains_key(group_kind));
        for (group_kind, want) in found {
            let Some(tracked) = kinds.get_mut(&group_kind) else {
                let watch = start(&group_kind, want.clone());
                let previous = None;
                kinds.insert(
                    group_kind,
                    Tracked {
                        want,
                        watch,
                        previous,
                    },
                );
                continue;
            };
            let want = match (tracked.want.stored, want.stored) {
                (Stored::Whole, Stored::Trimmed) => Want::whole(tracked.want.resource.clone()),
                _ => want,
            };
            if tracked.want == want {
                continue;
            }
            let previous = tracked.serving();
            let watch = start(&group_kind, want.clone());
            *tracked = Tracked {
                want,
                watch,
                previous,
            };
        }
        if stopped > 0 {
            tracing::info!(
                stopped,
                "Stopped watching custom kinds that no object references"
            );
            changed.send_replace(());
        }
    }
}

/// Starts the task that keeps a watch on every custom kind that needs
/// one. The task scans again after each change of the cluster, which
/// includes a reload of the resources. Call it after the cluster has
/// synced, so the first scan reads every owner reference.
pub fn discover_custom_kinds(
    cluster: Cluster,
    client: Client,
    knowledge: Field<Vec<KindKnowledge>>,
    device_kinds: Field<Vec<GroupKind>>,
) {
    tokio::spawn(async move {
        let mut changes = cluster.changed.subscribe();
        loop {
            let owners = kinds_to_watch(&cluster.owner_references()).into_keys();
            let plurals = cluster.plurals();
            let knowledge = knowledge.read();
            let known = described(&knowledge, &device_kinds.read(), &plurals);
            let fields = kept(&knowledge, &plurals);
            let controlled = controlled(&cluster.grants(), &plurals, &cluster.cluster_scoped());
            let served = served(&cluster.definitions.state());
            let preferred = cluster.preferred();
            let found = wants(
                owners.chain(known),
                &served,
                controlled,
                &fields,
                &preferred,
            );
            let probes = &cluster.custom.probes;
            let start = |group_kind: &GroupKind, want: Want| {
                discover(&client, group_kind, want, probes, &cluster.changes())
            };
            cluster.custom.track(found, start, &cluster.changed);
            if changes.changed().await.is_err() {
                return;
            }
            tokio::time::sleep(RESCAN).await;
            changes.borrow_and_update();
        }
    });
}

fn discover(
    client: &Client,
    group_kind: &GroupKind,
    want: Want,
    probes: &Arc<Semaphore>,
    changes: &Changes,
) -> Optional<Watched> {
    let probes = probes.clone();
    let (probing, starting) = (client.clone(), client.clone());
    let started = changes.clone();
    let (group, kind) = group_kind.clone();
    let Want {
        stored,
        resource,
        columns,
        fields,
    } = want;
    Optional::discover(
        format!("{kind} in {group}"),
        &changes.sender,
        move || {
            let (client, resource) = (probing.clone(), resource.clone());
            let (group, kind) = (group.clone(), kind.clone());
            let probes = probes.clone();
            async move {
                // The semaphore is never closed, so `acquire_owned`
                // only waits.
                let _permit = probes.acquire_owned().await.ok();
                let resource = match resource {
                    Some(resource) => resource,
                    None => resolve(&client, &group, &kind).await?,
                };
                listable(client, resource).await
            }
        },
        move |resource: ApiResource| {
            let CustomWatch {
                store,
                task,
                listed,
            } = reflect_custom(
                starting,
                resource.clone(),
                started,
                stored,
                (columns, fields),
            );
            Watched {
                resource,
                stored,
                store,
                listed,
                _running: Arc::new(Running(task)),
            }
        },
    )
}

/// The kind's resource at its group's preferred version, from
/// discovery.
async fn resolve(client: &Client, group: &str, kind: &str) -> Result<ApiResource, Refusal> {
    let found = kube::discovery::group(client, group).await;
    let found = found.map(|served| served.recommended_kind(kind));
    match found {
        Ok(Some((resource, _capabilities))) => Ok(resource),
        Ok(None) => {
            tracing::warn!(%group, %kind, "The API group does not serve this kind, so the ownership walk stops at an owner of this kind");
            Err(Refusal::NotServed)
        }
        Err(error) => {
            tracing::warn!(%group, %kind, %error, "Discovery of this kind returned an error, so the ownership walk stops at an owner of this kind");
            Err(error.into())
        }
    }
}

/// `resource`, after a list of one object of it succeeds.
async fn listable(client: Client, resource: ApiResource) -> Result<ApiResource, Refusal> {
    let api: Api<DynamicObject> = Api::all_with(client, &resource);
    let one = ListParams::default().limit(1);
    api.list_metadata(&one).await.inspect_err(|error| {
        let (group, kind) = (&resource.group, &resource.kind);
        tracing::warn!(%group, %kind, %error, "Listing this kind returned an error, so the ownership walk stops at an owner of this kind");
    })?;
    Ok(resource)
}

#[cfg(test)]
mod tests;
