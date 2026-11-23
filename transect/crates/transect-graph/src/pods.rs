use k8s_openapi::api::core::v1::Pod;

/// Whether the pod's phase is `Succeeded` or `Failed`, which means its
/// containers have stopped and will not restart.  The scheduler and
/// `kubectl describe node` no longer count such a pod's requests against
/// its node, and nothing can connect to it.
pub fn finished(pod: &Pod) -> bool {
    let phase = pod.status.as_ref().and_then(|s| s.phase.as_deref());
    matches!(phase, Some("Succeeded" | "Failed"))
}

/// Whether the pod's `Ready` condition is `True`: each of its
/// containers passes its readiness check, so a `Service` sends it
/// traffic.
pub fn ready(pod: &Pod) -> bool {
    let conditions = pod.status.as_ref().and_then(|s| s.conditions.as_ref());
    let mut conditions = conditions.into_iter().flatten();
    conditions.any(|c| c.type_ == "Ready" && c.status == "True")
}

/// How many of the pod's containers are ready, and how many it has, as
/// the `READY` column of `kubectl get pods` counts them: the containers
/// of its spec, not its init containers.
pub fn ready_containers(pod: &Pod) -> (usize, usize) {
    let total = pod.spec.as_ref().map_or(0, |spec| spec.containers.len());
    let statuses = pod
        .status
        .as_ref()
        .and_then(|s| s.container_statuses.as_ref());
    let ready = statuses.into_iter().flatten().filter(|s| s.ready).count();
    (ready, total)
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn pod(phase: Option<&str>) -> Pod {
        let status = phase.map(|phase| format!("status: {{phase: {phase}}}"));
        serde_yaml_ng::from_str(&status.unwrap_or_else(|| "metadata: {}".into())).unwrap()
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

    const READY_POD: &str = "
spec: {containers: [{name: db}, {name: exporter}]}
status:
  conditions: [{type: Ready, status: 'True'}]
  containerStatuses:
    - {name: db, ready: true, restartCount: 0, image: db, imageID: '', started: true}
    - {name: exporter, ready: true, restartCount: 0, image: exporter, imageID: ''}
";

    const STARTING_POD: &str = "
spec: {containers: [{name: db}, {name: exporter}]}
status:
  conditions: [{type: Ready, status: 'False'}]
  containerStatuses:
    - {name: db, ready: false, restartCount: 0, image: db, imageID: ''}
    - {name: exporter, ready: true, restartCount: 0, image: exporter, imageID: ''}
";

    #[rstest]
    #[case::ready(READY_POD, true, (2, 2))]
    #[case::starting(STARTING_POD, false, (1, 2))]
    #[case::no_status("spec: {containers: [{name: db}]}", false, (0, 1))]
    fn says_whether_a_pod_is_ready(
        #[case] written: &str,
        #[case] is_ready: bool,
        #[case] containers: (usize, usize),
    ) {
        let pod: Pod = serde_yaml_ng::from_str(written).unwrap();
        assert_eq!(
            (ready(&pod), ready_containers(&pod)),
            (is_ready, containers)
        );
    }
}
