// The browser's Prometheus metrics: the `liken_build_info` gauge milestone
// 65 puts on every liken process, the per-frame timing histogram, and the
// art cache's own byte gauge. A scrape reads the facade's in-memory
// registry; it opens no file and touches no volume.
//
// The facade (the `metrics` crate) is a set of macros over a global
// recorder. Nothing here calls those macros until `install` has set that
// recorder, because a call before that point is recorded nowhere and
// silently dropped, which is also why every test below runs its own
// recorder rather than the real one.

use std::net::SocketAddr;
use std::time::Duration;

use metrics_exporter_prometheus::{BuildError, Matcher, PrometheusBuilder};
use metrics_process::Collector;

/// The name milestone 65 gives this process on `liken_build_info`. The
/// dashboard that reads that gauge across the fleet keys on this label, so
/// it must match no other process's name.
const COMPONENT: &str = "media-browser";

/// How often the background thread refreshes layer 1's process_* series.
/// A gauge that goes stale between scrapes is still correct, so this only
/// has to be shorter than a scrape interval, not instant.
const PROCESS_COLLECT_INTERVAL: Duration = Duration::from_secs(5);

/// The release this binary was built as. The Dockerfile's `VERSION`
/// build argument sets `MEDIA_BROWSER_VERSION` before the final compile,
/// and a build outside that image, such as `cargo build` on a
/// workstation, reports "dev".
fn version() -> &'static str {
    option_env!("MEDIA_BROWSER_VERSION").unwrap_or("dev")
}

/// Bucket bounds for `library_browser_frame_seconds`, in seconds. They
/// run from 2 ms, a fast frame, to 1 s, a stalled one, with denser
/// coverage around 60 fps (0.0167 s) and 30 fps (0.033 s), the range
/// where a regression first shows up.
const FRAME_SECONDS_BUCKETS: &[f64] = &[
    0.002, 0.004, 0.008, 0.0167, 0.033, 0.05, 0.1, 0.25, 0.5, 1.0,
];

/// Configure `library_browser_frame_seconds` with fixed buckets so the
/// exporter renders it as a Prometheus histogram (`_bucket` lines).
/// Without a bucket configuration, the exporter renders a histogram as
/// a summary (`quantile` lines) instead, and a summary cannot be
/// aggregated across the pods the plan scrapes.
fn with_frame_seconds_buckets(builder: PrometheusBuilder) -> Result<PrometheusBuilder, BuildError> {
    builder.set_buckets_for_metric(
        Matcher::Full("library_browser_frame_seconds".to_string()),
        FRAME_SECONDS_BUCKETS,
    )
}

/// Install the recorder and start the listener, if `address` names one.
/// `None` serves no metrics, which is what a run outside a pod does and
/// what a pod gets until the operator sets the listener's address.
///
/// The Prometheus builder spawns the listener on a background thread with
/// a runtime of its own, because this process drives no async runtime of
/// its own for it to join. A failure here, an address already in use or
/// a port a container did not open, is reported and never blocks the
/// window the rest of the process exists to draw.
pub fn install(address: Option<SocketAddr>) {
    let Some(address) = address else {
        return;
    };

    let builder = PrometheusBuilder::new().with_http_listener(address);
    let builder = match with_frame_seconds_buckets(builder) {
        Ok(builder) => builder,
        Err(error) => {
            eprintln!("media-browser: metrics listener: {error}");
            return;
        }
    };
    if let Err(error) = builder.install() {
        eprintln!("media-browser: metrics listener: {error}");
        return;
    }

    record_build_info();

    // The collector reads /proc itself, on its own schedule, so no frame
    // waits on it. `describe` registers the help text once; `collect`
    // refreshes the values a scrape then reads.
    let collector = Collector::default();
    collector.describe();
    std::thread::spawn(move || {
        loop {
            collector.collect();
            std::thread::sleep(PROCESS_COLLECT_INTERVAL);
        }
    });
}

/// Set `liken_build_info` to 1, labeled with this process's component and
/// release. The value never changes; the labels are the fact, which is
/// the Prometheus convention for publishing a string.
fn record_build_info() {
    metrics::gauge!(
        "liken_build_info",
        "component" => COMPONENT,
        "version" => version(),
    )
    .set(1.0);
}

