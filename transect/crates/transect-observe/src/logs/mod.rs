//! This module reads the last log lines that the pod page shows.  When
//! the cluster declares a log source, transect reads them from it, and
//! otherwise from the Kubernetes API's `pods/log`, which needs nothing
//! but the API.  When the source returns no lines, because of an error
//! or because it has none for the pod, the page shows the API's tail in
//! their place and states why.
//!
//! A page load reads the tails and keeps them in a cache for `FRESH`.
//! A pushed render reads only the cache, because the pod page renders
//! again on every change in the cluster, and a read for each push and
//! each container would turn one open tab into a steady load on the API
//! server or the store.
//!
//! Health counts error lines in one of two ways.  When the log source
//! maps a level field, `search` counts them across every pod over the
//! last hour while it searches each pattern.  When it maps none, or
//! while that search fails, health counts the error lines in the reads
//! that the cache holds, so the count covers only the pods whose pages
//! were opened.  This module does not tail every pod through the API,
//! because that would read every log in the cluster on every change.

mod api;
pub mod evidence;
mod level;
mod levels;
mod logsql;
pub mod machines;
pub mod patterns;
mod query;
mod round;
pub mod sample;
pub mod search;
pub mod source;
mod store;
mod tags;
mod tail;

use std::collections::HashMap;
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

use futures::FutureExt;
use futures::future::{BoxFuture, Shared};
use k8s_openapi::api::core::v1::Pod;
use k8s_openapi::jiff::Timestamp;
use kube::Client;
use tokio::sync::watch;

pub use api::containers;
use patterns::LogPattern;
pub use search::Searched;
pub use source::LogSource;
use source::Reached;
use store::Store;
pub use tail::{Line, Matching, Origin, Read, TAIL_LINES, Tail, Tails};
use transect_resources::resources::Field;
#[cfg(any(test, feature = "fixtures"))]
pub use {query::Row, tail::from_rows};

/// How long the page loads and pushes after a read use that read
/// instead of reading again.
const FRESH: Duration = Duration::from_secs(30);

/// How long a read stays in the cache at all.  Pushes to a page that
/// stays open keep their lines, and the cache drops the reads of pods
/// whose pages closed long ago.
const KEEP: Duration = Duration::from_secs(600);

/// How long one container's read may take before the page renders
/// without its lines.
const READ_TIMEOUT: Duration = Duration::from_secs(5);

/// The text the aside shows when transect may not read `pods/log`.
pub const FORBIDDEN: &str =
    "transect may not read this pod's logs. Granting it get on pods/log shows them here.";

/// The text the page shows for a container with no lines.
pub const EMPTY: &str = "nothing logged yet";

/// The note under the heading.  It states how many lines of `container`
/// the page shows, or the most lines it shows of each container when
/// `container` is `None`, where the lines came from, and when transect
/// read them.
pub fn note(tails: &Tails, container: Option<&str>) -> String {
    let at = tails.read_at.strftime("%H:%M:%S");
    let from = match &tails.from {
        Origin::Store(name) => format!("the LogSource {name}"),
        Origin::Api | Origin::Fallback { .. } => "the Kubernetes API".into(),
    };
    let Some(container) = container else {
        return format!(
            "Each container: up to its last {} lines, from {from}, read at {at} UTC",
            tail::TAIL_LINES
        );
    };
    let shown = tails
        .containers
        .iter()
        .find(|tail| tail.container == container);
    let count = match shown.map(|tail| &tail.read) {
        Some(tail::Read::Lines(lines)) => lines.len(),
        _ => 0,
    };
    let lines = match count {
        1 => "its last line".to_string(),
        count => format!("its last {count} lines"),
    };
    format!("Container {container}: {lines}, from {from}, read at {at} UTC")
}

/// The reason the page shows the API's lines when a log source is
/// declared, or `None` when it shows the source's lines or no source
/// is declared.
pub fn fallback(tails: &Tails) -> Option<String> {
    match &tails.from {
        Origin::Fallback { source, why } => Some(format!(
            "The LogSource {source} {why}, so these are the API's lines, which keep only each container's current run."
        )),
        Origin::Api | Origin::Store(_) => None,
    }
}

/// The declared log source, and the settings transect uses to connect
/// to it from the place where transect runs.
#[derive(Clone)]
pub struct Sourcing {
    /// The `LogSource` that the resources declare.  There is at most
    /// one.
    pub source: Field<Option<LogSource>>,
    pub patterns: Field<Vec<LogPattern>>,
    /// `--log-source-url`, which transect uses in place of the
    /// resource's own address.
    pub url: Option<String>,
    /// The cluster's DNS domain, which a `Service`'s name ends in.
    pub domain: String,
}

