//! The memory that each pod uses, compared with the memory it requests.
//!
//! Under memory pressure, the kubelet first evicts the pods whose working
//! set exceeds their memory request, so a pod over its request is at
//! risk even when the machine's total looks safe.  The machine's total
//! cannot replace this comparison, because the root cgroup also counts
//! the system daemons and the kubelet, which no pod requests, so every
//! machine uses more than its pods request.

use super::Amount;

/// One pod's memory working set and its memory request.
#[derive(Clone, Debug, PartialEq)]
pub struct PodMemory {
    pub namespace: String,
    pub name: String,
    /// Bytes, when Prometheus has a reading for the pod.  A pod with no
    /// reading still counts in `requests_none`.
    pub used: Option<f64>,
    pub requested: Amount,
}

impl PodMemory {
    /// The bytes the pod uses past its request, or `None` when it uses no
    /// more than its request or has no reading.  For a pod that requests
    /// no memory, the excess is all that it uses.
    pub fn excess(&self) -> Option<f64> {
        let excess = self.used? - self.requested.units();
        (excess > 0.0).then_some(excess)
    }

    pub fn requests_none(&self) -> bool {
        self.requested == Amount::default()
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn pod(used: Option<f64>, requested: &str) -> PodMemory {
        PodMemory {
            namespace: "shop".into(),
            name: "web".into(),
            used,
            requested: Amount::parse(requested).unwrap(),
        }
    }

    #[rstest]
    #[case::over(Some(3000.0), "1k", Some(2000.0))]
    #[case::at_its_request(Some(1000.0), "1k", None)]
    #[case::under(Some(500.0), "1k", None)]
    #[case::requests_none(Some(700.0), "0", Some(700.0))]
    #[case::no_reading(None, "1k", None)]
    fn excess_is_what_a_pod_uses_past_its_request(
        #[case] used: Option<f64>,
        #[case] requested: &str,
        #[case] expected: Option<f64>,
    ) {
        assert_eq!(pod(used, requested).excess(), expected);
    }
}
