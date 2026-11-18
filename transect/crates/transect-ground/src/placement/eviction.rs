//! When Kubernetes evicts a pod from a machine that stops responding.
//!
//! The node lifecycle controller puts a `NoExecute` taint on the
//! machine.  The taint is `unreachable` when the machine's kubelet stops
//! posting status, and `not-ready` when the kubelet reports the machine
//! unhealthy.  A pod is evicted when the shortest `tolerationSeconds`
//! among its tolerations of that taint runs out.  A pod that does not
//! tolerate the taint is evicted at once, and a pod whose tolerations of
//! the taint all set no limit is never evicted.  Admission adds a
//! 300-second toleration of each taint to every pod that sets none of
//! its own, and the DaemonSet controller adds a toleration with no
//! limit to its pods.

use k8s_openapi::api::core::v1::{Node, PodSpec, Taint};

use super::tolerates;

pub const UNREACHABLE: &str = "node.kubernetes.io/unreachable";
pub const NOT_READY: &str = "node.kubernetes.io/not-ready";

#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub enum Eviction {
    /// Evicted this many seconds after the machine gets the taint.
    After(i64),
    Never,
}

/// The key of the `NoExecute` taint that a lost machine gets:
/// `not-ready` when the `Node` already has it, and `unreachable`
/// otherwise, because a machine that is powered off or disconnected
/// gets `unreachable`.
pub fn lost_taint(node: Option<&Node>) -> &'static str {
    let taints = node.and_then(|node| node.spec.as_ref()?.taints.as_ref());
    let mut taints = taints.into_iter().flatten();
    let not_ready = taints.any(|t| t.key == NOT_READY && t.effect == "NoExecute");
    if not_ready { NOT_READY } else { UNREACHABLE }
}

/// When a pod of `spec` is evicted, counted from the time that its
/// machine gets the `NoExecute` taint `key`.
pub fn eviction(spec: &PodSpec, key: &str) -> Eviction {
    let taint = Taint {
        key: key.into(),
        effect: "NoExecute".into(),
        ..Taint::default()
    };
    let tolerations = spec.tolerations.iter().flatten();
    let matching: Vec<_> = tolerations.filter(|t| tolerates(t, &taint)).collect();
    if matching.is_empty() {
        return Eviction::After(0);
    }
    let limits = matching.iter().filter_map(|t| t.toleration_seconds);
    match limits.map(|seconds| seconds.max(0)).min() {
        Some(seconds) => Eviction::After(seconds),
        None => Eviction::Never,
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn spec(tolerations: &str) -> PodSpec {
        serde_yaml::from_str(&format!("{{containers: [], tolerations: {tolerations}}}")).unwrap()
    }

    const DEFAULTS: &str = "[{key: node.kubernetes.io/not-ready, operator: Exists, effect: NoExecute, tolerationSeconds: 300}, {key: node.kubernetes.io/unreachable, operator: Exists, effect: NoExecute, tolerationSeconds: 300}]";

    #[rstest]
    #[case::admissions_default_toleration(DEFAULTS, UNREACHABLE, Eviction::After(300))]
    #[case::its_own_limit(
        "[{key: node.kubernetes.io/unreachable, operator: Exists, effect: NoExecute, tolerationSeconds: 30}]",
        UNREACHABLE,
        Eviction::After(30)
    )]
    #[case::not_tolerated("[]", UNREACHABLE, Eviction::After(0))]
    #[case::no_limit("[{operator: Exists}]", UNREACHABLE, Eviction::Never)]
    #[case::the_shortest_limit_applies(
        "[{operator: Exists}, {key: node.kubernetes.io/not-ready, operator: Exists, tolerationSeconds: 60}]",
        NOT_READY,
        Eviction::After(60)
    )]
    #[case::another_taints_toleration(
        "[{key: node.kubernetes.io/not-ready, operator: Exists, effect: NoExecute}]",
        UNREACHABLE,
        Eviction::After(0)
    )]
    fn a_pod_is_evicted_when_its_toleration_runs_out(
        #[case] tolerations: &str,
        #[case] key: &str,
        #[case] expected: Eviction,
    ) {
        assert_eq!(eviction(&spec(tolerations), key), expected);
    }
}
