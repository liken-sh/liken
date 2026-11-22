//! The server-sent event stream of a page. When the cluster changes,
//! the view's task in `views` renders the page's `main` block again,
//! and each browser that has the page open receives it.

use std::convert::Infallible;
use std::sync::Arc;

use axum::extract::{Path, Query, State};
use axum::http::StatusCode;
use axum::response::sse::{Event, KeepAlive, Sse};
use axum::response::{IntoResponse, Response};
use futures::{Stream, StreamExt};
use tokio::sync::watch;

use super::AppState;
use super::view::{self, Page};

/// The SSE event name that each page's `sse-swap` attribute listens for.
pub const VIEW: &str = "view";

/// Sends the latest rendering when a browser connects, then each new
/// rendering. The first event matters after a server restart, because
/// htmx reconnects by itself and the page it shows may be minutes old.
/// When the browser disconnects, axum drops the stream and `latest`
/// with it. The view's task stops when no receiver is left.
pub fn stream(
    latest: watch::Receiver<Arc<str>>,
) -> Sse<impl Stream<Item = Result<Event, Infallible>>> {
    let events = renderings(latest).map(|html| Ok(Event::default().event(VIEW).data(&*html)));
    // Proxies and load balancers often close a connection that is idle
    // for a minute. axum's default keep-alive sends a comment every 15
    // seconds, so the connection stays open while the cluster is quiet.
    Sse::new(events).keep_alive(KeepAlive::default())
}

/// The latest rendering, then each new one. An empty rendering is
/// skipped, because swapping it in would blank the page. A view
/// publishes an empty rendering when its first render fails.
fn renderings(latest: watch::Receiver<Arc<str>>) -> impl Stream<Item = Arc<str>> {
    futures::stream::unfold((latest, true), |(mut latest, mut first)| async move {
        loop {
            if !first {
                latest.changed().await.ok()?;
            }
            first = false;
            let html = latest.borrow_and_update().clone();
            if !html.is_empty() {
                return Some((html, (latest, false)));
            }
        }
    })
}

/// The event stream of the cluster map, whose path is empty.
pub async fn page_at_root(state: State<AppState>, query: Query<view::Query>) -> Response {
    page(state, Path(String::new()), query).await
}

/// The event stream of the page at `path`.
pub async fn page(
    State(state): State<AppState>,
    Path(path): Path<String>,
    Query(query): Query<view::Query>,
) -> Response {
    let Some(page) = Page::at(&path, &query) else {
        return StatusCode::NOT_FOUND.into_response();
    };
    // An event stream can connect with no page load before it, or after
    // `Reads` dropped its copy of the page's object, so read the object
    // here as a page load would.
    state.sources.prepare(&page).await;
    stream(state.views.subscribe(page).await).into_response()
}

#[cfg(test)]
mod tests {
    use futures::StreamExt;
    use rstest::rstest;

    use super::*;

    #[rstest]
    #[tokio::test]
    async fn sends_the_latest_rendering_at_once() {
        let (_published, latest) = watch::channel(Arc::from("one"));
        let mut renderings = std::pin::pin!(renderings(latest));
        assert_eq!(&*renderings.next().await.unwrap(), "one");
    }

    #[rstest]
    #[tokio::test]
    async fn skips_an_empty_rendering() {
        let (published, latest) = watch::channel(Arc::from(""));
        let mut renderings = std::pin::pin!(renderings(latest));
        // `yield_now` lets the stream read the empty rendering before
        // the next one is published.
        let publish = async {
            tokio::task::yield_now().await;
            published.send_replace(Arc::from("two"));
        };
        let (sent, ()) = tokio::join!(renderings.next(), publish);
        assert_eq!(&*sent.unwrap(), "two");
    }
}
