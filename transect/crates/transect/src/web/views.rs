//! The per-view registry: one render task for each view that has a
//! browser connected, shared by every browser on that view.
//!
//! Each URL renders differently, so browsers share a rendering only
//! when they share a view.  Each task renders its view once when its
//! first browser connects, then again after each settled change, and
//! publishes each rendering on a `watch` channel that its browsers
//! read.  So the cost of a change is one render per open view however
//! many browsers share it, and a browser that falls behind skips
//! straight to the newest rendering instead of replaying the ones it
//! missed.  A task ends when its last browser leaves, so a view nobody
//! looks at costs nothing.

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
    /// `changes` wakes every open view; `render` returns `None` when
    /// rendering fails, and the last good rendering stays published.  A
    /// view whose object is gone renders the page that reports it gone,
    /// which is a rendering like any other.
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

    /// The latest rendering of `view`, starting its task when no browser
    /// has it open.  The first rendering happens outside the lock, so a
    /// slow render holds up only the browsers of its own view; two
    /// browsers arriving together may both render, and the second to
    /// finish joins the first one's task instead of starting another.
    pub async fn subscribe(&self, view: K) -> watch::Receiver<Arc<str>> {
        if let Some(published) = self.open.lock().unwrap().get(&view) {
            return published.subscribe();
        }
        // Subscribing before the first render, not in the task, counts a
        // change that lands before the task first runs.
        let changes = self.changes.subscribe();
        // A first render that fails publishes an empty rendering, which
        // the event stream does not send.
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
                    // The browser that kept the view open may have
                    // cancelled a settle that had received a change, so
                    // render now rather than wait for the next one.
                    self.publish(&view, &published).await;
                }
            }
        }
    }

    /// Publishes a rendering only when it differs from the last one.  A
    /// change the view does not draw, such as a new `Deployment` revision
    /// with the same count, renders to the same HTML and is sent to no
    /// browser.
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

    /// Removes the view once its last browser has left.  A browser can
    /// subscribe between `closed()` resolving and this lock, so the count
    /// is read again under the lock.  When a browser has subscribed, the
    /// task keeps running.
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

    /// A stand-in for the cluster: each view renders to its name and the
    /// cluster's current text, and every render is counted by view.
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
