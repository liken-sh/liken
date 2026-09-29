//! The two gauges this crate reports on its own, through whatever recorder
//! the client that links it installed. [`build_info`] runs once, at start.
//! [`bus_connected`] runs from [`crate::reader`], on every change the
//! connection goes through.
//!
//! Neither function opens a listener or names a port: that is the
//! consuming binary's own setting, read the way every other one is, in
//! its own `wiring`. A client with no recorder installed pays for a
//! macro call that finds nowhere to go, which is what lets a
//! workstation run with none.

/// `liken_build_info{component, version}`, milestone 65's one gauge every
/// process in the organization reports under its own name. `component` is
/// this binary's identity, fixed in its own source; `version` is the tag
/// the operator resolved this client's image from, so a mixed fleet shows
/// on one panel.
pub fn build_info(component: &str, version: &str) {
    metrics::gauge!(
        "liken_build_info",
        "component" => component.to_string(),
        "version" => version.to_string(),
    )
    .set(1.0);
}

/// `media_bus_connected`, 1 while the session is up and 0 the moment it
/// is not. [`crate::reader::read`] calls this on every `ConnAck` and every
/// socket error, so a scrape never reads a session that ended with no word
/// of it.
pub fn bus_connected(connected: bool) {
    metrics::gauge!("media_bus_connected").set(if connected { 1.0 } else { 0.0 });
}

#[cfg(test)]
mod tests {
    use metrics::with_local_recorder;
    use metrics_util::debugging::{DebugValue, DebuggingRecorder};

    use super::*;

    /// Read the one gauge value a body sets, under a recorder of the test's
    /// own. A local recorder never touches the process-wide one, so the
    /// crate's other tests never race it.
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
        .expect("build_info sets the gauge");

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
            .expect("bus_connected sets the gauge");
        assert_eq!(value, 1.0);
    }

    #[test]
    fn bus_connected_reports_zero_once_the_session_ends() {
        let (value, _) = gauge("media_bus_connected", || bus_connected(false))
            .expect("bus_connected sets the gauge");
        assert_eq!(value, 0.0);
    }
}
