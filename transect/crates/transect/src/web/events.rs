//! The event stream: a view's `main` block, pushed to each page open on
//! that view over server-sent events after the cluster changes.  The
//! renderings come from the view's task in `views`.

use std::convert::Infallible;
use std::sync::Arc;

use axum::response::sse::{Event, KeepAlive, Sse};
use futures::{Stream, StreamExt};
use tokio::sync::watch;

/// The SSE event name each page's `sse-swap` listens for.
pub const VIEW: &str = "view";

/// Streams the latest rendering as soon as a browser connects, then each
/// new one.  The first event matters after a restart: htmx reconnects on
/// its own, and the page it holds may be minutes old by then.  Dropping
/// the stream when the browser leaves drops `latest`, which lowers the
/// receiver count that the view's task reads to find how many browsers
/// have it open.
pub fn stream(
    latest: watch::Receiver<Arc<str>>,
) -> Sse<impl Stream<Item = Result<Event, Infallible>>> {
    let events = renderings(latest).map(|html| Ok(Event::default().event(VIEW).data(&*html)));
    // Proxies and load balancers close a connection that stays idle, often
    // after a minute.  The keep-alive comment, sent every 15 s, keeps it
    // open through a quiet cluster.
    Sse::new(events).keep_alive(KeepAlive::default())
}

/// The latest rendering, then each new one.  An empty rendering, which a
/// view publishes when its first render failed, is never sent, since
/// swapping it in would blank the page.
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
        // The stream reads the empty rendering before this publishes.
        let publish = async {
            tokio::task::yield_now().await;
            published.send_replace(Arc::from("two"));
        };
        let (sent, ()) = tokio::join!(renderings.next(), publish);
        assert_eq!(&*sent.unwrap(), "two");
    }
}
