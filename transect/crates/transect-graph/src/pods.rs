//! A pod's lifecycle, as far as the map reads it.

use k8s_openapi::api::core::v1::Pod;

/// Whether a pod has finished: its phase is `Succeeded` or `Failed`, so
/// every container has stopped for good.  A finished pod holds no
/// requests on its node, as the scheduler and `kubectl describe node`
/// count them, and no address a connection could reach.
pub fn finished(pod: &Pod) -> bool {
    let phase = pod.status.as_ref().and_then(|s| s.phase.as_deref());
    matches!(phase, Some("Succeeded" | "Failed"))
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn pod(phase: Option<&str>) -> Pod {
        let status = phase.map(|phase| format!("status: {{phase: {phase}}}"));
        serde_yaml::from_str(&status.unwrap_or_else(|| "metadata: {}".into())).unwrap()
    }

    #[rstest]
    #[case::pending(Some("Pending"), false)]
    #[case::running(Some("Running"), false)]
    #[case::succeeded(Some("Succeeded"), true)]
    #[case::failed(Some("Failed"), true)]
    #[case::unknown(Some("Unknown"), false)]
    #[case::no_status(None, false)]
    fn a_pod_finishes_when_it_succeeds_or_fails(#[case] phase: Option<&str>, #[case] done: bool) {
        assert_eq!(finished(&pod(phase)), done);
    }
}
