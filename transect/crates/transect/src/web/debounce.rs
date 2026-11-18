//! Waits for a burst of changes to finish. A rollout or a namespace
//! deletion arrives as dozens of watch events within a second, and
//! rendering after each one would push dozens of nearly identical
//! pages.

use std::time::Duration;

use tokio::sync::watch;
use tokio::time::{Instant, timeout_at};

/// How long the cluster must stay quiet before the map renders.
pub const QUIET: Duration = Duration::from_millis(500);

/// The longest a render waits after the first change of a burst. Without
/// this limit, a cluster that changes every few hundred milliseconds
/// would never go quiet, and the open page would never update.
pub const CAP: Duration = Duration::from_secs(2);

/// Waits for a change, then returns when `quiet` has passed with no
/// further change, or when `cap` has passed since that first change,
/// whichever comes first. Returns an error when every sender has been
/// dropped, because no further change can arrive.
pub async fn settle(
    changes: &mut watch::Receiver<()>,
    quiet: Duration,
    cap: Duration,
) -> Result<(), watch::error::RecvError> {
    changes.changed().await?;
    let deadline = Instant::now() + cap;
    while Instant::now() < deadline {
        let until = (Instant::now() + quiet).min(deadline);
        match timeout_at(until, changes.changed()).await {
            Ok(result) => result?,
            Err(_elapsed) => return Ok(()),
        }
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    /// Sends a change at each offset from now, in milliseconds, and
    /// returns how many milliseconds `settle` took.
    async fn settle_after(offsets: &[u64]) -> u64 {
        let (sender, mut receiver) = watch::channel(());
        let start = Instant::now();
        let offsets = offsets.to_vec();
        tokio::spawn(async move {
            for offset in offsets {
                tokio::time::sleep_until(start + Duration::from_millis(offset)).await;
                sender.send_replace(());
            }
            // Keep the sender alive, so that `settle` ends on its timers
            // and not because the channel closed.
            std::future::pending::<()>().await;
        });
        settle(&mut receiver, QUIET, CAP).await.unwrap();
        start.elapsed().as_millis() as u64
    }

    #[rstest]
    #[case::one_change(&[100], 600)]
    #[case::a_burst(&[100, 150, 200], 700)]
    #[case::a_late_second_change(&[100, 550], 1050)]
    #[case::a_change_after_quiet_waits_for_the_next_settle(&[100, 700], 600)]
    #[case::a_steady_stream_is_capped(&[0, 300, 600, 900, 1200, 1500, 1800, 2100, 2400], 2000)]
    #[tokio::test(start_paused = true)]
    async fn renders_after_quiet_or_the_cap(#[case] offsets: &[u64], #[case] elapsed: u64) {
        assert_eq!(settle_after(offsets).await, elapsed);
    }

    #[tokio::test(start_paused = true)]
    async fn ends_when_every_sender_is_gone() {
        let (sender, mut receiver) = watch::channel(());
        drop(sender);
        assert!(settle(&mut receiver, QUIET, CAP).await.is_err());
    }
}
