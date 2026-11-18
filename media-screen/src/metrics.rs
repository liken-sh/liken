//! The gauges of this crate. They go to whatever `metrics` recorder the
//! client binary installs, and the client chooses how to serve them. With no
//! recorder installed the calls do nothing, so a client also runs on a
//! workstation.

/// Sets `liken_build_info{component, version}` to 1, so a query of the gauge
/// shows which version of each client runs. The client calls it once at
/// start.
pub fn build_info(component: &str, version: &str) {
    metrics::gauge!(
        "liken_build_info",
        "component" => component.to_string(),
        "version" => version.to_string(),
    )
    .set(1.0);
}

/// Sets `media_bus_connected` to 1 while the MQTT session is up and to 0
/// after it fails. [`crate::Reader::open`] sets it to 0 before the first
/// session, and [`crate::reader::read`] sets it on every `ConnAck` and every
/// connection error.
pub fn bus_connected(connected: bool) {
    metrics::gauge!("media_bus_connected").set(if connected { 1.0 } else { 0.0 });
}

#[cfg(test)]
mod tests {
    use metrics::with_local_recorder;
    use metrics_util::debugging::{DebugValue, DebuggingRecorder};

    use super::*;

    /// Runs `body` under a recorder local to this thread, so tests that run
    /// in parallel do not share one, and returns the value and labels of the
    /// gauge called `name`.
    fn gauge(name: &str, body: impl FnOnce()) -> Option<(f64, Vec<(String, String)>)> {
        let recorder = DebuggingRecorder::new();
        let snapshotter = recorder.snapshotter();
        with_local_recorder(&recorder, body);
        snapshotter
            .snapshot()
            .into_vec()
            .into_iter()
            .find_map(|(key, _, _, value)| {
                if key.key().name() != name {
                    return None;
                }
                let DebugValue::Gauge(value) = value else {
                    return None;
                };
                let labels = key
                    .key()
                    .labels()
                    .map(|label| (label.key().to_string(), label.value().to_string()))
                    .collect();
                Some((value.into_inner(), labels))
            })
    }

    #[test]
    fn build_info_reports_the_component_and_the_version() {
        let (value, labels) = gauge("liken_build_info", || {
            build_info("idle-screen", "2026.09.10-001")
        })
        .expect("build_info did not set liken_build_info");

        assert_eq!(value, 1.0);
        assert_eq!(
            labels,
            [
                ("component".to_string(), "idle-screen".to_string()),
                ("version".to_string(), "2026.09.10-001".to_string()),
            ]
        );
    }

    #[test]
    fn bus_connected_reports_one_while_up() {
        let (value, _) = gauge("media_bus_connected", || bus_connected(true))
            .expect("bus_connected did not set media_bus_connected");
        assert_eq!(value, 1.0);
    }

    #[test]
    fn bus_connected_reports_zero_once_the_session_ends() {
        let (value, _) = gauge("media_bus_connected", || bus_connected(false))
            .expect("bus_connected did not set media_bus_connected");
        assert_eq!(value, 0.0);
    }
}
