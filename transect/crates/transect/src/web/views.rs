//! One render task for each view, keyed by URL, that at least one
//! browser has open.  The task renders when the first browser connects
//! and again after each burst of changes settles, and publishes on a
//! `watch` channel that every browser on the view reads.  So a change
//! costs one render per open view, however many browsers have it open,
//! and a browser that falls behind skips to the newest rendering.  The
//! task ends when the last browser disconnects.

use std::collections::HashMap;
use std::future::Future;
use std::hash::Hash;
use std::sync::{Arc, Mutex};
use std::time::Duration;

use futures::FutureExt;
use futures::future::BoxFuture;
use tokio::sync::watch;

use super::debounce::settle;

type Render<K> = dyn Fn(K) -> BoxFuture<'static, Option<String>> + Send + Sync;

/// The channel a view's task publishes its renderings on.
type Published = Arc<watch::Sender<Arc<str>>>;

pub struct Views<K> {
    open: Arc<Mutex<HashMap<K, Published>>>,
    changes: watch::Sender<()>,
    quiet: Duration,
    cap: Duration,
    render: Arc<Render<K>>,
}

impl<K> Clone for Views<K> {
    fn clone(&self) -> Self {
        Self {
            open: self.open.clone(),
            changes: self.changes.clone(),
            quiet: self.quiet,
            cap: self.cap,
            render: self.render.clone(),
        }
    }
}

impl<K: Clone + Eq + Hash + Send + Sync + 'static> Views<K> {
    /// A send on `changes` makes every open view render again.  When
    /// `render` returns `None`, the view keeps its last rendering.
    pub fn new<F>(
        changes: watch::Sender<()>,
        quiet: Duration,
        cap: Duration,
        render: impl Fn(K) -> F + Send + Sync + 'static,
    ) -> Self
    where
        F: Future<Output = Option<String>> + Send + 'static,
    {
        Self {
            open: Arc::default(),
            changes,
            quiet,
            cap,
            render: Arc::new(move |view| render(view).boxed()),
        }
    }

    /// Returns a receiver of `view`'s renderings, and starts the view's
    /// task when no browser has it open.  The first render runs without
    /// the lock, so a slow render delays only the browsers of that view.
    /// When two browsers open the same view at once, both render, and
    /// the one that finishes second uses the task of the first.
    pub async fn subscribe(&self, view: K) -> watch::Receiver<Arc<str>> {
        if let Some(published) = self.open.lock().unwrap().get(&view) {
            return published.subscribe();
        }
        // Subscribe before the first render, so a change that arrives
        // while it runs makes the task render again.
        let changes = self.changes.subscribe();
        // When the first render fails, the view starts with an empty
        // rendering, and the event stream sends no empty rendering.
        let first = (self.render)(view.clone()).await.unwrap_or_default();
        let mut open = self.open.lock().unwrap();
        if let Some(published) = open.get(&view) {
            return published.subscribe();
        }
        let (published, latest) = watch::channel(Arc::from(first));
        let published = Arc::new(published);
        open.insert(view.clone(), published.clone());
        tokio::spawn(self.clone().run(view, published, changes));
        latest
    }

    /// How many views have a task running.
    #[cfg(test)]
    fn len(&self) -> usize {
        self.open.lock().unwrap().len()
    }

    async fn run(self, view: K, published: Published, mut changes: watch::Receiver<()>) {
        loop {
            tokio::select! {
                settled = settle(&mut changes, self.quiet, self.cap) => {
                    if settled.is_err() {
                        break;
                    }
                    self.publish(&view, &published).await;
                }
                () = published.closed() => {
                    if self.close(&view, &published) {
                        break;
                    }
                    // A browser subscribed before `close` took
                    // the lock.  The dropped `settle` future may have
                    // received a change already, so render now.
                    self.publish(&view, &published).await;
                }
            }
        }
    }

    /// Publishes a rendering only when it differs from the last one, so
    /// a change that the view does not show, such as a new `Deployment`
    /// revision with the same replica count, sends nothing to browsers.
    async fn publish(&self, view: &K, published: &watch::Sender<Arc<str>>) {
        let Some(html) = (self.render)(view.clone()).await else {
            return;
        };
        published.send_if_modified(|current| {
            let differs = **current != *html;
            if differs {
                *current = html.into();
            }
            differs
        });
    }

    /// Removes the view and returns true when no browser has it open.  A
    /// browser can subscribe after `closed()` resolves and before this
    /// takes the lock, so the receiver count is read again under the
    /// lock, and the task keeps running when it is not zero.
    fn close(&self, view: &K, published: &Published) -> bool {
        let mut open = self.open.lock().unwrap();
        if published.receiver_count() > 0 {
            return false;
        }
        if open
            .get(view)
            .is_some_and(|entry| Arc::ptr_eq(entry, published))
        {
            open.remove(view);
        }
        true
    }
}

#[cfg(test)]
mod tests {
    use rstest::{fixture, rstest};

    use super::*;

