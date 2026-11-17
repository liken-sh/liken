//! Reading one object of a kind that no store contains, such as a
//! Lease, once when its page loads.
//!
//! A page's event stream renders it on every settled change, so a read
//! inside a render would run every second or two.  So a page load, and
//! an event stream when it connects, read the object with one GET and
//! keep the API server's answer in this module.  Every render draws
//! that copy, and no connection stays open for it.  A copy read in the
//! last `FRESH` serves the next load too, so a burst of loads makes one
//! request: a load of an object already being read waits for that
//! read's answer rather than asking again.  Reads of different objects
//! run side by side, at most `AT_ONCE` of them, so one slow read delays
//! only the loads of its own object.
//!
//! The pages have no authentication yet, so a read asks only for a name
//! that is valid in Kubernetes, and never for a Secret or a ConfigMap,
//! either of which may contain a credential.  At most `MOST` copies are
//! kept, and the oldest is removed first.

use std::collections::HashMap;
use std::sync::{Arc, Mutex};
use std::time::Duration;

use futures::FutureExt;
use futures::future::{BoxFuture, Shared};
use kube::core::{ApiResource, DynamicObject};
use kube::{Api, Client};
use tokio::sync::Semaphore;
use tokio::time::Instant;

/// How long a copy serves a new page load before it is read again.
pub const FRESH: Duration = Duration::from_secs(5);

/// The most copies kept.
pub const MOST: usize = 256;

/// The most reads of different objects under way at once, which keeps
/// a burst of page loads from becoming a burst at the API server.
const AT_ONCE: usize = 4;

/// How long one read may take.
const ATTEMPT: Duration = Duration::from_secs(5);

/// The longest name Kubernetes allows, a DNS subdomain's.
const NAME_CHARS: usize = 253;

/// One object, by its kind's resource, its namespace, and its name.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub struct Key {
    pub resource: ApiResource,
    pub namespace: Option<String>,
    pub name: String,
}

impl Key {
    /// The key of `name` in `namespace`, or none for a name or a
    /// namespace that is not valid in Kubernetes, or for a Secret or a
    /// ConfigMap.
    pub fn of(resource: &ApiResource, namespace: Option<&str>, name: &str) -> Option<Self> {
        let credential =
            resource.group.is_empty() && matches!(resource.kind.as_str(), "Secret" | "ConfigMap");
        let names = valid_name(name) && namespace.is_none_or(valid_name);
        (names && !credential).then(|| Self {
            resource: resource.clone(),
            namespace: namespace.map(Into::into),
            name: name.into(),
        })
    }
}

/// Whether `name` is valid as a Kubernetes object's name: a DNS
/// subdomain, which is lowercase letters, digits, `-`, and `.`, starting
/// and ending with a letter or a digit.  Anything else, such as a `,` or
/// an `=`, is refused before it reaches the API server.
pub fn valid_name(name: &str) -> bool {
    let edge = |c: Option<char>| c.is_some_and(|c| c.is_ascii_lowercase() || c.is_ascii_digit());
    let inner = name
        .chars()
        .all(|c| c.is_ascii_lowercase() || c.is_ascii_digit() || c == '-' || c == '.');
    name.len() <= NAME_CHARS && inner && edge(name.chars().next()) && edge(name.chars().last())
}

/// The API server's answer for one object.
#[derive(Clone, Debug, PartialEq)]
pub enum Read {
    Found(Arc<DynamicObject>),
    /// The API server has no such object.
    Missing,
    /// The API server refused the read with a 401 or a 403, with its
    /// message.
    Refused(String),
    /// The read failed for another reason, with the error's message.
    Failed(String),
}

/// A read under way, which every load of its object shares.
type Reading = Shared<BoxFuture<'static, Read>>;

/// The copies read so far, shared by every clone.
#[derive(Clone)]
pub struct Reads {
    /// The connection to the API server, which a test leaves out, so
    /// that nothing is read.
    client: Option<Client>,
    held: Arc<Mutex<HashMap<Key, (Instant, Read)>>>,
    /// The reads under way, by the object each reads.
    reading: Arc<Mutex<HashMap<Key, Reading>>>,
    /// A permit for each read under way, up to `AT_ONCE`.
    permits: Arc<Semaphore>,
    /// Whether every kept copy stays fresh forever, as the fixture's
    /// copies do.
    fixed: bool,
}

impl Default for Reads {
    fn default() -> Self {
        Self {
            client: None,
            held: Arc::default(),
            reading: Arc::default(),
            permits: Arc::new(Semaphore::new(AT_ONCE)),
            fixed: false,
        }
    }
}

impl Reads {
    pub fn new(client: Client) -> Self {
        Self {
            client: Some(client),
            ..Self::default()
        }
    }

