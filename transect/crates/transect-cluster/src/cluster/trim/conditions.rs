//! The times a condition's writer bumps on every check, dropped before
//! the store.  Kubernetes's own kinds name them `lastHeartbeatTime`
//! (a `Node`'s), `lastProbeTime` (a `Pod`'s, a `Job`'s, a claim's), and
//! `lastUpdateTime` (a `Deployment`'s), and custom kinds copy the names.
//! No page reads them, and a write that bumps only one of them would
//! otherwise count as a change and rebuild the model.  A change to a
//! condition's status, reason, or message still counts as one, and
//! `lastTransitionTime`, which moves only when the status does, is kept.

use k8s_openapi::api::apps::v1::Deployment;
use k8s_openapi::api::core::v1::{Node, PersistentVolumeClaim, Pod};
use serde_json::Value;

/// The heartbeat fields of a condition, by the names Kubernetes uses.
pub const HEARTBEATS: [&str; 3] = ["lastHeartbeatTime", "lastProbeTime", "lastUpdateTime"];

pub fn node(node: &mut Node) {
    let conditions = node
        .status
        .iter_mut()
        .flat_map(|s| s.conditions.iter_mut().flatten());
    for condition in conditions {
        condition.last_heartbeat_time = None;
    }
}

pub fn pod(pod: &mut Pod) {
    let conditions = pod
        .status
        .iter_mut()
        .flat_map(|s| s.conditions.iter_mut().flatten());
    for condition in conditions {
        condition.last_probe_time = None;
    }
}

pub fn deployment(deployment: &mut Deployment) {
    let status = deployment.status.iter_mut();
    for condition in status.flat_map(|s| s.conditions.iter_mut().flatten()) {
        condition.last_update_time = None;
    }
}

pub fn claim(claim: &mut PersistentVolumeClaim) {
    let conditions = claim
        .status
        .iter_mut()
        .flat_map(|s| s.conditions.iter_mut().flatten());
    for condition in conditions {
        condition.last_probe_time = None;
    }
}

/// A custom resource's `status.conditions`, read as JSON.
pub fn custom(data: &mut Value) {
    let conditions = data
        .get_mut("status")
        .and_then(|status| status.get_mut("conditions"))
        .and_then(Value::as_array_mut);
    for condition in conditions.into_iter().flatten() {
        if let Some(condition) = condition.as_object_mut() {
            for field in HEARTBEATS {
                condition.remove(field);
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use kube::core::DynamicObject;
    use rstest::rstest;

    use super::*;
    use crate::cluster::change::same;

    /// Whether two writes of `kind`, which differ as `first` and `second`
    /// do, compare as the same object once trimmed.
    fn same_once_trimmed<K: serde::de::DeserializeOwned + serde::Serialize>(
        first: &str,
        second: &str,
        trim: fn(&mut K),
    ) -> bool {
        let (mut first, mut second): (K, K) = (
            serde_yaml::from_str(first).unwrap(),
            serde_yaml::from_str(second).unwrap(),
        );
        trim(&mut first);
        trim(&mut second);
        same(&first, &second, &[])
    }

    fn condition(times: &str, status: &str) -> String {
        format!(
            "{{metadata: {{name: a}}, status: {{conditions: [{{type: Ready, status: '{status}', {times}}}]}}}}"
        )
    }

    #[rstest]
    #[case::a_heartbeat("lastHeartbeatTime: '2026-01-01T00:00:10Z'", "True", true)]
    #[case::a_new_status("lastHeartbeatTime: '2026-01-01T00:00:10Z'", "False", false)]
    fn reads_a_node_heartbeat_as_no_change(
        #[case] times: &str,
        #[case] status: &str,
        #[case] expected: bool,
    ) {
        let held = condition("lastHeartbeatTime: '2026-01-01T00:00:00Z'", "True");
        let found = condition(times, status);
        assert_eq!(same_once_trimmed(&held, &found, node), expected);
    }

    #[rstest]
    #[case::a_pod(same_once_trimmed::<Pod>(
        &condition("lastProbeTime: '2026-01-01T00:00:00Z'", "True"),
        &condition("lastProbeTime: '2026-01-01T00:00:10Z'", "True"),
        pod,
    ))]
    #[case::a_claim(same_once_trimmed::<PersistentVolumeClaim>(
        &condition("lastProbeTime: '2026-01-01T00:00:00Z'", "True"),
        &condition("lastProbeTime: '2026-01-01T00:00:10Z'", "True"),
        claim,
    ))]
    #[case::a_deployment(same_once_trimmed::<Deployment>(
        &condition("lastUpdateTime: '2026-01-01T00:00:00Z'", "True"),
        &condition("lastUpdateTime: '2026-01-01T00:00:10Z'", "True"),
        deployment,
    ))]
    fn reads_a_probe_or_update_time_as_no_change(#[case] same: bool) {
        assert!(same);
    }

    fn widget(times: &str, reason: &str) -> DynamicObject {
        serde_yaml::from_str(&format!(
            "{{apiVersion: example.io/v1, kind: Widget, metadata: {{name: a}}, status: {{conditions: [{{type: Ready, status: 'True', reason: {reason}, {times}}}]}}}}"
        ))
        .unwrap()
    }

    fn trim_widget(widget: &mut DynamicObject) {
        custom(&mut widget.data);
    }

    #[rstest]
    #[case::each_heartbeat(
        widget("lastHeartbeatTime: b, lastProbeTime: b, lastUpdateTime: b", "Up"),
        true
    )]
    #[case::a_new_reason(widget("lastHeartbeatTime: b", "Down"), false)]
    #[case::a_transition(widget("lastTransitionTime: b", "Up"), false)]
    fn reads_a_custom_heartbeat_as_no_change(#[case] found: DynamicObject, #[case] expected: bool) {
        let (mut held, mut found) = (widget("lastHeartbeatTime: a", "Up"), found);
        trim_widget(&mut held);
        trim_widget(&mut found);
        assert_eq!(same(&held, &found, &[]), expected);
    }
}
