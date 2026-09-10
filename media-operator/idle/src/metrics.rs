//! This client's `/metrics` listener: milestone 65's layer 1, and the one
//! gauge [`media_screen::reader`] reports on this client's own bus
//! connection. This client never touches mpv, so it carries none of
//! `media-screen`'s row in plan 26's table; that reads through mpv's own
//! properties, which this process never opens a socket to.

use std::net::SocketAddr;
use std::time::Duration;

use metrics_exporter_prometheus::PrometheusBuilder;
use metrics_process::Collector;

/// How often the collector reads `/proc` again. The exporter has no
/// per-scrape hook to call it from, so a thread of its own keeps the
/// numbers under ten seconds old, well inside any interval a Prometheus
/// scrapes at.
const COLLECTION_INTERVAL: Duration = Duration::from_secs(10);

/// Parse `MEDIA_METRICS_ADDRESS`. Empty or unreadable is the address every
/// other setting on this client answers to with: no listener opens, and the
/// gauges below cost a lookup that finds nowhere to go.
pub fn address(raw: &str) -> Option<SocketAddr> {
    let raw = raw.trim();
    if raw.is_empty() {
        return None;
    }
    raw.parse().ok()
}

/// Install the exporter, report this client's build identity, and start the
/// thread that keeps `process_*` current. A failure here is a line, never an
/// exit: the client draws the idle screen whether or not a Prometheus reads
/// it, the way a lost bus session draws on with its last-known state.
pub fn serve(address: SocketAddr, component: &str, version: &str) {
    if let Err(error) = PrometheusBuilder::new()
        .with_http_listener(address)
        .install()
    {
        eprintln!("idle-screen: metrics: {error}");
        return;
    }
    media_screen::metrics::build_info(component, version);
    // Primed before this client's own reader opens, so a scrape between
    // start and the first session reads a stated 0, not an absent series.
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
        assert_eq!(address("0.0.0.0:9222"), Some(([0, 0, 0, 0], 9222).into()));
    }

    // The global recorder installs once per process, so this is the one test
    // in the crate that calls `serve`. The first call takes the recorder and
    // proves the happy path runs with no panic; the second finds it taken,
    // which proves the listener's own failure never becomes this client's.
    #[test]
    fn a_second_listener_in_one_process_fails_without_panicking() {
        serve(([127, 0, 0, 1], 0).into(), "idle-screen", "test");
        serve(([127, 0, 0, 1], 0).into(), "idle-screen", "test");
    }
}
