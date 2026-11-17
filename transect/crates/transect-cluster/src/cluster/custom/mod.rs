//! Watches on custom kinds: whole on the kinds that own pods and
//! workloads and those whose kind knowledge reads their fields, and
//! trimmed on every other CRD the cluster serves.
//!
//! No list of custom kinds is configured.  `transect` finds the kinds it
//! reads whole in the owner references it already holds, once the
//! cluster's first list is in, and again whenever the cluster changes,
//! so a new operator's kind gets a watch without a restart.  A kind
//! whose knowledge reads its fields, such as a reference to another
//! object, is watched whole too, even when it owns nothing.  Each of
//! these is resolved by discovery, in its group's preferred version,
//! because its knowledge's paths are written against that version.
//!
//! Every other CRD is watched trimmed, on its storage version, which
//! the CRD store already holds, so no discovery call is made per kind.
//! An object stored at that version needs no conversion webhook to be
//! read.  An object still stored at an older version, which the CRD's
//! `status.storedVersions` lists until a migration rewrites it, does.
//! A trimmed object keeps its metadata, its conditions, and its printer
//! columns' values, so every kind has a `Ready` dot and its objects'
//! pages, and no spec reaches a store.  A custom kind outside any CRD
//! whose status some `ServiceAccount` may write is watched trimmed too,
//! after discovery, because its controller's card lists it.
//!
//! Each kind is tried with a list of one object before its watch
//! starts, in the background and again after a transient failure, at
//! most `PROBES` at once.  The first scan finds every CRD at once, and
//! a burst of a hundred probes or more opens as many connections, some
//! of which the API server has refused.  A kind that `transect` may not
//! list gets no store, and the ownership walk ends at its owner
//! reference, which names the kind and the object well enough to count
//! it.  Nothing waits for these stores, so a slow or missing kind never
//! holds up a page.
//!
//! A kind that nothing names any more, because its operator was removed
//! or its knowledge was, has its watch stopped.  A kind whose need
//! changes from trimmed to whole, or whose CRD changes its storage
//! version or its printer columns, is watched again.  The pages read its
//! previous store until the next one holds its first list, so its
//! objects never leave the pages while it is listed again.  A kind
//! watched whole stays whole until `transect` restarts, because a kind
//! whose objects owned pods once, such as one that starts Jobs, will
//! likely own them again, and each change back and forth would cost a
//! full list.

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
use super::reflect::{Changes, CustomWatch, Holding, reflect_custom};
use crate::ownership::kinds_to_watch;
use transect_graph::edges::Plurals;
use transect_resources::knowledge::KindKnowledge;
use transect_resources::resources::Field;

mod named;
mod want;
use named::{controlled, described, kept};
use want::{Want, served, wants};

/// How long after a change `transect` looks for new owner kinds.  Pods
/// change many times a second during a rollout; a new owner kind
/// appears when an operator is installed, which can wait this long.
const RESCAN: Duration = Duration::from_secs(10);

/// The most probes of custom kinds that run at once.
const PROBES: usize = 8;

/// A custom kind by its group and kind.
pub type GroupKind = (String, String);

/// How far the watch on one kind has come.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Watch {
    Listed,
    /// Still finding the kind, or waiting for its first list.
    Listing,
    /// `transect` may not list it.
    Refused,
}

/// One custom resource, whole, as its watch holds it.
pub type Custom = DynamicObject;

/// One custom kind's watch: the resource discovery resolved, what it
/// holds of each object, and the objects its store holds.  The watch
/// stops once the last clone is dropped.
#[derive(Clone)]
struct Watched {
    resource: ApiResource,
    holding: Holding,
    store: Store<Custom>,
    /// Whether its first list is in.
    listed: Arc<AtomicBool>,
    _running: Arc<Running>,
}

impl Watched {
    fn listed(&self) -> bool {
        self.listed.load(Ordering::Acquire)
    }
}

/// A running watch, which stops when this is dropped.
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

/// The watches running, shared by the cluster's clones, each with what
/// it holds, and the probes that may run.
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

/// One kind's watch, or its discovery, what it was asked for, and the
/// watch it replaced, which the pages read until the new one has
/// listed.
struct Tracked {
    want: Want,
    watch: Optional<Watched>,
    previous: Option<Watched>,
}

