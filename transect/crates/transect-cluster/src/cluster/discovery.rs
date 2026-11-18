//! Finds out in the background whether the cluster serves each optional
//! kind, such as the Dynamic Resource Allocation kinds, so that a slow
//! API server delays that kind and not the first page.
//!
//! A probe checks API discovery for the kind and lists one object of it.
//! A 404 or a permission error is final, and the kind stays off. Any
//! other error, such as a timeout from a slow or restarting API server,
//! tells nothing about the kind, so the probe retries with backoff.

use std::future::Future;
use std::hash::Hash;
use std::time::Duration;

use kube::runtime::reflector::{Lookup, Store};
use tokio::sync::watch;

/// The timeout for one probe, and for the first list of the kind's
/// stores. Both together stay under `SYNC_TIMEOUT` in the `transect`
/// binary, so a probe that hangs leaves its kind retrying instead of
/// failing startup.
const ATTEMPT: Duration = Duration::from_secs(10);

const FIRST_BACKOFF: Duration = Duration::from_secs(1);
const MAX_BACKOFF: Duration = Duration::from_secs(60);

/// Why a probe did not turn a kind on.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Refusal {
    /// The cluster does not serve the kind.
    NotServed,
    /// It does, but `transect` may not list it.
    Forbidden,
    /// The probe failed with an error that tells nothing about the kind.
    Transient(String),
}

impl From<kube::Error> for Refusal {
    fn from(error: kube::Error) -> Self {
        match &error {
            kube::Error::Api(status) if status.code == 404 => Self::NotServed,
            kube::Error::Api(status) if matches!(status.code, 401 | 403) => Self::Forbidden,
            kube::Error::Discovery(_) => Self::NotServed,
            _ => Self::Transient(error.to_string()),
        }
    }
}

/// The final results that leave a kind off.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Off {
    NotServed,
    Forbidden,
}

/// The state of one optional kind's discovery.
#[derive(Clone, Debug)]
pub enum State<T> {
    /// The first probe has not finished.
    Probing,
    /// A probe failed with a transient error, and another will follow.
    Retrying,
    Off(Off),
    /// The kind is on, with its stores.
    On(T),
}

/// Stores that can wait for their first full list.
pub trait Ready {
    fn ready(&self) -> impl Future<Output = ()> + Send;
}

impl<K> Ready for Store<K>
where
    K: Lookup + Clone + Send + Sync + 'static,
    K::DynamicType: Eq + Hash + Clone + Send + Sync,
{
    async fn ready(&self) {
        // An error means the writer is gone. The store will never fill,
        // so there is nothing to wait for.
        let _ = self.wait_until_ready().await;
    }
}

/// One optional kind, discovered in the background.
#[derive(Clone)]
pub struct Optional<T> {
    state: watch::Receiver<State<T>>,
}

impl<T: Ready + Clone + Send + Sync + 'static> Optional<T> {
    /// Retries `probe` in a background task until it has a final
    /// result. When the probe succeeds, it starts the kind's stores with
    /// `start` and sends on `changed` once they have their first list, so
    /// every open page draws the kind again. It also sends on `changed`
    /// after each transient failure.
    pub fn discover<P, F>(
        what: impl Into<String>,
        changed: &watch::Sender<()>,
        probe: impl Fn() -> F + Send + 'static,
        start: impl FnOnce(P) -> T + Send + 'static,
    ) -> Self
    where
        P: Send + 'static,
        F: Future<Output = Result<P, Refusal>> + Send,
    {
        let (sender, state) = watch::channel(State::Probing);
        let changed = changed.clone();
        let what = what.into();
        tokio::spawn(async move {
            let found = probe_until_final(&what, probe, FIRST_BACKOFF, || {
                sender.send_replace(State::Retrying);
                changed.send_replace(());
            })
            .await;
            let state = match found {
                Ok(probed) => {
                    let stores = start(probed);
                    if tokio::time::timeout(ATTEMPT, stores.ready()).await.is_err() {
                        tracing::warn!(%what, "The first list is slow, so the view shows what has arrived");
                    }
                    tracing::info!(%what, "Watching the kind");
                    State::On(stores)
                }
                Err(off) => {
                    tracing::info!(%what, ?off, "Not watching the kind");
                    State::Off(off)
                }
            };
            sender.send_replace(state);
            changed.send_replace(());
        });
        Self { state }
    }

    /// Resolves when the state leaves `Probing`: the kind is off, it is on
    /// and its stores have their first list, or the first probe failed
    /// transiently. A page drawn before then would show the kind as off
    /// when it may be on.
    pub async fn settled(&self) {
        let mut state = self.state.clone();
        // An error means the task ended, and it ends only after sending
        // a final state.
        let _ = state
            .wait_for(|state| !matches!(state, State::Probing))
            .await;
    }

    pub fn state(&self) -> State<T> {
        self.state.borrow().clone()
    }
}