    /// Reads that keep `read` for `key`, for a test of what a page draws.
    #[cfg(any(test, feature = "fixtures"))]
    pub fn holding(key: Key, read: Read) -> Self {
        let reads = Self::default();
        reads.keep(key, read);
        reads
    }

    /// Reads that keep `copies` forever and read nothing, for the
    /// fixture cluster, which has no API server.
    #[cfg(any(test, debug_assertions))]
    pub fn fixed(copies: impl IntoIterator<Item = (Key, Read)>) -> Self {
        let reads = Self {
            fixed: true,
            ..Self::default()
        };
        for (key, read) in copies {
            reads.keep(key, read);
        }
        reads
    }

    /// The API server's answer for `key`, from a copy read in the last
    /// `FRESH`, or read now.
    pub async fn read(&self, key: &Key) -> Read {
        let reading = {
            // The check runs under the lock of the reads under way.  A
            // read removes itself from them only after it keeps its
            // copy, so a load never misses both the copy and the read
            // and then asks again.
            let mut reading = self.reading.lock().unwrap();
            if let Some((at, read)) = self.held.lock().unwrap().get(key)
                && (self.fixed || at.elapsed() < FRESH)
            {
                return read.clone();
            }
            let entry = reading.entry(key.clone());
            entry.or_insert_with(|| self.start(key.clone())).clone()
        };
        reading.await
    }

    /// Reads `key` on a task of its own, so the read finishes and keeps
    /// its copy whether or not the load that asked still waits.
    fn start(&self, key: Key) -> Reading {
        let reads = self.clone();
        let task = tokio::spawn(async move {
            let read = match reads.client.clone() {
                Some(client) => {
                    let _permit = reads.permits.acquire().await;
                    get(client, &key).await
                }
                None => Read::Failed("transect has no connection to the API server".into()),
            };
            let found = matches!(read, Read::Found(_));
            tracing::debug!(kind = %key.resource.kind, name = %key.name, found, "read an object once");
            let mut reading = reads.reading.lock().unwrap();
            reads.keep(key.clone(), read.clone());
            reading.remove(&key);
            read
        });
        let failed = |_| Read::Failed("the read stopped before it answered".into());
        task.map(move |joined| joined.unwrap_or_else(failed))
            .boxed()
            .shared()
    }

    /// The copy of `key` read last, however old, or none when none is
    /// kept.
    pub fn last(&self, key: &Key) -> Option<Read> {
        let held = self.held.lock().unwrap();
        held.get(key).map(|(_, read)| read.clone())
    }

    fn keep(&self, key: Key, read: Read) {
        let mut held = self.held.lock().unwrap();
        held.insert(key, (Instant::now(), read));
        while held.len() > MOST {
            let oldest = held.iter().min_by_key(|(_, (at, _))| *at);
            let Some(oldest) = oldest.map(|(key, _)| key.clone()) else {
                break;
            };
            held.remove(&oldest);
        }
    }
}

