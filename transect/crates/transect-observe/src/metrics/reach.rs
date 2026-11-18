//! Chooses which Prometheus transect queries: the address in
//! `--prometheus`, or else the address that the `MetricsSource` gives
//! in its `url` or `service`. The `MetricsSource` can change while
//! transect runs, so each poller chooses again on every round. The
//! pollers share one client until the address changes.

use std::sync::{Arc, Mutex};

use super::MetricsSource;
use crate::prometheus::Prometheus;
use transect_graph::upstream;
use transect_resources::resources::Field;

#[derive(Clone)]
pub struct Reaching {
    /// The `--prometheus` address. It overrides the source's address.
    flag: Option<String>,
    /// The cluster's DNS domain, the suffix of a `Service`'s DNS name.
    domain: String,
    source: Field<Option<MetricsSource>>,
    /// The last client built. `prometheus` returns it while the address
    /// is unchanged.
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

    /// The watched `MetricsSource` field. A poller waits on it, so the
    /// poller runs again when the resource changes.
    pub fn resources(&self) -> Field<Option<MetricsSource>> {
        self.source.clone()
    }

    pub fn source(&self) -> Option<MetricsSource> {
        self.source.read()
    }

    /// The base URL of the Prometheus to query, or `None` when neither
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

    /// A client for the Prometheus at `url()`, or `None` when there is
    /// no address or the client cannot be built.
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
                tracing::warn!(%url, %error, "The Prometheus client was not built");
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
    fn the_flag_overrides_the_source_address(
        #[case] flag: Option<&str>,
        #[case] spec: &str,
        #[case] url: Option<&str>,
    ) {
        assert_eq!(reaching(flag, spec).url().as_deref(), url);
    }
}
