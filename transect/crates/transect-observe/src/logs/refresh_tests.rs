//! Tests of a page load's read of a pod's tails.  The log source and
//! the API server accept connections and never respond, so every
//! request waits until its timeout.

use std::sync::Arc;
use std::sync::atomic::{AtomicUsize, Ordering};

use k8s_openapi::api::core::v1::{Container, Pod, PodSpec};
use kube::api::ObjectMeta;
use rstest::{fixture, rstest};
use tokio::net::TcpListener;
use tokio::sync::watch;
use tokio::time::{Duration, Instant, sleep};

use super::source::fixtures::source;
use super::*;

/// An address that accepts connections, counts them, and never
/// responds.
struct Silent {
    url: String,
    connections: Arc<AtomicUsize>,
}

async fn silent() -> Silent {
    let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
    let url = format!("http://{}", listener.local_addr().unwrap());
    let connections = Arc::new(AtomicUsize::new(0));
    let counted = connections.clone();
    tokio::spawn(async move {
        let mut held = Vec::new();
        while let Ok((socket, _)) = listener.accept().await {
            counted.fetch_add(1, Ordering::SeqCst);
            held.push(socket);
        }
    });
    Silent { url, connections }
}

/// The tails of a cluster whose log source and API server both never
/// respond, with the log source's `Silent` address.
async fn silent_tails() -> (LogTails, Silent) {
    let (store, api) = (silent().await, silent().await);
    let config = kube::Config::new(api.url.parse().unwrap());
    let client = Client::try_from(config).unwrap();
    let sourcing = Sourcing {
        source: Field::fixed(Some(source())),
        patterns: Field::fixed(Vec::new()),
        url: Some(store.url.clone()),
        domain: "cluster.local".into(),
    };
    let (changed, _) = watch::channel(());
    (LogTails::new(client, sourcing, changed).unwrap(), store)
}

#[fixture]
fn pod() -> Pod {
    Pod {
        metadata: ObjectMeta {
            namespace: Some("shop".into()),
            name: Some("web-1".into()),
            ..ObjectMeta::default()
        },
        spec: Some(PodSpec {
            containers: vec![Container {
                name: "web".into(),
                ..Container::default()
            }],
            ..PodSpec::default()
        }),
        ..Pod::default()
    }
}

#[rstest]
#[tokio::test(start_paused = true)]
async fn a_page_waits_for_a_silent_source_no_longer_than_the_timeout(pod: Pod) {
    let (tails, _store) = silent_tails().await;
    let started = Instant::now();
    tails.refresh(&pod).await;
    assert!(started.elapsed() <= READ_TIMEOUT + Duration::from_millis(100));
}

#[rstest]
#[tokio::test(start_paused = true)]
async fn the_read_goes_on_after_the_page_stops_waiting(pod: Pod) {
    let (tails, _store) = silent_tails().await;
    tails.refresh(&pod).await;
    let before = tails.cached("shop", "web-1").is_some();
    sleep(READ_TIMEOUT * 2).await;
    let after = tails
        .cached("shop", "web-1")
        .map(|tails| tails.from.clone());
    let fell_back = matches!(after, Some(Origin::Fallback { .. }));
    assert_eq!((before, fell_back), (false, true));
}

#[rstest]
#[tokio::test(start_paused = true)]
async fn page_loads_of_one_pod_together_share_one_read(pod: Pod) {
    let (tails, store) = silent_tails().await;
    tokio::join!(tails.refresh(&pod), tails.refresh(&pod));
    assert_eq!(store.connections.load(Ordering::SeqCst), 1);
}