impl Sourcing {
    /// The log source as it is declared now, with its endpoint, or
    /// `None` when the cluster declares none.
    pub fn reached(&self) -> Option<Reached> {
        let source = self.source.read()?;
        Some(source.reached(self.url.as_deref(), &self.domain))
    }
}

/// The log data that health and the findings read: the cached reads and
/// the last round of searches.
#[derive(Clone, Debug, Default)]
pub struct Counted {
    pub logged: Vec<Logged>,
    pub searched: Arc<Searched>,
}

/// Starts the searches across the cluster in the background, and
/// returns a receiver for each round's results.
pub fn search(
    sourcing: Sourcing,
    changed: watch::Sender<()>,
) -> anyhow::Result<watch::Receiver<Arc<Searched>>> {
    Ok(search::watch(Store::new()?, sourcing, changed))
}

/// A pod, by namespace and name.
type Key = (String, String);

/// Each pod's last read, with the time it was made.
type Reads = HashMap<Key, (Instant, Arc<Tails>)>;

/// A read in progress.  Every page load that waits on it shares the
/// same future.
type Reading = Shared<BoxFuture<'static, ()>>;

/// The counts from one cached read of a pod's logs.
#[derive(Clone, Debug, PartialEq)]
pub struct Logged {
    pub namespace: String,
    pub pod: String,
    pub read_at: Timestamp,
    /// The lines read, across the pod's containers.
    pub lines: usize,
    /// How many of them are errors.
    pub errors: usize,
}

/// The cached tails of each pod a page has loaded, by namespace and
/// name.
#[derive(Clone)]
pub struct LogTails {
    client: Client,
    store: Store,
    sourcing: Sourcing,
    read: Arc<Mutex<Reads>>,
    /// The reads in progress.  A page load of the same pod waits on one
    /// of these instead of starting its own.
    reading: Arc<Mutex<HashMap<Key, Reading>>>,
    /// Sent after each read, so health counts the read's errors without
    /// waiting for a change in the cluster.
    changed: watch::Sender<()>,
}

impl LogTails {
    pub fn new(
        client: Client,
        sourcing: Sourcing,
        changed: watch::Sender<()>,
    ) -> anyhow::Result<Self> {
        Ok(Self {
            client,
            store: Store::new()?,
            sourcing,
            read: Arc::default(),
            reading: Arc::default(),
            changed,
        })
    }

    /// Tails that connect to no log store and no API server, for the
    /// fixture cluster, whose DNS domain is `domain`.  Each read returns
    /// that nothing could be read.
    #[cfg(any(test, debug_assertions))]
    pub fn offline(changed: watch::Sender<()>, domain: &str) -> anyhow::Result<Self> {
        let nothing = tower::service_fn(|_: http::Request<kube::client::Body>| async {
            let error: tower::BoxError = "the fixture cluster has no API server".into();
            Err::<http::Response<kube::client::Body>, _>(error)
        });
        let sourcing = Sourcing {
            source: Field::fixed(None),
            patterns: Field::fixed(Vec::new()),
            url: None,
            domain: domain.into(),
        };
        Self::new(Client::new(nothing, "default"), sourcing, changed)
    }

    /// The counts from each read that is still in the cache.
    pub fn logged(&self) -> Vec<Logged> {
        let read = self.read.lock().unwrap_or_else(|e| e.into_inner());
        let kept = read.iter().filter(|(_, (at, _))| at.elapsed() < KEEP);
        kept.map(|((namespace, pod), (_, tails))| {
            let (errors, lines) = tails.errors();
            Logged {
                namespace: namespace.clone(),
                pod: pod.clone(),
                read_at: tails.read_at,
                lines,
                errors,
            }
        })
        .collect()
    }

    /// The tails last read for `namespace/name`, for any render.
    pub fn cached(&self, namespace: &str, name: &str) -> Option<Arc<Tails>> {
        let read = self.read.lock().unwrap_or_else(|e| e.into_inner());
        let key = (namespace.to_owned(), name.to_owned());
        read.get(&key).map(|(_, tails)| tails.clone())
    }

