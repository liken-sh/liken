//! One watch per kind. Each watch fills an in-memory store with every
//! object of the kind, trimmed to the fields `transect` reads, and
//! signals when an event changes the store's contents.

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

/// The sender that a watch signals when its store's contents change,
/// and the keys that it ignores when it compares them.
#[derive(Clone)]
pub struct Changes {
    pub sender: watch::Sender<()>,
    pub ignored: Ignored,
}

/// Starts a watch on every object of `api` and returns the store that
/// it fills, with each object trimmed by `trim::meta`. After an error
/// the watch restarts with backoff, so when the connection to the API
/// server drops, the store is stale until the watch reconnects.
pub fn reflect<K>(api: Api<K>, changes: Changes) -> Store<K>
where
    K: Resource + Clone + DeserializeOwned + Serialize + Debug + Send + Sync + 'static,
    K::DynamicType: Default + Eq + Hash + Clone + Send + Sync,
{
    reflect_cut_with(api, K::DynamicType::default(), changes, |object| {
        trim::meta(object.meta_mut())
    })
}

/// Like `reflect`, but `trim` cuts each object down to the fields
/// `transect` reads, in place of `trim::meta`.
pub fn reflect_cut<K>(api: Api<K>, changes: Changes, trim: fn(&mut K)) -> Store<K>
where
    K: Resource + Clone + DeserializeOwned + Serialize + Debug + Send + Sync + 'static,
    K::DynamicType: Default + Eq + Hash + Clone + Send + Sync,
{
    reflect_cut_with(api, K::DynamicType::default(), changes, trim)
}

/// How much of each object the store of a custom kind keeps.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Stored {
    /// Everything that `trim::whole_custom` keeps, for a kind whose
    /// fields `transect` reads.
    Whole,
    /// The metadata, the conditions, the values of the printer columns,
    /// and the fields that kind knowledge reads, for every other kind.
    Trimmed,
}

/// The watch of a custom kind. Aborting `task` stops the watch, and
/// `listed` is set when the first list is complete.
pub struct CustomWatch {
    pub store: Store<DynamicObject>,
    pub task: AbortHandle,
    pub listed: Arc<AtomicBool>,
}

/// Starts a watch for the custom kind `resource`. Each object keeps the
/// names of the field managers of its `status`, which controller
/// attribution reads, and drops the rest of its managed fields. With
/// `Stored::Trimmed`, `kept` gives the printer columns and the field
/// paths to keep. The watch runs until its task is aborted, so the
/// watch of a kind that no page needs any more can be stopped.
pub fn reflect_custom(
    client: Client,
    resource: ApiResource,
    changes: Changes,
    stored: Stored,
    kept: (Vec<Column>, Vec<String>),
) -> CustomWatch {
    let kind = (resource.group.clone(), resource.kind.clone());
    let writer = reflector::store::Writer::new(resource.clone());
    let store = writer.as_reader();
    let api: Api<DynamicObject> = Api::all_with(client, &resource);
    let listed = Arc::new(AtomicBool::new(false));
    let events = watcher(api, watcher::Config::default()).default_backoff();
    let task = match stored {
        Stored::Whole => {
            let events = events.modify(trim::whole_custom);
            forward(kind, resource, writer, events, changes, listed.clone())
        }
        Stored::Trimmed => {
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
    // The handle is dropped without aborting, so the watch runs until
    // `transect` exits.
    let listed = Arc::new(AtomicBool::new(false));
    let _running = forward(kind, dyntype, writer, events, changes, listed);
    store
}

/// Applies each trimmed event to the store of `writer`, and signals
/// `changes.sender` when the event changes the store, apart from the
/// keys that `changes.ignored` lists for `kind`. The event is applied
/// before the signal is sent, so a reader that the signal wakes finds
/// the change in the store.
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
                    // The first completed list signals even when the
                    // store did not change, because a page draws a kind
                    // that has not been listed yet differently.
                    let done = matches!(event, watcher::Event::InitDone);
                    let first = done && !listed.load(Ordering::Acquire);
                    let ignored = changes.ignored.keys(&kind.0, &kind.1);
                    let differs = differs(&store, &dyntype, &event, &mut relist, &ignored);
                    let signalled = differs || first;
                    writer.apply_watcher_event(&event);
                    if done {
                        listed.store(true, Ordering::Release);
                        tracing::debug!(group = %kind.0, kind = %kind.1, objects = store.len(), "Listed the objects of the kind");
                    }
                    tracing::trace!(group = %kind.0, kind = %kind.1, signalled, "Received a watch event");
                    // `send_replace` stores the value when no receiver
                    // exists yet, where `send` would fail.
                    if signalled {
                        changes.sender.send_replace(());
                    }
                }
                Err(error) => {
                    tracing::warn!(group = %kind.0, kind = %kind.1, %error, "The watch of the kind returned an error and will be retried")
                }
            }
        }
    });
    task.abort_handle()
}
