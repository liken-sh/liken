//! Finding the optional kinds a cluster serves, such as Dynamic
//! Resource Allocation's, without holding up the map.
//!
//! Each kind is probed in the background: discovery shows whether the
//! cluster serves it, and a list of one object shows whether `transect`
//! may list it.  Either answer is final.  Any other result, such as from
//! an API server that is slow, restarting, or briefly unreachable, gives
//! no answer about the kind, so the probe is tried again with backoff
//! until it gets an answer, and the kind turns on when it does.  Every
//! attempt has a timeout, so a slow API server delays a kind, never
//! the first page.

use std::future::Future;
use std::hash::Hash;
use std::time::Duration;

use kube::runtime::reflector::{Lookup, Store};
use tokio::sync::watch;

/// How long one probe, or one store's first list, may take.  It is
/// within the cluster's sync timeout, so a first probe that hangs
/// leaves its kind retrying instead of failing the start.
const ATTEMPT: Duration = Duration::from_secs(10);

/// The first wait between failed probes, doubled after each to
/// `MAX_BACKOFF`.
const FIRST_BACKOFF: Duration = Duration::from_secs(1);
const MAX_BACKOFF: Duration = Duration::from_secs(60);

/// Why a probe did not turn a kind on.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Refusal {
    /// The cluster does not serve the kind.
    NotServed,
    /// It does, but `transect` may not list it.
    Forbidden,
    /// The probe failed for a reason that gives no answer about the kind.
    Transient(String),
}

impl From<kube::Error> for Refusal {
    /// A 404 means the API serves no such group, version, or resource,
    /// and discovery's own errors mean a group or kind is missing from
    /// what the API serves.  A 401 or 403 means `transect` may not list
    /// it.
    fn from(error: kube::Error) -> Self {
        match &error {
            kube::Error::Api(status) if status.code == 404 => Self::NotServed,
            kube::Error::Api(status) if matches!(status.code, 401 | 403) => Self::Forbidden,
            kube::Error::Discovery(_) => Self::NotServed,
            _ => Self::Transient(error.to_string()),
        }
    }
}

/// A final answer that leaves a kind off.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Off {
    NotServed,
    Forbidden,
}

/// The state of one optional kind's discovery.
#[derive(Clone, Debug)]
pub enum State<T> {
    /// The first probe has not answered.
    Probing,
    /// A probe failed for a transient reason, and another will follow.
    Retrying,
    Off(Off),
    /// The kind is on, with its stores.
    On(T),
}

/// Stores that can report when they hold their first full list.
pub trait Ready {
    fn ready(&self) -> impl Future<Output = ()> + Send;
}

impl<K> Ready for Store<K>
where
    K: Lookup + Clone + Send + Sync + 'static,
    K::DynamicType: Eq + Hash + Clone + Send + Sync,
{
    async fn ready(&self) {
        // An error means the writer is gone, so the store will never
        // fill and there is nothing to wait for.
        let _ = self.wait_until_ready().await;
    }
}

/// One optional kind, found in the background.
#[derive(Clone)]
pub struct Optional<T> {
    state: watch::Receiver<State<T>>,
}

impl<T: Ready + Clone + Send + Sync + 'static> Optional<T> {
    /// Starts probing with `probe` until it answers, then starts the
    /// kind's stores with `start` and marks `changed` once they hold
    /// their first list, so every open view renders the kind.  `start`
    /// runs once, after a probe that succeeded, so a failed probe
    /// leaves no watch behind.
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
            let found = answer(&what, probe, FIRST_BACKOFF, || {
                sender.send_replace(State::Retrying);
                changed.send_replace(());
            })
            .await;
            let state = match found {
                Ok(probed) => {
                    let stores = start(probed);
                    if tokio::time::timeout(ATTEMPT, stores.ready()).await.is_err() {
                        tracing::warn!(%what, "the first list is slow; showing what has arrived");
                    }
                    tracing::info!(%what, "watching");
                    State::On(stores)
                }
                Err(off) => {
                    tracing::info!(%what, ?off, "not watching");
                    State::Off(off)
                }
            };
            sender.send_replace(state);
            changed.send_replace(());
        });
        Self { state }
    }

    /// Resolves once the first probe has answered, or failed and left
    /// the kind retrying.  A page drawn before then would show the kind
    /// off for no reason.
    pub async fn settled(&self) {
        let mut state = self.state.clone();
        // An error means the task ended, which it does only after
        // sending a final state.
        let _ = state
            .wait_for(|state| !matches!(state, State::Probing))
            .await;
    }

    /// The kind's state as of now.
    pub fn state(&self) -> State<T> {
        self.state.borrow().clone()
    }
}

/// Probes until `probe` answers, waiting `backoff`, then twice as long
/// each time, up to `MAX_BACKOFF`, after each transient failure.  Calls
/// `retrying` after each one, so the caller can report the retry.
async fn answer<P, F>(
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
            Err(_) => format!("no answer in {ATTEMPT:?}"),
        };
        tracing::warn!(%what, %reason, ?backoff, "cannot find whether the cluster serves this; retrying");
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
    fn tells_an_answer_from_a_transient_failure(
        #[case] error: kube::Error,
        #[case] refusal: Refusal,
    ) {
        assert_eq!(Refusal::from(error), refusal);
    }

    /// A probe that fails transiently `failures` times, then gives
    /// `then`, counting every attempt.
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
    async fn retries_until_it_has_an_answer(
        #[case] failures: usize,
        #[case] then: Result<(), Refusal>,
        #[case] answered: Result<(), Off>,
        #[case] tries: usize,
    ) {
        let (attempts, probe) = flaky(failures, then);
        let found = answer("a kind", probe, FIRST_BACKOFF, || {}).await;
        assert_eq!((found, attempts.load(Ordering::SeqCst)), (answered, tries));
    }

    #[rstest]
    #[tokio::test(start_paused = true)]
    async fn waits_longer_after_each_failure() {
        let (_, probe) = flaky(8, Ok(()));
        let started = tokio::time::Instant::now();
        answer("a kind", probe, FIRST_BACKOFF, || {}).await.unwrap();
        // 1 + 2 + 4 + 8 + 16 + 32 + 60 + 60 seconds.
        assert_eq!(started.elapsed(), Duration::from_secs(183));
    }

    #[rstest]
    #[tokio::test(start_paused = true)]
    async fn a_probe_with_no_answer_is_retried() {
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
        answer("a kind", probe, FIRST_BACKOFF, || {}).await.unwrap();
        assert_eq!(attempts.load(Ordering::SeqCst), 2);
    }
}