    /// Reads `pod`'s tails unless a read in the last `FRESH` already
    /// returned them.  A page load calls this before it renders, and
    /// waits for the read no longer than `READ_TIMEOUT`.  The read
    /// itself may take longer, because when the source returns no
    /// tails, transect then requests the API's.  A read that takes
    /// longer than the wait continues, and when it finishes, the change
    /// it sends pushes its lines to the page.
    pub async fn refresh(&self, pod: &Pod) {
        let (Some(namespace), Some(name)) = (&pod.metadata.namespace, &pod.metadata.name) else {
            return;
        };
        let key = (namespace.clone(), name.clone());
        let reading = {
            let mut reading = self.reading.lock().unwrap_or_else(|e| e.into_inner());
            if self.fresh(&key) {
                return;
            }
            let since = pod.metadata.creation_timestamp.as_ref().map(|t| t.0);
            let entry = reading.entry(key.clone());
            entry
                .or_insert_with(|| self.start(key, since, containers(pod)))
                .clone()
        };
        let _ = tokio::time::timeout(READ_TIMEOUT, reading).await;
    }

    fn fresh(&self, key: &Key) -> bool {
        let read = self.read.lock().unwrap_or_else(|e| e.into_inner());
        read.get(key).is_some_and(|(at, _)| at.elapsed() < FRESH)
    }

    /// Reads the tails of the pod that `key` names on a task of its own,
    /// so the read finishes and fills the cache whether or not any page
    /// still waits for it.
    fn start(&self, key: Key, since: Option<Timestamp>, containers: Vec<String>) -> Reading {
        let tails = self.clone();
        let task = tokio::spawn(async move {
            let (namespace, name) = &key;
            let read = tails.read(namespace, name, since, containers).await;
            {
                let mut cached = tails.read.lock().unwrap_or_else(|e| e.into_inner());
                cached.retain(|_, (at, _)| at.elapsed() < KEEP);
                cached.insert(key.clone(), (Instant::now(), Arc::new(read)));
            }
            let mut reading = tails.reading.lock().unwrap_or_else(|e| e.into_inner());
            reading.remove(&key);
            tails.changed.send_replace(());
        });
        task.map(|_| ()).boxed().shared()
    }

    /// Reads the tails from the log source, or from the API when there
    /// is no source or the source returns no lines.
    async fn read(
        &self,
        namespace: &str,
        name: &str,
        since: Option<Timestamp>,
        containers: Vec<String>,
    ) -> Tails {
        let Some(reached) = self.sourcing.reached() else {
            return api::read(&self.client, namespace, name, containers).await;
        };
        let pod = (namespace, name, since);
        let patterns = self.sourcing.patterns.read();
        let asked = self
            .store
            .tails(&reached, pod, &containers, &patterns, READ_TIMEOUT);
        let source = reached.source.name.clone();
        match asked.await {
            Ok((containers, matching)) => Tails {
                read_at: Timestamp::now(),
                from: Origin::Store(source),
                forbidden: false,
                containers,
                matching,
            },
            Err(why) => {
                tracing::debug!(namespace, name, %why, "the log source gave no tail");
                Tails {
                    from: Origin::Fallback { source, why },
                    ..api::read(&self.client, namespace, name, containers).await
                }
            }
        }
    }
}

#[cfg(test)]
mod refresh_tests;

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    /// A read from `from` of one container, named proxy, with two lines.
    fn from(from: Origin) -> Tails {
        let line = tail::Line {
            text: "started".into(),
            level: level::Level::Plain,
            time: None,
            tags: Vec::new(),
        };
        Tails {
            read_at: "2026-01-01T12:34:56Z".parse().unwrap(),
            from,
            containers: vec![tail::Tail {
                container: "proxy".into(),
                read: tail::Read::Lines(vec![line.clone(), line]),
            }],
            ..Tails::default()
        }
    }

    #[rstest]
    #[case::the_api(
        Origin::Api,
        None,
        "Each container: up to its last 50 lines, from the Kubernetes API, read at 12:34:56 UTC",
        None
    )]
    #[case::a_store(
        Origin::Store("logs".into()),
        None,
        "Each container: up to its last 50 lines, from the LogSource logs, read at 12:34:56 UTC",
        None
    )]
    #[case::one_container(
        Origin::Api,
        Some("proxy"),
        "Container proxy: its last 2 lines, from the Kubernetes API, read at 12:34:56 UTC",
        None
    )]
    #[case::a_fallback(
        Origin::Fallback { source: "logs".into(), why: "is unreachable".into() },
        None,
        "Each container: up to its last 50 lines, from the Kubernetes API, read at 12:34:56 UTC",
        Some("The LogSource logs is unreachable, so these are the API's lines, which keep only each container's current run.")
    )]
    fn the_note_says_how_many_from_where_and_when(
        #[case] origin: Origin,
        #[case] container: Option<&str>,
        #[case] expected: &str,
        #[case] why: Option<&str>,
    ) {
        let tails = from(origin);
        assert_eq!(
            (
                note(&tails, container).as_str(),
                fallback(&tails).as_deref()
            ),
            (expected, why)
        );
    }
}
