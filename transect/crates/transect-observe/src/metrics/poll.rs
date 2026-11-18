//! Reads the bytes used on each claim's volume with the `volumeUsed`
//! query that a `MetricsSource` declares, and keeps the latest reading in
//! a `watch` channel that each render reads.

use std::collections::HashMap;
use std::sync::Arc;
use std::time::Duration;

use tokio::sync::watch;
use tokio::time::MissedTickBehavior;

use super::Reaching;
use super::source::{ClaimLabels, ClaimQuery};
use crate::prometheus::{Prometheus, Sample};
use transect_graph::edges::Claim;

/// The longest that a failed query's error may be on a page, in
/// characters.
const ERROR_CHARS: usize = 200;

/// The result of the last `volumeUsed` query.
#[derive(Clone, Debug, Default, PartialEq)]
pub enum Reading {
    /// Neither `--prometheus` nor a `MetricsSource` gives a Prometheus.
    #[default]
    Off,
    /// No `MetricsSource` declares a `volumeUsed` query.
    Undeclared,
    /// The first query has not returned yet.
    Waiting,
    /// The query failed, with the error from Prometheus or from the
    /// network.
    Failed(String),
    /// Bytes used, by claim.
    Read(Arc<HashMap<Claim, f64>>),
}

impl Reading {
    /// The bytes used on `claim`'s volume, when the last result included
    /// them.
    pub fn used(&self, claim: &Claim) -> Option<f64> {
        match self {
            Self::Read(used) => used.get(claim).copied(),
            _ => None,
        }
    }

    /// The line that a volume page shows in place of its used bytes,
    /// with the text of `missing`.  When the reading is `Off` or
    /// `Undeclared` and a `MetricsSource` did not load, the line says so
    /// in place of that text.
    pub fn missing_line(
        &self,
        claim: &Claim,
        loaded: &transect_resources::resources::Directory,
    ) -> Option<transect_resources::resources::OffLine> {
        let missing = self.missing(claim)?;
        Some(match self {
            Self::Off | Self::Undeclared => loaded.off("MetricsSource", "Used bytes: ", &missing),
            _ => transect_resources::resources::OffLine::plain(missing),
        })
    }

    /// The gray line that a volume page shows in place of its used
    /// bytes, which says what would show them.  It is `None` when the
    /// reading has the bytes or the first query has not returned.
    pub fn missing(&self, claim: &Claim) -> Option<String> {
        match self {
            Self::Off => Some(
                "Used bytes come from Prometheus, and neither --prometheus nor a MetricsSource names one."
                    .into(),
            ),
            Self::Undeclared => Some(
                "A MetricsSource with a volumeUsed query would show how full it is.".into(),
            ),
            Self::Waiting => None,
            Self::Failed(why) => Some(format!("The MetricsSource's volumeUsed query failed: {why}")),
            Self::Read(used) if used.contains_key(claim) => None,
            Self::Read(_) => Some(
                "Prometheus has no series for this claim from the MetricsSource's volumeUsed query."
                    .into(),
            ),
        }
    }
}

/// Sends the `volumeUsed` query of the `MetricsSource` to the
/// Prometheus that `reaching` selects, at once, then every `interval`
/// and whenever the resources change.  It sends a reading to the
/// channel and marks `changed` only when the reading differs from the
/// one before, so an open volume page shows the new figure.
pub fn poll(
    reaching: Reaching,
    interval: Duration,
    changed: watch::Sender<()>,
) -> watch::Receiver<Reading> {
    let (readings, latest) = watch::channel(Reading::Waiting);
    tokio::spawn(async move {
        let mut ticks = tokio::time::interval(interval);
        ticks.set_missed_tick_behavior(MissedTickBehavior::Delay);
        let mut resources = reaching.resources();
        loop {
            // When the resources channel's sender is dropped, `changed`
            // returns an error and its arm does not match, so only the
            // tick wakes the loop.
            tokio::select! {
                _ = ticks.tick() => {}
                Ok(()) = resources.changed() => {}
            }
            resources.mark_unchanged();
            let query = reaching.source().and_then(|source| source.volume_used);
            let reading = match (reaching.prometheus(), query) {
                (None, _) => Reading::Off,
                (Some(_), None) => Reading::Undeclared,
                (Some(prometheus), Some(query)) => query_volumes(&prometheus, &query).await,
            };
            if readings.send_if_modified(|current| replace_if_differs(current, reading)) {
                changed.send_replace(());
            }
        }
    });
    latest
}

