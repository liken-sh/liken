//! Spreads requests to the API server over several HTTP/2 connections,
//! so that no request waits for a free stream.
//!
//! hyper's client sends every request to a host on one HTTP/2
//! connection, and the server's `SETTINGS_MAX_CONCURRENT_STREAMS`
//! limits how many streams are open on it at once. A request over the
//! limit waits, with no error, until another stream closes. A watch
//! keeps its stream open while it runs, so with enough watches a request
//! can wait forever. So each request goes to the connection with the
//! fewest open streams, or to a new connection when every connection
//! already has `most` streams open. A stream counts as open from the
//! request until its response body is dropped, which for a watch is when
//! the watch stops.

use std::pin::Pin;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::{Arc, Mutex};
use std::task::{Context, Poll};

use futures::future::BoxFuture;
use http::{Request, Response};
use hyper::body::{Body, Frame, Incoming, SizeHint};
use tower::ServiceExt;

/// A client for one connection, with a count of its open streams.
struct Connection<S> {
    client: S,
    open: Arc<AtomicUsize>,
}

/// A client that sends each request on one of several connections, each
/// made by `connect`, with at most `most` open streams on each.
pub struct Spread<S> {
    connections: Arc<Mutex<Vec<Connection<S>>>>,
    connect: Arc<dyn Fn() -> S + Send + Sync>,
    most: usize,
}

impl<S> Clone for Spread<S> {
    fn clone(&self) -> Self {
        Self {
            connections: self.connections.clone(),
            connect: self.connect.clone(),
            most: self.most,
        }
    }
}

impl<S> Spread<S> {
    pub fn new(most: usize, connect: impl Fn() -> S + Send + Sync + 'static) -> Self {
        Self {
            connections: Arc::default(),
            connect: Arc::new(connect),
            most: most.max(1),
        }
    }

    /// The open stream count of each connection opened so far.
    pub fn open(&self) -> Vec<usize> {
        let connections = self.connections.lock().unwrap();
        let open = connections.iter();
        open.map(|connection| connection.open.load(Ordering::SeqCst))
            .collect()
    }
}

impl<S: Clone> Spread<S> {
    /// Chooses the connection for a new request, and opens a new one when
    /// every connection has `most` streams open. Returns the connection's
    /// client, its index, and a guard that counts the new stream as open
    /// until the guard is dropped.
    fn open_stream(&self) -> (S, usize, Open) {
        let mut connections = self.connections.lock().unwrap();
        let open: Vec<usize> = connections
            .iter()
            .map(|connection| connection.open.load(Ordering::SeqCst))
            .collect();
        let at = match pick(&open, self.most) {
            Some(at) => at,
            None => {
                if !open.is_empty() {
                    tracing::info!(
                        ?open,
                        most = self.most,
                        "Every connection to the API server has the most streams open, so a new connection opens"
                    );
                }
                connections.push(Connection {
                    client: (self.connect)(),
                    open: Arc::default(),
                });
                connections.len() - 1
            }
        };
        let connection = &connections[at];
        connection.open.fetch_add(1, Ordering::SeqCst);
        (connection.client.clone(), at, Open(connection.open.clone()))
    }
}

/// Returns the index of the connection with the fewest streams in
/// `open`, or `None` when even that one has `most` or more, so the
/// caller must open another. Ties go to the lowest index, so earlier
/// connections fill first.
pub fn pick(open: &[usize], most: usize) -> Option<usize> {
    let fewest = open
        .iter()
        .enumerate()
        .min_by_key(|(at, open)| (**open, *at));
    fewest.filter(|(_, open)| **open < most).map(|(at, _)| at)
}

/// One open stream, which stays counted until this is dropped.
struct Open(Arc<AtomicUsize>);

impl Drop for Open {
    fn drop(&mut self) {
        self.0.fetch_sub(1, Ordering::SeqCst);
    }
}

/// A response body that keeps its stream counted as open until the body
/// is dropped.
pub struct Counted<B> {
    body: B,
    _open: Open,
}

impl<B: Body + Unpin> Body for Counted<B> {
    type Data = B::Data;
    type Error = B::Error;

    fn poll_frame(
        mut self: Pin<&mut Self>,
        cx: &mut Context<'_>,
    ) -> Poll<Option<Result<Frame<Self::Data>, Self::Error>>> {
        Pin::new(&mut self.body).poll_frame(cx)
    }

    fn is_end_stream(&self) -> bool {
        self.body.is_end_stream()
    }

    fn size_hint(&self) -> SizeHint {
        self.body.size_hint()
    }
}

impl<S, B> tower::Service<Request<B>> for Spread<S>
where
    S: tower::Service<Request<B>, Response = Response<Incoming>> + Clone + Send + 'static,
    S::Future: Send + 'static,
    S::Error: Into<tower::BoxError>,
    B: Send + 'static,
{
    type Response = Response<Counted<Incoming>>;
    type Error = tower::BoxError;
    type Future = BoxFuture<'static, Result<Self::Response, Self::Error>>;

    /// Always ready, because a request that finds every connection at
    /// `most` streams opens another connection instead of waiting.
    fn poll_ready(&mut self, _cx: &mut Context<'_>) -> Poll<Result<(), Self::Error>> {
        Poll::Ready(Ok(()))
    }

    fn call(&mut self, request: Request<B>) -> Self::Future {
        let (client, connection, open) = self.open_stream();
        let path = request.uri().path_and_query().map(ToString::to_string);
        Box::pin(async move {
            let response = client.oneshot(request).await.map_err(Into::into)?;
            let status = response.status().as_u16();
            tracing::trace!(
                connection,
                status,
                ?path,
                "The API server responded to a request"
            );
            Ok(response.map(|body| Counted { body, _open: open }))
        })
    }
}

#[cfg(test)]
mod tests;