impl Tracked {
    /// The watch whose store the pages read now: the kind's watch once
    /// it has listed, and until then the one it replaced, or the kind's
    /// watch when it replaced none.
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
    /// Every watched kind's resource and the objects its store holds.
    pub fn snapshot(&self) -> Vec<(ApiResource, Vec<Arc<Custom>>)> {
        self.watched()
            .into_iter()
            .map(|(_, watched)| (watched.resource, watched.store.state()))
            .collect()
    }

    /// The plural of every watched kind, by group and kind.
    pub fn plurals(&self) -> Plurals {
        self.watched()
            .into_iter()
            .map(|(group_kind, watched)| (group_kind, watched.resource.plural))
            .collect()
    }

    /// The objects of every kind watched whole, whose owners are watched
    /// whole in turn.
    pub fn whole(&self) -> Vec<Vec<Arc<Custom>>> {
        let whole = self.watched().into_iter();
        let whole = whole.filter(|(_, watched)| watched.holding == Holding::Whole);
        whole.map(|(_, watched)| watched.store.state()).collect()
    }

    /// The kinds watched whole, by group and kind.
    pub fn whole_kinds(&self) -> std::collections::HashSet<GroupKind> {
        let whole = self.watched().into_iter();
        let whole = whole.filter(|(_, watched)| watched.holding == Holding::Whole);
        whole.map(|(group_kind, _)| group_kind).collect()
    }

    /// How far the watch on each kind has come, by group and kind.
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

    /// The watch that serves each kind now.
    fn watched(&self) -> Vec<(GroupKind, Watched)> {
        let mut kinds = self.kinds.lock().unwrap();
        kinds
            .iter_mut()
            .filter_map(|(group_kind, tracked)| Some((group_kind.clone(), tracked.serving()?)))
            .collect()
    }

    /// Watches exactly the kinds in `found`, each holding what its
    /// `Want` gives, with `start`: starts each new one, stops each one
    /// `found` leaves out, and starts again each one whose want changed,
    /// keeping its old watch until the new one has listed.  A kind
    /// watched whole is never asked to be trimmed again.  A discovery
    /// still probing when its kind is dropped sends its watch into a
    /// channel that nothing reads, so the watch is dropped and stops.
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
            let want = match (tracked.want.holding, want.holding) {
                (Holding::Whole, Holding::Trimmed) => Want::whole(tracked.want.resource.clone()),
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
            tracing::info!(stopped, "stopped watching custom kinds nothing names");
            changed.send_replace(());
        }
    }
}

/// Starts a watch on each custom owner kind `cluster` names now, and on
/// each kind `knowledge` describes, and on each kind device knowledge
/// reads whole, `device_kinds`, then on each new one after a change.
/// A reload of the resources is a change too.  The caller starts it
/// once the cluster has synced, so the first scan reads every owner
/// reference.
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
        holding,
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
                // The semaphore is never closed, so this always waits
                // for a permit.
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
                holding,
                (columns, fields),
            );
            Watched {
                resource,
                holding,
                store,
                listed,
                _running: Arc::new(Running(task)),
            }
        },
    )
}

/// The kind's resource in its group's preferred version, once discovery
/// resolves it.  A kind it cannot resolve ends the ownership walk at its
/// owners.
async fn resolve(client: &Client, group: &str, kind: &str) -> Result<ApiResource, Refusal> {
    let found = kube::discovery::group(client, group).await;
    let found = found.map(|served| served.recommended_kind(kind));
    match found {
        Ok(Some((resource, _capabilities))) => Ok(resource),
        Ok(None) => {
            tracing::warn!(%group, %kind, "the API group does not serve this kind; the ownership walk stops at an owner of this kind");
            Err(Refusal::NotServed)
        }
        Err(error) => {
            tracing::warn!(%group, %kind, %error, "cannot resolve this kind; the ownership walk stops at an owner of this kind");
            Err(error.into())
        }
    }
}

/// `resource`, once a list of one of it succeeds.  A kind `transect` may
/// not list ends the ownership walk at its owners.
async fn listable(client: Client, resource: ApiResource) -> Result<ApiResource, Refusal> {
    let api: Api<DynamicObject> = Api::all_with(client, &resource);
    let one = ListParams::default().limit(1);
    api.list_metadata(&one).await.inspect_err(|error| {
        let (group, kind) = (&resource.group, &resource.kind);
        tracing::warn!(%group, %kind, %error, "cannot list this kind; the ownership walk stops at an owner of this kind");
    })?;
    Ok(resource)
}

#[cfg(test)]
mod tests;
