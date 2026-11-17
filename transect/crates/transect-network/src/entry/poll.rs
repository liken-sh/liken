//! Reads each ingress controller's traffic from Prometheus on an
//! interval, with the queries that its `IngressController` declares,
//! and publishes the latest reading on a watch channel that each
//! render reads.  When a reading differs from the last one, the task
//! signals that the cluster changed, so open pages show the new
//! figures.

use std::sync::Arc;
use std::time::Duration;

use tokio::sync::watch;
use tokio::time::MissedTickBehavior;

use super::IngressController;
use super::traffic::{Measured, measure};
use transect_observe::metrics::Reaching;
use transect_observe::prometheus::QueryError;
use transect_resources::resources::Field;

/// The results of every controller's queries in the last round.
pub type Reading = Arc<Vec<Measured>>;

/// Queries the Prometheus that `reaching` returns, for each of
/// `controllers` that declares `entry`, now and every `interval`.
/// With no Prometheus, the reading is empty.
pub fn poll(
    reaching: Reaching,
    controllers: Field<Vec<IngressController>>,
    interval: Duration,
    changed: watch::Sender<()>,
) -> watch::Receiver<Reading> {
    let (readings, latest) = watch::channel(Reading::default());
    tokio::spawn(async move {
        let mut ticks = tokio::time::interval(interval);
        ticks.set_missed_tick_behavior(MissedTickBehavior::Delay);
        loop {
            ticks.tick().await;
            let prometheus = reaching.prometheus();
            let controllers: Vec<IngressController> = match prometheus {
                Some(_) => controllers.read(),
                None => Vec::new(),
            };
            let asks = controllers.iter().filter_map(|controller| {
                let queries = controller.entry.clone()?;
                let prometheus = prometheus.as_ref()?;
                Some(async move {
                    let (requests, latency, errors) = tokio::join!(
                        prometheus.query(&queries.requests),
                        prometheus.query(&queries.latency),
                        prometheus.query(&queries.errors),
                    );
                    let answered = (|| -> Result<_, QueryError> {
                        Ok(measure(&queries.label, &requests?, &latency?, &errors?))
                    })();
                    match answered {
                        Ok(backends) => Some(Measured {
                            controller: controller.name.clone(),
                            queries,
                            backends,
                        }),
                        Err(error) => {
                            tracing::warn!(controller = %controller.name, %error, "an entry query failed");
                            None
                        }
                    }
                })
            });
            let found = futures::future::join_all(asks).await;
            let reading: Reading = Arc::new(found.into_iter().flatten().collect());
            let differs = readings.send_if_modified(|current| {
                let differs = **current != *reading;
                if differs {
                    *current = reading;
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
