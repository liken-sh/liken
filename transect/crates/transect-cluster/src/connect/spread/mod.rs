//! Spreading requests over a few HTTP/2 connections, so that no request
//! waits for a stream.
//!
//! hyper's client keeps one HTTP/2 connection per host and multiplexes
//! every request on it.  The server's `SETTINGS_MAX_CONCURRENT_STREAMS`
//! caps how many streams are open on that connection at once, and a
//! request past the cap waits for a stream to close, with no error.
//! A watch holds its stream for as long as it runs, so a watch past the
//! cap would wait forever.  So each request goes to the connection with the
//! fewest open streams, and when every connection has `most` open, to a
//! new one.  A stream counts as open from the request until its
//! response body is dropped, which for a watch is when the watch stops.

use std::pin::Pin;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::{Arc, Mutex};
use std::task::{Context, Poll};

use futures::future::BoxFuture;
use http::{Request, Response};
use hyper::body::{Body, Frame, Incoming, SizeHint};
use tower::ServiceExt;

/// One connection's client, and how many of its streams are open.
struct Connection<S> {
    client: S,
    open: Arc<AtomicUsize>,
}

/// Requests spread over clients that `connect` makes, each kept below
/// `most` open streams.
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

    /// How many connections it has opened, and the streams open on each.
    pub fn open(&self) -> Vec<usize> {
        let connections = self.connections.lock().unwrap();
        let open = connections.iter();
        open.map(|connection| connection.open.load(Ordering::SeqCst))
            .collect()
    }
}

impl<S: Clone> Spread<S> {
    /// The client of the connection a new request goes to, which one it
    /// is, and the stream it opens, counted.
    fn take(&self) -> (S, usize, Open) {
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
                        "every connection to the API server has its most streams open; opening another"
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

/// The connection with the fewest of `open` streams, when it has fewer
/// than `most`, or none, so that the caller opens another.  Ties go to
/// the first, so the earliest connections fill before a later one is
/// used.
pub fn pick(open: &[usize], most: usize) -> Option<usize> {
    let fewest = open
        .iter()
        .enumerate()
        .min_by_key(|(at, open)| (**open, *at));
    fewest.filter(|(_, open)| **open < most).map(|(at, _)| at)
}

/// One open stream, counted until this is dropped.
struct Open(Arc<AtomicUsize>);

impl Drop for Open {
    fn drop(&mut self) {
        self.0.fetch_sub(1, Ordering::SeqCst);
    }
}

/// A response body that keeps its stream counted until it is dropped.
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

    /// Always ready: a request that would find every connection full
    /// opens another rather than wait.
    fn poll_ready(&mut self, _cx: &mut Context<'_>) -> Poll<Result<(), Self::Error>> {
        Poll::Ready(Ok(()))
    }

    fn call(&mut self, request: Request<B>) -> Self::Future {
        let (client, connection, open) = self.take();
        let path = request.uri().path_and_query().map(ToString::to_string);
        Box::pin(async move {
            let response = client.oneshot(request).await.map_err(Into::into)?;
            let status = response.status().as_u16();
            tracing::trace!(connection, status, ?path, "answered");
            Ok(response.map(|body| Counted { body, _open: open }))
        })
    }
}

#[cfg(test)]
mod tests;