async fn get(client: Client, key: &Key) -> Read {
    let api: Api<DynamicObject> = match &key.namespace {
        Some(namespace) => Api::namespaced_with(client, namespace, &key.resource),
        None => Api::all_with(client, &key.resource),
    };
    let found = tokio::time::timeout(ATTEMPT, api.get(&key.name)).await;
    match found {
        Ok(Ok(mut object)) => {
            transect_cluster::cluster::trim_meta(&mut object.metadata);
            Read::Found(Arc::new(object))
        }
        Ok(Err(kube::Error::Api(status))) if status.code == 404 => Read::Missing,
        Ok(Err(kube::Error::Api(status))) if matches!(status.code, 401 | 403) => {
            Read::Refused(status.message.clone())
        }
        Ok(Err(error)) => {
            tracing::warn!(kind = %key.resource.kind, %error, "reading an object failed");
            Read::Failed(error.to_string())
        }
        Err(_) => Read::Failed(format!("no answer in {ATTEMPT:?}")),
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn resource(group: &str, kind: &str) -> ApiResource {
        ApiResource {
            group: group.into(),
            version: "v1".into(),
            api_version: "v1".into(),
            kind: kind.into(),
            plural: String::new(),
        }
    }

    fn lease() -> ApiResource {
        resource("coordination.k8s.io", "Lease")
    }

    #[rstest]
    #[case::a_name("web-1", true)]
    #[case::a_dotted_name("db9c8771.example.io", true)]
    #[case::a_comma("web,other", false)]
    #[case::an_equals_sign("web=1", false)]
    #[case::a_field_selector("metadata.name=web", false)]
    #[case::a_slash("a/b", false)]
    #[case::upper_case("Web", false)]
    #[case::a_leading_dash("-web", false)]
    #[case::a_trailing_dot("web.", false)]
    #[case::empty("", false)]
    #[case::too_long(&"a".repeat(254), false)]
    fn reads_only_a_name_kubernetes_could_hold(#[case] name: &str, #[case] read: bool) {
        assert_eq!(Key::of(&lease(), Some("shop"), name).is_some(), read);
    }

    #[rstest]
    #[case::a_secret("", "Secret", false)]
    #[case::a_config_map("", "ConfigMap", false)]
    #[case::a_lease("coordination.k8s.io", "Lease", true)]
    #[case::a_custom_secret("example.io", "Secret", true)]
    fn never_reads_what_may_hold_a_credential(
        #[case] group: &str,
        #[case] kind: &str,
        #[case] read: bool,
    ) {
        let key = Key::of(&resource(group, kind), Some("shop"), "web");
        assert_eq!(key.is_some(), read);
    }

    #[rstest]
    fn refuses_a_namespace_kubernetes_could_not_hold() {
        assert!(Key::of(&lease(), Some("shop,x"), "web").is_none());
    }

    fn key(name: &str) -> Key {
        Key::of(&lease(), Some("shop"), name).unwrap()
    }

    #[rstest]
    #[tokio::test(start_paused = true)]
    async fn keeps_at_most_the_newest_copies() {
        let reads = Reads::default();
        for n in 0..=MOST {
            reads.keep(key(&format!("lease-{n}")), Read::Missing);
            tokio::time::advance(Duration::from_millis(1)).await;
        }
        let kept = (reads.last(&key("lease-0")), reads.last(&key("lease-1")));
        assert_eq!(kept, (None, Some(Read::Missing)));
    }

    #[rstest]
    #[case::fresh(Duration::from_secs(1), Read::Missing)]
    #[case::stale(FRESH, Read::Failed("transect has no connection to the API server".into()))]
    #[tokio::test(start_paused = true)]
    async fn serves_a_fresh_copy_without_reading_again(
        #[case] after: Duration,
        #[case] expected: Read,
    ) {
        let reads = Reads::holding(key("web"), Read::Missing);
        tokio::time::advance(after).await;
        assert_eq!(reads.read(&key("web")).await, expected);
    }

    /// An API server that never answers a read of `slow`, answers 404
    /// to any other, and counts the reads it was sent.
    async fn server() -> (Reads, Arc<std::sync::atomic::AtomicUsize>) {
        use std::sync::atomic::Ordering;

        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let url = format!("http://{}", listener.local_addr().unwrap());
        let asked = Arc::new(std::sync::atomic::AtomicUsize::new(0));
        let counted = asked.clone();
        tokio::spawn(async move {
            while let Ok((tcp, _)) = listener.accept().await {
                let counted = counted.clone();
                tokio::spawn(async move {
                    let service = hyper::service::service_fn(
                        move |request: http::Request<hyper::body::Incoming>| {
                            counted.fetch_add(1, Ordering::SeqCst);
                            let slow = request.uri().path().ends_with("/slow");
                            async move {
                                if slow {
                                    futures::future::pending::<()>().await;
                                }
                                let status = r#"{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"NotFound","code":404}"#;
                                let response = http::Response::builder()
                                    .status(404)
                                    .header("content-type", "application/json")
                                    .body(http_body_util::Full::new(hyper::body::Bytes::from(
                                        status,
                                    )))
                                    .unwrap();
                                Ok::<_, std::convert::Infallible>(response)
                            }
                        },
                    );
                    let serving = hyper::server::conn::http1::Builder::new()
                        .serve_connection(hyper_util::rt::TokioIo::new(tcp), service);
                    let _ = serving.await;
                });
            }
        });
        let client = Client::try_from(kube::Config::new(url.parse().unwrap())).unwrap();
        (Reads::new(client), asked)
    }

    #[rstest]
    #[tokio::test]
    async fn a_slow_read_holds_up_no_read_of_another_object() {
        let (reads, _) = server().await;
        let slow = reads.clone();
        tokio::spawn(async move { slow.read(&key("slow")).await });
        tokio::task::yield_now().await;
        let quick = key("quick");
        let quick = tokio::time::timeout(Duration::from_secs(1), reads.read(&quick));
        assert_eq!(quick.await, Ok(Read::Missing));
    }

    #[rstest]
    #[tokio::test]
    async fn loads_of_one_object_at_once_send_one_read() {
        let (reads, asked) = server().await;
        let web = key("web");
        let loads = (0..5).map(|_| reads.read(&web));
        let read = futures::future::join_all(loads).await;
        let asked = asked.load(std::sync::atomic::Ordering::SeqCst);
        assert_eq!((read, asked), (vec![Read::Missing; 5], 1));
    }
}
