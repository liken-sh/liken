use std::convert::Infallible;
use std::sync::Arc;
use std::sync::atomic::{AtomicUsize, Ordering};

use http::{Request, Response, StatusCode};
use http_body_util::Full;
use hyper::body::{Bytes, Incoming};
use hyper_util::rt::{TokioExecutor, TokioIo};
use rstest::rstest;

#[rstest]
#[case::kube_apiservers(250, 200)]
#[case::the_least_recommended(100, 80)]
#[case::one(1, 1)]
fn stays_a_fifth_below_the_limit(#[case] limit: u32, #[case] expected: usize) {
    assert_eq!(super::most(limit), expected);
}

/// A server that answers its first request 429, with a `Retry-After`
/// of no seconds, and every later one 200, and counts the requests.
async fn busy_once() -> (String, Arc<AtomicUsize>) {
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let address = listener.local_addr().unwrap();
    let asked = Arc::new(AtomicUsize::new(0));
    let counted = asked.clone();
    tokio::spawn(async move {
        while let Ok((tcp, _)) = listener.accept().await {
            let counted = counted.clone();
            tokio::spawn(async move {
                let service = hyper::service::service_fn(move |_: Request<Incoming>| {
                    let first = counted.fetch_add(1, Ordering::SeqCst) == 0;
                    let status = match first {
                        true => StatusCode::TOO_MANY_REQUESTS,
                        false => StatusCode::OK,
                    };
                    let response = Response::builder()
                        .status(status)
                        .header("Retry-After", "0")
                        .body(Full::new(Bytes::from_static(b"answered")))
                        .unwrap();
                    async move { Ok::<_, Infallible>(response) }
                });
                let serving = hyper::server::conn::http1::Builder::new()
                    .serve_connection(TokioIo::new(tcp), service);
                let _ = serving.await;
            });
        }
    });
    (format!("http://{address}"), asked)
}

#[rstest]
#[case::retried(true, true, 2)]
#[case::not_retried(false, false, 1)]
#[tokio::test]
async fn retries_a_429_as_kubes_own_client_does(
    #[case] default_retry: bool,
    #[case] answered: bool,
    #[case] requests: usize,
) {
    let (url, asked) = busy_once().await;
    let mut config = kube::Config::new(url.parse().unwrap());
    config.default_retry = default_retry;
    let connections = hyper_util::client::legacy::Client::builder(TokioExecutor::new())
        .build_http::<kube::client::Body>();
    let client = super::layered(&config, connections).unwrap();
    let request = Request::get("/").body(Vec::new()).unwrap();
    let text = client.request_text(request).await;
    assert_eq!(
        (text.is_ok(), asked.load(Ordering::SeqCst)),
        (answered, requests)
    );
}