fn replace_if_differs(current: &mut Reading, next: Reading) -> bool {
    let differs = *current != next;
    if differs {
        if let Reading::Failed(why) = &next {
            tracing::warn!(error = %why, "The volumeUsed query of the MetricsSource failed");
        }
        *current = next;
    }
    differs
}

async fn query_volumes(prometheus: &Prometheus, query: &ClaimQuery) -> Reading {
    match prometheus.query(&query.query).await {
        Ok(samples) => Reading::Read(Arc::new(by_claim(&samples, &query.labels))),
        Err(error) => Reading::Failed(
            transect_graph::untrusted::capped(&error.to_string(), ERROR_CHARS).unwrap_or_default(),
        ),
    }
}

/// The bytes used on each claim's volume in `samples`, keyed by the
/// labels that name the claim.  The function leaves out a series that
/// lacks either label or whose value is not finite.  When two series
/// name one claim, the larger value counts, because a volume that is
/// mounted on two machines is reported from both.
pub fn by_claim(samples: &[Sample], labels: &ClaimLabels) -> HashMap<Claim, f64> {
    let mut used: HashMap<Claim, f64> = HashMap::new();
    for sample in samples.iter().filter(|sample| sample.value.is_finite()) {
        let (Some(namespace), Some(name)) = (
            sample.labels.get(&labels.namespace),
            sample.labels.get(&labels.claim),
        ) else {
            continue;
        };
        let bytes = used.entry(Claim::new(namespace, name)).or_insert(0.0);
        *bytes = bytes.max(sample.value);
    }
    used
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn sample(labels: &[(&str, &str)], value: f64) -> Sample {
        Sample {
            labels: labels
                .iter()
                .map(|(k, v)| (k.to_string(), v.to_string()))
                .collect(),
            value,
        }
    }

    fn labels() -> ClaimLabels {
        ClaimLabels {
            namespace: "namespace".into(),
            claim: "persistentvolumeclaim".into(),
        }
    }

    #[rstest]
    #[case::one(vec![sample(&[("namespace", "shop"), ("persistentvolumeclaim", "data")], 5.0)], Some(5.0))]
    #[case::the_larger_of_two(vec![
        sample(&[("namespace", "shop"), ("persistentvolumeclaim", "data")], 5.0),
        sample(&[("namespace", "shop"), ("persistentvolumeclaim", "data")], 7.0),
    ], Some(7.0))]
    #[case::no_claim_label(vec![sample(&[("namespace", "shop")], 5.0)], None)]
    #[case::not_a_number(vec![sample(&[("namespace", "shop"), ("persistentvolumeclaim", "data")], f64::NAN)], None)]
    fn reads_each_claims_bytes(#[case] samples: Vec<Sample>, #[case] bytes: Option<f64>) {
        let used = by_claim(&samples, &labels());
        assert_eq!(used.get(&Claim::new("shop", "data")).copied(), bytes);
    }

    fn read(bytes: f64) -> Reading {
        let used = HashMap::from([(Claim::new("shop", "data"), bytes)]);
        Reading::Read(Arc::new(used))
    }

    #[rstest]
    #[case::read(read(5.0))]
    #[case::waiting(Reading::Waiting)]
    fn says_nothing_when_it_has_the_bytes_or_waits(#[case] reading: Reading) {
        assert_eq!(reading.missing(&Claim::new("shop", "data")), None);
    }

    #[rstest]
    #[case::no_series(read(5.0), "has no series")]
    #[case::off(Reading::Off, "neither --prometheus nor a MetricsSource")]
    #[case::undeclared(Reading::Undeclared, "would show how full")]
    #[case::failed(Reading::Failed("bad query".into()), "failed: bad query")]
    fn says_what_would_show_used_bytes(#[case] reading: Reading, #[case] says: &str) {
        let line = reading.missing(&Claim::new("shop", "other")).unwrap();
        assert!(line.contains(says), "{line:?} does not say {says:?}");
    }
}
