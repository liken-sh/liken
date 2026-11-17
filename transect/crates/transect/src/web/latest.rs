//! The model of the cluster as of its latest change, built once per
//! change and shared by every view that renders after it.
//!
//! Every open view wakes on the same change, and every page load asks
//! for the same cluster, so building the model for each of them would
//! repeat the same work once per view.  The first caller after a change
//! builds it, on a blocking thread since it is CPU work, and callers
//! that arrive meanwhile wait for that build instead of starting their
//! own.

use std::sync::Arc;
use std::time::Instant;

use tokio::sync::{Mutex, watch};

/// Builds a model, given the one before it, which a build may copy
/// values from, such as the pods that have since gone.
type Build<T> = dyn Fn(Option<&T>) -> T + Send + Sync;

pub struct Latest<T> {
    build: Arc<Build<T>>,
    state: Arc<Mutex<State<T>>>,
    /// The last model built, which a caller that can take a model from
    /// before the latest change reads without waiting for a build.  An
    /// open page's event stream builds after each change, so it is
    /// seldom more than a few seconds old.
    last: Arc<std::sync::RwLock<Option<Arc<T>>>>,
}

struct State<T> {
    /// Marked changed when the cluster changed since the last build.
    changes: watch::Receiver<()>,
    built: Option<Arc<T>>,
}

impl<T> Clone for Latest<T> {
    fn clone(&self) -> Self {
        Self {
            build: self.build.clone(),
            state: self.state.clone(),
            last: self.last.clone(),
        }
    }
}

impl<T: Send + Sync + 'static> Latest<T> {
    /// `changes` marks the model stale; `build` makes a fresh one from
    /// the last one built, or from none on the first build.
    pub fn new(
        changes: &watch::Sender<()>,
        build: impl Fn(Option<&T>) -> T + Send + Sync + 'static,
    ) -> Self {
        let state = State {
            changes: changes.subscribe(),
            built: None,
        };
        Self {
            build: Arc::new(build),
            state: Arc::new(Mutex::new(state)),
            last: Arc::default(),
        }
    }

    /// The last model built, without waiting for a build after a change,
    /// or the latest when none has been built yet.  Page loads and quick
    /// search read this, because on a cluster that changes every second,
    /// waiting for each build would delay every click and keystroke by
    /// the length of one.  When the cluster has changed since, it starts
    /// a build on its own task, so the next keystroke reads a newer model
    /// even on a page with no event stream to build one.
    pub async fn last(&self) -> Option<Arc<T>> {
        let last = self.last.read().unwrap_or_else(|e| e.into_inner()).clone();
        let Some(last) = last else {
            return self.get().await;
        };
        if self.stale() {
            let latest = self.clone();
            tokio::spawn(async move { latest.get().await });
        }
        Some(last)
    }

    /// Whether a model newer than the last one built is on its way or
    /// due: a build is under way, or the cluster changed since the last.
    pub fn behind(&self) -> bool {
        match self.state.try_lock() {
            Ok(state) => state.changes.has_changed().unwrap_or(false),
            Err(_) => true,
        }
    }

    /// Whether the cluster changed since the last build, and no build
    /// is under way.  A build holds the lock, and the next one after it
    /// reads any change that landed meanwhile.
    fn stale(&self) -> bool {
        let Ok(state) = self.state.try_lock() else {
            return false;
        };
        state.changes.has_changed().unwrap_or(false)
    }

    /// The model as of the latest change, or `None` when building it
    /// panicked.
    pub async fn get(&self) -> Option<Arc<T>> {
        let mut state = self.state.lock().await;
        let changed = state.changes.has_changed().unwrap_or(false);
        if let (Some(built), false) = (&state.built, changed) {
            return Some(built.clone());
        }
        // Marked before the build, so a change that lands during it makes
        // the next caller build again.
        state.changes.mark_unchanged();
        let build = self.build.clone();
        let previous = state.built.clone();
        let built = tokio::task::spawn_blocking(move || {
            let started = Instant::now();
            let built = build(previous.as_deref());
            tracing::debug!(elapsed = ?started.elapsed(), "built the model");
            built
        })
        .await
        .inspect_err(|error| tracing::error!(%error, "building the model"))
        .ok()?;
        let built = Arc::new(built);
        state.built = Some(built.clone());
        *self.last.write().unwrap_or_else(|e| e.into_inner()) = Some(built.clone());
        Some(built)
    }
}

#[cfg(test)]
mod tests {
    use std::sync::atomic::{AtomicUsize, Ordering};

    use rstest::rstest;

    use super::*;

    #[rstest]
    #[case::nothing_changed(false, false)]
    #[case::the_cluster_changed(true, true)]
    #[tokio::test]
    async fn is_behind_only_when_the_cluster_changed_since_the_last_build(
        #[case] change: bool,
        #[case] behind: bool,
    ) {
        let (changes, latest) = counting();
        latest.get().await;
        if change {
            changes.send_replace(());
        }
        assert_eq!(latest.behind(), behind);
    }

    /// A model that is the number of builds so far, counting this one.
    fn counting() -> (watch::Sender<()>, Latest<usize>) {
        let (changes, _) = watch::channel(());
        let builds = AtomicUsize::new(0);
        let latest = Latest::new(&changes, move |_| builds.fetch_add(1, Ordering::SeqCst) + 1);
        (changes, latest)
    }

    #[rstest]
    #[case::once_for_callers_with_no_change_between(0, 1)]
    #[case::again_after_a_change(1, 2)]
    #[tokio::test]
    async fn builds_once_per_change(#[case] changes: usize, #[case] builds: usize) {
        let (sender, latest) = counting();
        latest.get().await;
        for _ in 0..changes {
            sender.send_replace(());
        }
        assert_eq!(*latest.get().await.unwrap(), builds);
    }

    #[rstest]
    #[tokio::test]
    async fn the_last_model_does_not_wait_for_a_change() {
        let (sender, latest) = counting();
        let first = latest.last().await;
        sender.send_replace(());
        let again = latest.last().await;
        assert_eq!((*first.unwrap(), *again.unwrap()), (1, 1));
    }

    #[rstest]
    #[tokio::test]
    async fn the_last_model_starts_a_build_after_a_change() {
        let (sender, latest) = counting();
        latest.last().await;
        sender.send_replace(());
        let stale = latest.last().await;
        // The build runs on its own task: let it take the lock, then
        // wait for it to release the lock.
        tokio::task::yield_now().await;
        drop(latest.state.lock().await);
        let fresh = latest.last().await;
        assert_eq!((*stale.unwrap(), *fresh.unwrap()), (1, 2));
    }

    #[rstest]
    #[tokio::test]
    async fn callers_together_share_one_build() {
        let (_sender, latest) = counting();
        let (first, second) = tokio::join!(latest.get(), latest.get());
        assert_eq!((*first.unwrap(), *second.unwrap()), (1, 1));
    }

    #[rstest]
    #[tokio::test]
    async fn a_build_reads_the_model_before_it() {
        let (changes, _) = watch::channel(());
        let latest = Latest::new(&changes, |previous: Option<&Vec<usize>>| {
            let mut models = previous.cloned().unwrap_or_default();
            models.push(models.len());
            models
        });
        latest.get().await;
        changes.send_replace(());
        assert_eq!(*latest.get().await.unwrap(), vec![0, 1]);
    }
}
