//! The Prometheus `/metrics` listener of the idle client. It serves the
//! build info, the `process_*` metrics, and the `media_bus_connected`
//! gauge that [`media_screen::reader`] sets. It reports no mpv metrics,
//! because this client does not connect to mpv.

use std::net::SocketAddr;
use std::time::Duration;

use metrics_exporter_prometheus::PrometheusBuilder;
use metrics_process::Collector;

/// How often the collector reads `/proc`. The exporter has no hook that
/// runs on each scrape, so a separate thread refreshes the `process_*`
/// metrics on this interval.
const COLLECTION_INTERVAL: Duration = Duration::from_secs(10);

/// Parses `MEDIA_METRICS_ADDRESS`. An empty or invalid value gives
/// `None`, and no listener starts. The metric calls elsewhere still run
/// and record nothing.
pub fn address(raw: &str) -> Option<SocketAddr> {
    let raw = raw.trim();
    if raw.is_empty() {
        return None;
    }
    raw.parse().ok()
}

/// Starts the exporter on `address`, records the build info, and starts
/// the thread that refreshes `process_*`. If the exporter fails to
/// start, this prints an error and returns, because the idle screen
/// must keep drawing without metrics.
pub fn serve(address: SocketAddr, component: &str, version: &str) {
    if let Err(error) = PrometheusBuilder::new()
        .with_http_listener(address)
        .install()
    {
        eprintln!("idle-screen: starting the metrics exporter: {error}");
        return;
    }
    media_screen::metrics::build_info(component, version);
    // Set before the bus reader opens, so a scrape before the first
    // session reads 0 and not a missing series.
    media_screen::metrics::bus_connected(false);

    let collector = Collector::default();
    collector.describe();
    std::thread::spawn(move || {
        loop {
            collector.collect();
            std::thread::sleep(COLLECTION_INTERVAL);
        }
    });
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn an_empty_address_disables_the_listener() {
        assert_eq!(address(""), None);
        assert_eq!(address("   "), None);
    }

    #[test]
    fn an_address_this_client_cannot_read_disables_the_listener() {
        assert_eq!(address("not-an-address"), None);
    }

    #[test]
    fn a_host_and_a_port_parse() {
        assert_eq!(address("0.0.0.0:9200"), Some(([0, 0, 0, 0], 9200).into()));
    }

    // The global recorder can be installed once per process, so this is
    // the only test that calls `serve`. The first call installs it. The
    // second fails to install it, which must print an error and not
    // panic.
    #[test]
    fn a_second_listener_in_one_process_fails_without_panicking() {
        serve(([127, 0, 0, 1], 0).into(), "idle-screen", "test");
        serve(([127, 0, 0, 1], 0).into(), "idle-screen", "test");
    }
}
