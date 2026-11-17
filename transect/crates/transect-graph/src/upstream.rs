//! How `transect` connects to a store it reads from: Prometheus, an
//! Alertmanager, or a log store.  A resource that declares one gives
//! its `url`, its `service`, or both, and a flag can name a URL in
//! their place.  The flag takes precedence over `url`, and `url` over
//! `service`, so one resource serves a workstation through a
//! port-forward and the cluster through the `Service`'s DNS name.

use serde::Deserialize;

/// A `Service` in the cluster that serves the store.
#[derive(Clone, Debug, PartialEq, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Service {
    pub namespace: String,
    pub name: String,
    pub port: u16,
}

/// `value` as a base URL: plain `http` or `https`, with no trailing
/// slash.
pub fn base_url(value: &str) -> Result<String, String> {
    http_base(value).ok_or_else(|| format!("a store's url is http or https, not {value:?}"))
}

/// `value`, trimmed, as an `http` or `https` URL with a host and no
/// trailing slash, or none when it is not one.
pub fn http_base(value: &str) -> Option<String> {
    let value = value.trim();
    let plain = value.starts_with("https://") || value.starts_with("http://");
    let host = value
        .split_once("://")
        .map(|(_, rest)| rest)
        .unwrap_or_default();
    if !plain || host.is_empty() || host.contains(char::is_whitespace) {
        return None;
    }
    Some(value.trim_end_matches('/').into())
}

/// The base URL to reach a store at: `flag`, then `url`, then
/// `service`'s DNS name in the cluster's `domain`, or none of them.
pub fn base(
    flag: Option<&str>,
    url: Option<&str>,
    service: Option<&Service>,
    domain: &str,
) -> Option<String> {
    let given = flag
        .or(url)
        .map(|url| url.trim_end_matches('/').to_string());
    given.or_else(|| {
        let Service {
            namespace,
            name,
            port,
        } = service?;
        Some(format!("http://{name}.{namespace}.svc.{domain}:{port}"))
    })
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn service() -> Service {
        Service {
            namespace: "monitoring".into(),
            name: "prometheus".into(),
            port: 9090,
        }
    }

    #[rstest]
    #[case::the_flag_wins(
        Some("http://localhost:1"),
        Some("http://localhost:2"),
        Some("http://localhost:1")
    )]
    #[case::then_the_url(None, Some("http://localhost:2/"), Some("http://localhost:2"))]
    #[case::then_the_service(
        None,
        None,
        Some("http://prometheus.monitoring.svc.cluster.local:9090")
    )]
    fn reaches_by_the_flag_then_the_url_then_the_service(
        #[case] flag: Option<&str>,
        #[case] url: Option<&str>,
        #[case] base_url: Option<&str>,
    ) {
        let service = service();
        let found = base(flag, url, Some(&service), "cluster.local");
        assert_eq!(found.as_deref(), base_url);
    }

    #[rstest]
    fn reaches_nothing_with_nothing_declared() {
        assert_eq!(base(None, None, None, "cluster.local"), None);
    }

    #[rstest]
    #[case::http("http://localhost:9090/", Ok("http://localhost:9090".to_string()))]
    #[case::https("https://metrics.example.test", Ok("https://metrics.example.test".to_string()))]
    #[case::another_scheme("ftp://metrics.example.test", Err(()))]
    fn reads_a_base_url(#[case] value: &str, #[case] expected: Result<String, ()>) {
        assert_eq!(base_url(value).map_err(|_| ()), expected);
    }
}