    const QUIET: Duration = Duration::from_millis(500);
    const CAP: Duration = Duration::from_secs(2);

    /// A fake cluster: each view renders to its name and the current
    /// `text`, and `renders` counts the renders of each view.
    struct Cluster {
        text: Arc<Mutex<String>>,
        renders: Arc<Mutex<HashMap<String, usize>>>,
        changed: watch::Sender<()>,
        views: Views<String>,
    }

    impl Cluster {
        fn set(&self, text: &str) {
            *self.text.lock().unwrap() = text.into();
            self.changed.send_replace(());
        }

        fn touch(&self) {
            self.changed.send_replace(());
        }

        fn renders(&self, view: &str) -> usize {
            self.renders.lock().unwrap().get(view).copied().unwrap_or(0)
        }
    }

    #[fixture]
    fn cluster() -> Cluster {
        let text = Arc::new(Mutex::new(String::from("one")));
        let renders = Arc::new(Mutex::new(HashMap::new()));
        let (changed, _) = watch::channel(());
        let views = Views::new(changed.clone(), QUIET, CAP, {
            let (text, renders) = (text.clone(), renders.clone());
            move |view: String| {
                *renders.lock().unwrap().entry(view.clone()).or_default() += 1;
                let html = format!("{view}: {}", text.lock().unwrap());
                async move { Some(html) }
            }
        });
        Cluster {
            text,
            renders,
            changed,
            views,
        }
    }

    async fn wait(millis: u64) {
        tokio::time::sleep(Duration::from_millis(millis)).await;
    }

    #[rstest]
    #[tokio::test(start_paused = true)]
    async fn renders_a_view_when_its_first_browser_connects(cluster: Cluster) {
        let latest = cluster.views.subscribe("/".into()).await;
        assert_eq!(&**latest.borrow(), "/: one");
    }

    #[rstest]
    #[tokio::test(start_paused = true)]
    async fn publishes_a_change_once_it_settles(cluster: Cluster) {
        let mut latest = cluster.views.subscribe("/".into()).await;
        cluster.set("two");
        wait(400).await;
        assert!(!latest.has_changed().unwrap());
        wait(200).await;
        assert_eq!(&**latest.borrow_and_update(), "/: two");
    }

    #[rstest]
    #[tokio::test(start_paused = true)]
    async fn renders_a_burst_once(cluster: Cluster) {
        let _latest = cluster.views.subscribe("/".into()).await;
        for _ in 0..50 {
            cluster.set("two");
            wait(10).await;
        }
        wait(1000).await;
        assert_eq!(cluster.renders("/"), 2);
    }

    #[rstest]
    #[tokio::test(start_paused = true)]
    async fn skips_a_rendering_that_matches_the_last(cluster: Cluster) {
        let latest = cluster.views.subscribe("/".into()).await;
        cluster.touch();
        wait(1000).await;
        assert_eq!(cluster.renders("/"), 2);
        assert!(!latest.has_changed().unwrap());
    }

    #[rstest]
    #[tokio::test(start_paused = true)]
    async fn browsers_on_one_view_share_its_renders(cluster: Cluster) {
        let mut first = cluster.views.subscribe("/".into()).await;
        let mut second = cluster.views.subscribe("/".into()).await;
        cluster.set("two");
        wait(1000).await;
        assert_eq!(cluster.renders("/"), 2);
        assert_eq!(cluster.views.len(), 1);
        assert_eq!(&**first.borrow_and_update(), "/: two");
        assert_eq!(&**second.borrow_and_update(), "/: two");
    }

    #[rstest]
    #[tokio::test(start_paused = true)]
    async fn a_change_wakes_every_open_view(cluster: Cluster) {
        let mut plain = cluster.views.subscribe("/".into()).await;
        let mut selected = cluster.views.subscribe("/?select=shop".into()).await;
        cluster.set("two");
        wait(1000).await;
        assert_eq!(&**plain.borrow_and_update(), "/: two");
        assert_eq!(&**selected.borrow_and_update(), "/?select=shop: two");
    }

    #[rstest]
    #[tokio::test(start_paused = true)]
    async fn a_view_closes_when_its_last_browser_leaves(cluster: Cluster) {
        let first = cluster.views.subscribe("/".into()).await;
        let second = cluster.views.subscribe("/".into()).await;
        drop(first);
        wait(10).await;
        assert_eq!(cluster.views.len(), 1);
        drop(second);
        wait(10).await;
        assert_eq!(cluster.views.len(), 0);
        cluster.set("two");
        wait(1000).await;
        assert_eq!(cluster.renders("/"), 1);
    }

    #[rstest]
    #[tokio::test(start_paused = true)]
    async fn a_view_opened_again_renders_afresh(cluster: Cluster) {
        drop(cluster.views.subscribe("/".into()).await);
        wait(10).await;
        cluster.set("two");
        wait(1000).await;
        let latest = cluster.views.subscribe("/".into()).await;
        assert_eq!(&**latest.borrow(), "/: two");
        assert_eq!(cluster.renders("/"), 2);
    }
}
