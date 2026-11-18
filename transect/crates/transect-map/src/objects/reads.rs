//! Reads one object of a kind that no store watches, such as a Lease,
//! with a single GET, and caches the response.
//!
//! A page's event stream renders the page again after each change in
//! the cluster, so a GET inside a render would repeat with every change.
//! Instead, a page load and an event stream's connect each read the
//! object once, and renders draw the cached response. Loads of an
//! object that is already being read wait for that GET instead of
//! sending another. Up to `AT_ONCE` reads of different objects run at
//! once, so one slow read delays only the loads of its own object.
//!
//! The pages have no authentication, so `Key::of` refuses an invalid
//! name and a core Secret or ConfigMap, which can contain credentials.

use std::collections::HashMap;
use std::sync::{Arc, Mutex};
use std::time::Duration;

use futures::FutureExt;
use futures::future::{BoxFuture, Shared};
use kube::core::{ApiResource, DynamicObject};
use kube::{Api, Client};
use tokio::sync::Semaphore;
use tokio::time::Instant;

/// How long a cached response serves new page loads.
pub const FRESH: Duration = Duration::from_secs(5);

/// The most responses cached. `keep` evicts the oldest first.
pub const MOST: usize = 256;

/// The most GETs in flight at once, so a burst of page loads does not
/// become a burst of requests to the API server.
const AT_ONCE: usize = 4;

/// How long one read may take.
const ATTEMPT: Duration = Duration::from_secs(5);

/// The longest DNS subdomain name, which Kubernetes allows for an
/// object name.
const NAME_CHARS: usize = 253;

#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub struct Key {
    pub resource: ApiResource,
    pub namespace: Option<String>,
    pub name: String,
}

impl Key {
    /// Returns none when `name` or `namespace` is not a valid
    /// Kubernetes name, and for a core Secret or ConfigMap.
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

/// Whether `name` is a DNS subdomain: lowercase letters, digits, `-`,
/// and `.`, starting and ending with a letter or a digit.
pub fn valid_name(name: &str) -> bool {
    let edge = |c: Option<char>| c.is_some_and(|c| c.is_ascii_lowercase() || c.is_ascii_digit());
    let inner = name
        .chars()
        .all(|c| c.is_ascii_lowercase() || c.is_ascii_digit() || c == '-' || c == '.');
    name.len() <= NAME_CHARS && inner && edge(name.chars().next()) && edge(name.chars().last())
}

#[derive(Clone, Debug, PartialEq)]
pub enum Read {
    Found(Arc<DynamicObject>),
    /// The API server has no such object.
    Missing,
    /// The API server returned 401 or 403. Holds its message.
    Refused(String),
    /// Any other failure, including a timeout. Holds the error message.
    Failed(String),
}

/// A GET in flight, shared by every load of its object.
type Reading = Shared<BoxFuture<'static, Read>>;

/// The cache of responses. Clones share it.
#[derive(Clone)]
pub struct Reads {
    /// None in tests, so that no read reaches an API server.
    client: Option<Client>,
    copies: Arc<Mutex<HashMap<Key, (Instant, Read)>>>,
    reading: Arc<Mutex<HashMap<Key, Reading>>>,
    permits: Arc<Semaphore>,
    /// Set for the fixture cluster, whose cached responses never go
    /// stale.
    fixed: bool,
}

impl Default for Reads {
    fn default() -> Self {
        Self {
            client: None,
            copies: Arc::default(),
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

    /// A cache with `read` for `key`, for testing what a page draws.
    #[cfg(any(test, feature = "fixtures"))]
    pub fn with_read(key: Key, read: Read) -> Self {
        let reads = Self::default();
        reads.keep(key, read);
        reads
    }

    /// A cache that serves `copies` forever, for the fixture cluster,
    /// which has no API server.
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

    /// The response for `key`: the cached one if it is younger than
    /// `FRESH`, otherwise the result of a new read.
    pub async fn read(&self, key: &Key) -> Read {
        let reading = {
            // Check the cache while holding the lock on `reading`. A
            // finished read caches its response before it removes
            // itself from `reading`, under the same lock, so a load
            // finds either the response or the read in flight and never
            // starts a second GET.
            let mut reading = self.reading.lock().unwrap();
            if let Some((at, read)) = self.copies.lock().unwrap().get(key)
                && (self.fixed || at.elapsed() < FRESH)
            {
                return read.clone();
            }
            let entry = reading.entry(key.clone());
            entry.or_insert_with(|| self.start(key.clone())).clone()
        };
        reading.await
    }

    /// Reads `key` on its own task, so the read finishes and caches its
    /// response even when the load that started it is dropped.
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
            tracing::debug!(kind = %key.resource.kind, name = %key.name, found, "Read an object from the API server");
            let mut reading = reads.reading.lock().unwrap();
            reads.keep(key.clone(), read.clone());
            reading.remove(&key);
            read
        });
        let failed = |_| Read::Failed("the read stopped before the API server responded".into());
        task.map(move |joined| joined.unwrap_or_else(failed))
            .boxed()
            .shared()
    }

    /// The cached response for `key` however old it is, or none.
    pub fn last(&self, key: &Key) -> Option<Read> {
        let copies = self.copies.lock().unwrap();
        copies.get(key).map(|(_, read)| read.clone())
    }

    fn keep(&self, key: Key, read: Read) {
        let mut copies = self.copies.lock().unwrap();
        copies.insert(key, (Instant::now(), read));
        while copies.len() > MOST {
            let oldest = copies.iter().min_by_key(|(_, (at, _))| *at);
            let Some(oldest) = oldest.map(|(key, _)| key.clone()) else {
                break;
            };
            copies.remove(&oldest);
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
            tracing::warn!(kind = %key.resource.kind, %error, "The API server returned an error for an object read");
            Read::Failed(error.to_string())
        }
        Err(_) => Read::Failed(format!("the API server did not respond within {ATTEMPT:?}")),
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
        let reads = Reads::with_read(key("web"), Read::Missing);
        tokio::time::advance(after).await;
        assert_eq!(reads.read(&key("web")).await, expected);
    }

    /// An API server that never responds to a read of `slow`, returns
    /// 404 for any other, and counts the requests it receives.
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