/// One sample of how long a drawn frame took to build, draw, and submit,
/// in seconds. The harness calls this at the same point it already
/// measures the frame for the JSON stats file, so no frame is measured
/// twice and a covered screen, which draws no frame, adds no sample.
pub fn record_frame_seconds(seconds: f64) {
    metrics::histogram!("library_browser_frame_seconds").record(seconds);
}

/// The bytes the in-memory art cache holds right now. `None` says the
/// screen holds no such cache, such as a run with no catalog, and leaves
/// the gauge unset rather than publishing a zero the screen never
/// measured.
pub fn set_art_cache_bytes(bytes: Option<usize>) {
    if let Some(bytes) = bytes {
        metrics::gauge!("library_browser_art_cache_bytes").set(bytes as f64);
    }
}

#[cfg(test)]
mod tests {
    use metrics_util::CompositeKey;
    use metrics_util::debugging::{DebugValue, DebuggingRecorder};

    use super::*;

    /// One recorder per test, read back through its own snapshot, so two
    /// tests running in parallel never share a global recorder.
    fn value(name: &str, record: impl FnOnce()) -> Option<(CompositeKey, DebugValue)> {
        let recorder = DebuggingRecorder::new();
        let snapshotter = recorder.snapshotter();
        metrics::with_local_recorder(&recorder, record);
        snapshotter
            .snapshot()
            .into_vec()
            .into_iter()
            .find(|(key, ..)| key.key().name() == name)
            .map(|(key, _, _, value)| (key, value))
    }

    fn label<'a>(key: &'a CompositeKey, name: &str) -> Option<&'a str> {
        key.key()
            .labels()
            .find(|label| label.key() == name)
            .map(metrics::Label::value)
    }

    #[test]
    fn build_info_names_the_component_and_the_release() {
        let (key, recorded) =
            value("liken_build_info", record_build_info).expect("the gauge was recorded");
        assert_eq!(label(&key, "component"), Some(COMPONENT));
        assert_eq!(label(&key, "version"), Some(version()));
        assert_eq!(recorded, DebugValue::Gauge(1.0.into()));
    }

    #[test]
    fn a_frame_lands_one_histogram_sample() {
        let (_, recorded) = value("library_browser_frame_seconds", || {
            record_frame_seconds(0.008)
        })
        .expect("the histogram was recorded");
        assert_eq!(recorded, DebugValue::Histogram(vec![0.008.into()]));
    }

    #[test]
    fn the_cache_gauge_reports_the_bytes_it_was_given() {
        let (_, recorded) = value("library_browser_art_cache_bytes", || {
            set_art_cache_bytes(Some(4_096));
        })
        .expect("the gauge was recorded");
        assert_eq!(recorded, DebugValue::Gauge(4_096.0.into()));
    }

    #[test]
    fn no_cache_bytes_means_no_gauge_write() {
        let recorded = value("library_browser_art_cache_bytes", || {
            set_art_cache_bytes(None);
        });
        assert!(recorded.is_none());
    }

    /// The `DebuggingRecorder` used by the tests above records only the
    /// facade call, not the exporter's rendering, so it cannot tell a
    /// histogram from a summary. This test builds the real
    /// `PrometheusRecorder` instead, with no HTTP listener, and reads
    /// back its rendered text, the only place the summary-vs-histogram
    /// distinction shows up.
    #[test]
    fn frame_seconds_renders_as_a_histogram_not_a_summary() {
        let recorder = with_frame_seconds_buckets(PrometheusBuilder::new())
            .expect("the metric name is a valid matcher")
            .build_recorder();
        let handle = recorder.handle();
        metrics::with_local_recorder(&recorder, || record_frame_seconds(0.008));

        let rendered = handle.render();

        assert!(
            rendered.contains("library_browser_frame_seconds_bucket"),
            "expected bucket lines, got:\n{rendered}"
        );
        assert!(
            !rendered.contains("quantile="),
            "expected no summary quantiles, got:\n{rendered}"
        );
    }

    #[test]
    fn an_empty_address_installs_no_listener() {
        // No recorder is installed globally by this test, so the only
        // thing to prove is that the early return takes, and not the
        // panic a second global install would raise.
        install(None);
    }
}
