//! Selects which Prometheus transect queries: the one that
//! `--prometheus` names, or else the one that the `MetricsSource` names
//! by its `url` or its `service`.  The source can change while transect
//! runs, so every poller selects the Prometheus again on each round.
//! The pollers share one client for as long as the address stays the
//! same.

use std::sync::{Arc, Mutex};

use super::MetricsSource;
use crate::prometheus::Prometheus;
use transect_graph::upstream;
use transect_resources::resources::Field;

#[derive(Clone)]
pub struct Reaching {
    /// `--prometheus`, which takes precedence over the source's address.
    flag: Option<String>,
    /// The cluster's DNS domain, at the end of a `Service`'s DNS name.
    domain: String,
    source: Field<Option<MetricsSource>>,
    /// The last client built.  `prometheus` reuses it while the address
    /// stays the same.
    client: Arc<Mutex<Option<Prometheus>>>,
}

impl Reaching {
    pub fn new(flag: Option<String>, source: Field<Option<MetricsSource>>, domain: String) -> Self {
        Self {
            flag,
            domain,
            source,
            client: Arc::default(),
        }
    }

    /// The watched source.  A poller waits on it, so the poller wakes
    /// when the resources change.
    pub fn resources(&self) -> Field<Option<MetricsSource>> {
        self.source.clone()
    }

    /// The `MetricsSource` as of now.
    pub fn source(&self) -> Option<MetricsSource> {
        self.source.read()
    }

    /// The base URL of the Prometheus to query now, or none when neither
    /// the flag nor a `MetricsSource` gives one.
    pub fn url(&self) -> Option<String> {
        let source = self.source.read();
        let source = source.as_ref();
        upstream::base(
            self.flag.as_deref(),
            source.and_then(|source| source.url.as_deref()),
            source.and_then(|source| source.service.as_ref()),
            &self.domain,
        )
    }

    /// The client for the Prometheus to query now, or none.
    pub fn prometheus(&self) -> Option<Prometheus> {
        let url = self.url()?;
        let mut client = self.client.lock().unwrap_or_else(|held| held.into_inner());
        if let Some(built) = client.as_ref().filter(|built| built.url() == url) {
            return Some(built.clone());
        }
        match Prometheus::new(&url) {
            Ok(built) => {
                *client = Some(built.clone());
                Some(built)
            }
            Err(error) => {
                tracing::warn!(%url, %error, "cannot build a client for Prometheus");
                None
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;
    use crate::metrics::source::fixtures::document;
    use transect_resources::resources::DocId;

    fn reaching(flag: Option<&str>, spec: &str) -> Reaching {
        let source = crate::metrics::source::parse(DocId::default(), document(spec)).unwrap();
        let source = Field::fixed(Some(source));
        Reaching::new(flag.map(Into::into), source, "cluster.local".into())
    }

    #[rstest]
    #[case::the_flag(
        Some("http://localhost:9090"),
        "{url: 'http://prometheus.example.test'}",
        Some("http://localhost:9090")
    )]
    #[case::the_url(
        None,
        "{url: 'http://prometheus.example.test'}",
        Some("http://prometheus.example.test")
    )]
    #[case::the_service(
        None,
        "{service: {namespace: monitoring, name: prometheus, port: 9090}}",
        Some("http://prometheus.monitoring.svc.cluster.local:9090")
    )]
    #[case::nothing(None, "{}", None)]
    fn asks_the_flags_prometheus_then_the_sources(
        #[case] flag: Option<&str>,
        #[case] spec: &str,
        #[case] url: Option<&str>,
    ) {
        assert_eq!(reaching(flag, spec).url().as_deref(), url);
    }
}