/// Runs `probe` until it succeeds or fails with a final refusal. After
/// each transient failure it calls `retrying` and waits `backoff`, which
/// doubles after each wait up to `MAX_BACKOFF`.
async fn probe_until_final<P, F>(
    what: &str,
    probe: impl Fn() -> F,
    mut backoff: Duration,
    mut retrying: impl FnMut(),
) -> Result<P, Off>
where
    F: Future<Output = Result<P, Refusal>>,
{
    loop {
        let reason = match tokio::time::timeout(ATTEMPT, probe()).await {
            Ok(Ok(probed)) => return Ok(probed),
            Ok(Err(Refusal::NotServed)) => return Err(Off::NotServed),
            Ok(Err(Refusal::Forbidden)) => return Err(Off::Forbidden),
            Ok(Err(Refusal::Transient(reason))) => reason,
            Err(_) => format!("the check returned no result in {ATTEMPT:?}"),
        };
        tracing::warn!(%what, %reason, ?backoff, "Checking whether the cluster serves this kind returned an error, so the check runs again");
        retrying();
        tokio::time::sleep(backoff).await;
        backoff = (backoff * 2).min(MAX_BACKOFF);
    }
}

#[cfg(test)]
mod tests {
    use std::sync::Arc;
    use std::sync::atomic::{AtomicUsize, Ordering};

    use kube::core::Status;
    use rstest::rstest;

    use super::*;

    fn api(code: u16) -> kube::Error {
        kube::Error::Api(Box::new(
            Status::failure("refused", "Refused").with_code(code),
        ))
    }

    #[rstest]
    #[case::not_found(api(404), Refusal::NotServed)]
    #[case::forbidden(api(403), Refusal::Forbidden)]
    #[case::unauthorized(api(401), Refusal::Forbidden)]
    #[case::missing_from_discovery(
        kube::Error::Discovery(kube::error::DiscoveryError::MissingApiGroup("x".into())),
        Refusal::NotServed
    )]
    #[case::unavailable(api(503), Refusal::Transient(api(503).to_string()))]
    fn separates_final_refusals_from_transient_failures(
        #[case] error: kube::Error,
        #[case] refusal: Refusal,
    ) {
        assert_eq!(Refusal::from(error), refusal);
    }

    /// A probe that fails transiently `failures` times, then returns
    /// `then`, and a count of its attempts.
    fn flaky(
        failures: usize,
        then: Result<(), Refusal>,
    ) -> (
        Arc<AtomicUsize>,
        impl Fn() -> std::future::Ready<Result<(), Refusal>>,
    ) {
        let attempts = Arc::new(AtomicUsize::new(0));
        let counted = attempts.clone();
        let probe = move || {
            let attempt = counted.fetch_add(1, Ordering::SeqCst);
            let transient = Err(Refusal::Transient("unavailable".into()));
            std::future::ready(if attempt < failures {
                transient
            } else {
                then.clone()
            })
        };
        (attempts, probe)
    }

    #[rstest]
    #[case::at_once(0, Ok(()), Ok(()), 1)]
    #[case::after_two_failures(2, Ok(()), Ok(()), 3)]
    #[case::not_served_is_final(1, Err(Refusal::NotServed), Err(Off::NotServed), 2)]
    #[case::forbidden_is_final(1, Err(Refusal::Forbidden), Err(Off::Forbidden), 2)]
    #[tokio::test(start_paused = true)]
    async fn retries_transient_failures_until_a_final_result(
        #[case] failures: usize,
        #[case] then: Result<(), Refusal>,
        #[case] expected: Result<(), Off>,
        #[case] tries: usize,
    ) {
        let (attempts, probe) = flaky(failures, then);
        let found = probe_until_final("a kind", probe, FIRST_BACKOFF, || {}).await;
        assert_eq!((found, attempts.load(Ordering::SeqCst)), (expected, tries));
    }

    #[rstest]
    #[tokio::test(start_paused = true)]
    async fn waits_longer_after_each_failure() {
        let (_, probe) = flaky(8, Ok(()));
        let started = tokio::time::Instant::now();
        probe_until_final("a kind", probe, FIRST_BACKOFF, || {})
            .await
            .unwrap();
        // 1 + 2 + 4 + 8 + 16 + 32 + 60 + 60 seconds.
        assert_eq!(started.elapsed(), Duration::from_secs(183));
    }

    #[rstest]
    #[tokio::test(start_paused = true)]
    async fn a_probe_that_times_out_is_retried() {
        let attempts = Arc::new(AtomicUsize::new(0));
        let counted = attempts.clone();
        let probe = move || {
            let first = counted.fetch_add(1, Ordering::SeqCst) == 0;
            async move {
                if first {
                    std::future::pending::<()>().await;
                }
                Ok::<(), Refusal>(())
            }
        };
        probe_until_final("a kind", probe, FIRST_BACKOFF, || {})
            .await
            .unwrap();
        assert_eq!(attempts.load(Ordering::SeqCst), 2);
    }
}
