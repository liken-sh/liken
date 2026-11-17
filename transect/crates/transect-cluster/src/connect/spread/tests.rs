//! What a request does at an HTTP/2 server's stream limit: one hyper
//! client waits with no error, and `Spread` opens another connection.
//! A stream also stops being counted however it ends: its body
//! dropped, a reset, or a request given up on before its answer.

use std::convert::Infallible;
use std::time::Duration;

use bytes::Bytes;
use http::{Request, Response};
use http_body_util::{Empty, StreamBody};
use hyper::body::{Frame, Incoming};
use hyper_util::client::legacy::Client;
use hyper_util::client::legacy::connect::HttpConnector;
use hyper_util::rt::{TokioExecutor, TokioIo, TokioTimer};
use rstest::rstest;
use tower::ServiceExt;

use super::*;

#[rstest]
#[case::none_yet(&[], 2, None)]
#[case::room_on_the_first(&[1], 2, Some(0))]
#[case::the_fewest(&[2, 1, 2], 3, Some(1))]
#[case::the_first_of_a_tie(&[1, 1], 2, Some(0))]
#[case::every_one_full(&[2, 2], 2, None)]
fn sends_a_request_where_it_has_room(
    #[case] open: &[usize],
    #[case] most: usize,
    #[case] expected: Option<usize>,
) {
    assert_eq!(pick(open, most), expected);
}

/// How a test server answers each request.
#[derive(Clone, Copy)]
enum Answer {
    /// With a body it holds open, as a watch's is.
    Held,
    /// By resetting the stream.
    Reset,
    /// Never.
    Never,
}

/// A server that allows `streams` streams on each connection and holds
/// every response body open, as a watch's is, and counts the
/// connections it accepts.
async fn server(streams: u32) -> (String, Arc<AtomicUsize>) {
    answering(streams, Answer::Held).await
}

/// A server that allows `streams` streams on each connection, answers
/// each request as `answer` states, and counts the connections it
/// accepts.
async fn answering(streams: u32, answer: Answer) -> (String, Arc<AtomicUsize>) {
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let (address, accepted) = (
        listener.local_addr().unwrap(),
        Arc::new(AtomicUsize::new(0)),
    );
    let counted = accepted.clone();
    tokio::spawn(async move {
        while let Ok((tcp, _)) = listener.accept().await {
            counted.fetch_add(1, Ordering::SeqCst);
            tokio::spawn(async move {
                let service = hyper::service::service_fn(move |_: Request<Incoming>| async move {
                    match answer {
                        Answer::Held => {}
                        Answer::Reset => return Err("refused"),
                        Answer::Never => futures::future::pending().await,
                    }
                    let open = futures::stream::pending::<Result<Frame<Bytes>, Infallible>>();
                    Ok(Response::new(StreamBody::new(open)))
                });
                let serving = hyper::server::conn::http2::Builder::new(TokioExecutor::new())
                    .max_concurrent_streams(streams)
                    .serve_connection(TokioIo::new(tcp), service);
                let _ = serving.await;
            });
        }
    });
    (format!("http://{address}/"), accepted)
}

type Hyper = Client<HttpConnector, Empty<Bytes>>;

fn hyper() -> Hyper {
    Client::builder(TokioExecutor::new())
        .http2_only(true)
        .timer(TokioTimer::new())
        .build_http()
}

/// Whether each of `count` requests to `url` through `service` is
/// answered within a second, holding every answer open.
async fn answered<S>(service: S, url: &str, count: usize) -> Vec<bool>
where
    S: tower::Service<Request<Empty<Bytes>>> + Clone,
{
    let mut held = Vec::new();
    let mut answered = Vec::new();
    for _ in 0..count {
        let request = Request::get(url).body(Empty::new()).unwrap();
        let answer = tokio::time::timeout(Duration::from_secs(1), service.clone().oneshot(request));
        match answer.await {
            Ok(Ok(response)) => {
                held.push(response);
                answered.push(true);
            }
            _ => answered.push(false),
        }
    }
    answered
}

#[rstest]
#[tokio::test]
async fn one_client_waits_at_the_servers_limit_with_no_error() {
    let (url, _) = server(2).await;
    assert_eq!(answered(hyper(), &url, 3).await, [true, true, false]);
}

#[rstest]
#[tokio::test]
async fn a_request_past_the_limit_opens_another_connection() {
    let (url, accepted) = server(2).await;
    let spread = Spread::new(2, hyper);
    assert_eq!(answered(spread.clone(), &url, 5).await, [true; 5]);
    assert_eq!(accepted.load(Ordering::SeqCst), 3);
}

#[rstest]
#[tokio::test]
async fn a_closed_stream_makes_room_on_its_connection() {
    let (url, accepted) = server(2).await;
    let spread = Spread::new(2, hyper);
    let request = || Request::get(&url).body(Empty::new()).unwrap();
    let first = spread.clone().oneshot(request()).await.unwrap();
    let _second = spread.clone().oneshot(request()).await.unwrap();
    drop(first);
    let _third = spread.clone().oneshot(request()).await.unwrap();
    assert_eq!(
        (spread.open(), accepted.load(Ordering::SeqCst)),
        (vec![2], 1)
    );
}

#[rstest]
#[tokio::test]
async fn a_reset_stream_is_no_longer_counted() {
    let (url, _) = answering(2, Answer::Reset).await;
    let spread = Spread::new(2, hyper);
    let request = Request::get(&url).body(Empty::<Bytes>::new()).unwrap();
    let answered = spread.clone().oneshot(request).await;
    assert_eq!((answered.is_err(), spread.open()), (true, vec![0]));
}

#[rstest]
#[tokio::test]
async fn a_request_given_up_on_is_no_longer_counted() {
    let (url, _) = answering(2, Answer::Never).await;
    let spread = Spread::new(2, hyper);
    let request = Request::get(&url).body(Empty::<Bytes>::new()).unwrap();
    let waited = tokio::time::timeout(Duration::from_millis(100), spread.clone().oneshot(request));
    assert_eq!((waited.await.is_err(), spread.open()), (true, vec![0]));
}
