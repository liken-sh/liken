//! Polls Prometheus for each ingress controller's traffic, with the
//! queries in its `IngressController`, and publishes the latest
//! reading on a watch channel for the pages to read. When a reading
//! differs from the previous one, the task also sends on `changed`, so
//! open pages refresh.

use std::sync::Arc;
use std::time::Duration;

use tokio::sync::watch;
use tokio::time::MissedTickBehavior;

use super::IngressController;
use super::traffic::{Measured, measure};
use transect_observe::metrics::Reaching;
use transect_observe::prometheus::QueryError;
use transect_resources::resources::Field;

/// The measurements of the latest poll, one for each controller whose
/// queries succeeded.
pub type Reading = Arc<Vec<Measured>>;

/// Starts the polling task and returns the receiver of its readings.
/// The task polls once at start and then every `interval`, and polls
/// only the controllers that have `entry` queries. A controller whose
/// query fails is left out of the reading. The reading is empty while
/// `reaching` finds no Prometheus.
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
            let controller_queries = controllers.iter().filter_map(|controller| {
                let queries = controller.entry.clone()?;
                let prometheus = prometheus.as_ref()?;
                Some(async move {
                    let (requests, latency, errors) = tokio::join!(
                        prometheus.query(&queries.requests),
                        prometheus.query(&queries.latency),
                        prometheus.query(&queries.errors),
                    );
                    let measurement = (|| -> Result<_, QueryError> {
                        Ok(measure(&queries.label, &requests?, &latency?, &errors?))
                    })();
                    match measurement {
                        Ok(backends) => Some(Measured {
                            controller: controller.name.clone(),
                            queries,
                            backends,
                        }),
                        Err(error) => {
                            tracing::warn!(controller = %controller.name, %error, "Prometheus returned an error for the traffic queries of an IngressController");
                            None
                        }
                    }
                })
            });
            let measured = futures::future::join_all(controller_queries).await;
            let reading: Reading = Arc::new(measured.into_iter().flatten().collect());
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
