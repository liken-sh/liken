//! Reads the host name of Prometheus at an interval, so the loss can
//! show whether it includes the machine that Prometheus runs on.  When
//! a read fails, or no Prometheus is configured, the poll keeps the last
//! name it read, because Prometheus rarely moves, and a loss requested
//! while Prometheus is unreachable still needs the name.

use std::time::Duration;

use tokio::sync::watch;
use tokio::time::MissedTickBehavior;

use transect_observe::metrics::Reaching;

/// Reads the host name of the Prometheus that `reaching` returns, at
/// once and then every `interval`, and sends on `changed` when the name
/// differs from the last one read.
pub fn poll(
    reaching: Reaching,
    interval: Duration,
    changed: watch::Sender<()>,
) -> watch::Receiver<Option<String>> {
    let (name, latest) = watch::channel(None);
    tokio::spawn(async move {
        let mut ticks = tokio::time::interval(interval);
        ticks.set_missed_tick_behavior(MissedTickBehavior::Delay);
        loop {
            ticks.tick().await;
            let Some(prometheus) = reaching.prometheus() else {
                continue;
            };
            let Ok(read) = prometheus.hostname().await else {
                continue;
            };
            let differs = name.send_if_modified(|current| {
                let differs = current.as_deref() != Some(read.as_str());
                if differs {
                    *current = Some(read);
                }
                differs
            });
            if differs {
                changed.send_replace(());
            }
        }
    });
    latest
}
