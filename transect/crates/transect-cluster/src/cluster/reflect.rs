//! One reflector per kind: a watch on the API that keeps an in-memory
//! store of every object of that kind current, trimmed to what `transect`
//! reads, and marks the cluster changed when an event changes what the
//! store holds.

use std::fmt::Debug;
use std::hash::Hash;
use std::sync::Arc;
use std::sync::atomic::{AtomicBool, Ordering};

use futures::{Stream, StreamExt};
use kube::core::{ApiResource, DynamicObject};
use kube::runtime::reflector::{self, Store};
use kube::runtime::{WatchStreamExt, watcher};
use kube::{Api, Client, Resource};
use serde::Serialize;
use serde::de::DeserializeOwned;
use tokio::sync::watch;
use tokio::task::AbortHandle;

use super::change::{Ignored, differs};
use super::trim;
use transect_resources::knowledge::Column;

/// The sender on which a watch reports that its store's contents
/// changed, and the keys it leaves out when it decides whether they did.
#[derive(Clone)]
pub struct Changes {
    pub sender: watch::Sender<()>,
    pub ignored: Ignored,
}

/// Starts watching every object `api` lists and returns the store the
/// watch fills.  The watch restarts with backoff after an error, so a
/// dropped connection to the API server leaves the store stale until it
/// reconnects instead of ending the watch.
pub fn reflect<K>(api: Api<K>, changes: Changes) -> Store<K>
where
    K: Resource + Clone + DeserializeOwned + Serialize + Debug + Send + Sync + 'static,
    K::DynamicType: Default + Eq + Hash + Clone + Send + Sync,
{
    reflect_cut_with(api, K::DynamicType::default(), changes, |object| {
        trim::meta(object.meta_mut())
    })
}

/// `reflect` for a built-in kind whose objects `trim` cuts to the
/// fields `transect` reads.
pub fn reflect_cut<K>(api: Api<K>, changes: Changes, trim: fn(&mut K)) -> Store<K>
where
    K: Resource + Clone + DeserializeOwned + Serialize + Debug + Send + Sync + 'static,
    K::DynamicType: Default + Eq + Hash + Clone + Send + Sync,
{
    reflect_cut_with(api, K::DynamicType::default(), changes, trim)
}

/// What a custom kind's watch holds of each object.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Holding {
    /// The whole object, for a kind whose fields `transect` reads.
    Whole,
    /// Its metadata, its conditions, and its printer columns' values,
    /// for every other kind.
    Trimmed,
}

/// A custom kind's watch: the store it fills, its task, which stops
/// the watch when aborted, and whether its first list is in.
pub struct CustomWatch {
    pub store: Store<DynamicObject>,
    pub task: AbortHandle,
    pub listed: Arc<AtomicBool>,
}

/// `reflect_cut_with` for a custom kind, whose objects keep the names of
/// the managers that write their status, which controller attribution
/// reads.  Everything else in their managed fields is dropped.  A
/// trimmed kind's objects keep only their metadata, their conditions,
/// the values of `kept`'s printer columns, and `kept`'s fields, the
/// paths its kind knowledge reads.  The watch runs until its task is
/// aborted, so the watch on a kind that nothing needs any more can be
/// stopped.
pub fn reflect_custom(
    client: Client,
    resource: ApiResource,
    changes: Changes,
    holding: Holding,
    kept: (Vec<Column>, Vec<String>),
) -> CustomWatch {
    let kind = (resource.group.clone(), resource.kind.clone());
    let writer = reflector::store::Writer::new(resource.clone());
    let store = writer.as_reader();
    let api: Api<DynamicObject> = Api::all_with(client, &resource);
    let listed = Arc::new(AtomicBool::new(false));
    let events = watcher(api, watcher::Config::default()).default_backoff();
    let task = match holding {
        Holding::Whole => {
            let events = events.modify(trim::whole_custom);
            forward(kind, resource, writer, events, changes, listed.clone())
        }
        Holding::Trimmed => {
            let (columns, fields) = kept;
            let events = events
                .modify(move |object| trim::conditions_and_columns(object, &columns, &fields));
            forward(kind, resource, writer, events, changes, listed.clone())
        }
    };
    CustomWatch {
        store,
        task,
        listed,
    }
}

/// `reflect_cut` for a kind whose type is `dyntype`.
fn reflect_cut_with<K>(
    api: Api<K>,
    dyntype: K::DynamicType,
    changes: Changes,
    trim: fn(&mut K),
) -> Store<K>
where
    K: Resource + Clone + DeserializeOwned + Serialize + Debug + Send + Sync + 'static,
    K::DynamicType: Eq + Hash + Clone + Send + Sync,
{
    let kind = (
        K::group(&dyntype).into_owned(),
        K::kind(&dyntype).into_owned(),
    );
    let writer = reflector::store::Writer::new(dyntype.clone());
    let store = writer.as_reader();
    let events = watcher(api, watcher::Config::default())
        .default_backoff()
        .modify(trim);
    // A built-in kind's watch runs for as long as `transect` does.
    let listed = Arc::new(AtomicBool::new(false));
    let _running = forward(kind, dyntype, writer, events, changes, listed);
    store
}

/// Runs `events`, already trimmed, into `writer`, marks `changes` after
/// an event that changes what the store holds but for the keys it
/// ignores on `kind`, and sets `listed` once the first list is in.  The
/// writer applies each event before the change is marked, so a reader
/// woken by it finds the store already holding the change.
fn forward<K>(
    kind: (String, String),
    dyntype: K::DynamicType,
    mut writer: reflector::store::Writer<K>,
    events: impl Stream<Item = watcher::Result<watcher::Event<K>>> + Send + 'static,
    changes: Changes,
    listed: Arc<AtomicBool>,
) -> AbortHandle
where
    K: Resource + Clone + Serialize + Send + Sync + 'static,
    K::DynamicType: Eq + Hash + Clone + Send + Sync,
{
    let store = writer.as_reader();
    let task = tokio::spawn(async move {
        let mut events = std::pin::pin!(events);
        let mut relist = Vec::new();
        while let Some(event) = events.next().await {
            match event {
                Ok(event) => {
                    // The first list always marks a change, because a
                    // page draws a kind that has not listed yet
                    // differently.
                    let done = matches!(event, watcher::Event::InitDone);
                    let first = done && !listed.load(Ordering::Acquire);
                    let ignored = changes.ignored.keys(&kind.0, &kind.1);
                    let differs = differs(&store, &dyntype, &event, &mut relist, &ignored);
                    let signalled = differs || first;
                    writer.apply_watcher_event(&event);
                    if done {
                        listed.store(true, Ordering::Release);
                        tracing::debug!(group = %kind.0, kind = %kind.1, objects = store.len(), "listed");
                    }
                    tracing::trace!(group = %kind.0, kind = %kind.1, signalled, "watch event");
                    // send_replace, unlike send, succeeds while no one
                    // is subscribed yet.
                    if signalled {
                        changes.sender.send_replace(());
                    }
                }
                Err(error) => {
                    tracing::warn!(group = %kind.0, kind = %kind.1, %error, "watch failed; retrying")
                }
            }
        }
    });
    task.abort_handle()
}
