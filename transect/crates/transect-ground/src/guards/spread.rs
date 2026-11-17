//! How a workload spreads its pods across machines, so that the loss of
//! one machine removes one of its pods and not all of them.

use k8s_openapi::api::core::v1::PodSpec;

/// The node label that spreads pods one to a machine.
const HOSTNAME: &str = "kubernetes.io/hostname";

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Spread {
    AntiAffinity,
    TopologySpread,
}

/// How `spec` spreads its pods across machines: by anti-affinity or a
/// topology spread on the machine's hostname, required or preferred.  A
/// spread across zones or racks does not put each pod on its own
/// machine, so this function does not count it.
pub fn spread(spec: Option<&PodSpec>) -> Option<Spread> {
    let spec = spec?;
    let anti_affinity = spec
        .affinity
        .as_ref()
        .and_then(|affinity| affinity.pod_anti_affinity.as_ref());
    if let Some(anti) = anti_affinity {
        let required = anti
            .required_during_scheduling_ignored_during_execution
            .iter()
            .flatten();
        let preferred = anti
            .preferred_during_scheduling_ignored_during_execution
            .iter()
            .flatten()
            .map(|weighted| &weighted.pod_affinity_term);
        if required
            .chain(preferred)
            .any(|term| term.topology_key == HOSTNAME)
        {
            return Some(Spread::AntiAffinity);
        }
    }
    let constraints = spec.topology_spread_constraints.iter().flatten();
    constraints
        .clone()
        .any(|constraint| constraint.topology_key == HOSTNAME)
        .then_some(Spread::TopologySpread)
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn yaml(text: &str) -> PodSpec {
        serde_yaml::from_str(text).unwrap()
    }

    #[rstest]
    #[case::none("{containers: []}", None)]
    #[case::required_anti_affinity(
        "{containers: [], affinity: {podAntiAffinity: {requiredDuringSchedulingIgnoredDuringExecution: [{topologyKey: kubernetes.io/hostname}]}}}",
        Some(Spread::AntiAffinity)
    )]
    #[case::preferred_anti_affinity(
        "{containers: [], affinity: {podAntiAffinity: {preferredDuringSchedulingIgnoredDuringExecution: [{weight: 100, podAffinityTerm: {topologyKey: kubernetes.io/hostname}}]}}}",
        Some(Spread::AntiAffinity)
    )]
    #[case::anti_affinity_by_zone(
        "{containers: [], affinity: {podAntiAffinity: {requiredDuringSchedulingIgnoredDuringExecution: [{topologyKey: topology.kubernetes.io/zone}]}}}",
        None
    )]
    #[case::topology_spread(
        "{containers: [], topologySpreadConstraints: [{maxSkew: 1, topologyKey: kubernetes.io/hostname, whenUnsatisfiable: DoNotSchedule}]}",
        Some(Spread::TopologySpread)
    )]
    fn reads_how_a_template_spreads_its_pods(#[case] spec: &str, #[case] spread: Option<Spread>) {
        assert_eq!(super::spread(Some(&yaml(spec))), spread);
    }
}
