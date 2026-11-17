//! The memory findings: pods that use more than they requested, on each
//! machine, and pods that request no memory, across the cluster.
//!
//! The two are separate because they have separate fixes.  A pod over a
//! request it set needs a larger request; a pod with no request needs
//! one at all.  Counting both together would make the per-machine
//! finding show on nearly every machine, mostly because of pods that set
//! no request.

use super::{Finding, Overage};
use transect_ground::machines::{Machine, PodMemory};

/// How far the pods on one machine that set a memory request may use
/// memory past those requests, together, before it is worth knowing.
/// Small pods drift a few Mi past small requests all the time, and
/// without a floor the finding would show on most machines most of the
/// time.
pub const OVER_REQUESTS_BYTES: f64 = (256u64 << 20) as f64;

/// The working set at which a pod that requests no memory is worth
/// knowing about.  Most such pods are small helpers using a few Mi, and
/// the scheduler's error for those is too small to matter.
pub const HEAVY_UNREQUESTED_BYTES: f64 = (64u64 << 20) as f64;

/// One finding naming every machine whose pods with a memory request use
/// at least `OVER_REQUESTS_BYTES` past those requests, the largest excess
/// first, or none when no machine does.  The rule needs a reading of what
/// each pod uses, so transect never makes this finding when it runs with
/// no Prometheus.
pub fn over_memory_requests(machines: &[Machine]) -> Option<Finding> {
    let mut over: Vec<Overage> = machines.iter().filter_map(over_on_machine).collect();
    over.sort_by(|a, b| b.excess.total_cmp(&a.excess));
    (!over.is_empty()).then_some(Finding::OverMemoryRequests { machines: over })
}

fn over_on_machine(machine: &Machine) -> Option<Overage> {
    let over: Vec<f64> = machine
        .pod_memory
        .iter()
        .filter(|pod| !pod.requests_none())
        .filter_map(PodMemory::excess)
        .collect();
    let excess: f64 = over.iter().sum();
    (excess >= OVER_REQUESTS_BYTES).then(|| Overage {
        machine: machine.name.clone(),
        pods: over.len(),
        excess,
    })
}

/// One finding for the whole cluster when any pod that requests no
/// memory uses `HEAVY_UNREQUESTED_BYTES` or more.  It counts every such
/// pod, with a reading or without, so the title shows the real size of
/// the problem even when only a few of the pods are heavy.
pub fn requesting_no_memory(machines: &[Machine]) -> Option<Finding> {
    let unrequested: Vec<&PodMemory> = machines
        .iter()
        .flat_map(|machine| &machine.pod_memory)
        .filter(|pod| pod.requests_none())
        .collect();
    let heavy = unrequested
        .iter()
        .filter_map(|pod| pod.used)
        .filter(|used| *used >= HEAVY_UNREQUESTED_BYTES)
        .count();
    (heavy > 0).then(|| Finding::RequestNoMemory {
        pods: unrequested.len(),
        heavy,
        used: unrequested.iter().filter_map(|pod| pod.used).sum(),
    })
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::super::fixtures::*;
    use super::*;

    fn alpha(pods: &[(f64, &str)]) -> Machine {
        with_pods(machine("alpha", "1", "1Gi"), pods)
    }

    #[rstest]
    #[case::well_over(&[(600.0, "256Mi"), (300.0, "400Mi")], Some((1, 344.0)))]
    #[case::several_add_up(&[(300.0, "200Mi"), (300.0, "200Mi"), (156.0, "100Mi")], Some((3, 256.0)))]
    #[case::unrequested_left_out(&[(2000.0, "0"), (300.0, "40Mi")], Some((1, 260.0)))]
    #[case::only_unrequested(&[(2000.0, "0")], None)]
    #[case::just_under_the_line(&[(355.0, "100Mi")], None)]
    #[case::all_within_requests(&[(900.0, "1Gi"), (100.0, "200Mi")], None)]
    #[case::no_readings(&[], None)]
    fn over_memory_requests_adds_up_requested_pods_excess(
        #[case] pods: &[(f64, &str)],
        #[case] expected: Option<(usize, f64)>,
    ) {
        let expected = expected.map(|(pods, excess_mi)| Finding::OverMemoryRequests {
            machines: vec![Overage {
                machine: "alpha".into(),
                pods,
                excess: excess_mi * BYTES_PER_MI,
            }],
        });
        assert_eq!(over_memory_requests(&[alpha(pods)]), expected);
    }

    #[rstest]
    fn over_memory_requests_lists_the_largest_excess_first() {
        let machines = [
            with_pods(machine("alpha", "1", "1Gi"), &[(600.0, "256Mi")]),
            with_pods(machine("beta", "1", "1Gi"), &[(2048.0, "512Mi")]),
            machine("gamma", "1", "1Gi"),
            with_pods(machine("delta", "1", "1Gi"), &[(1024.0, "256Mi")]),
        ];
        let overage = |machine: &str, excess_mi: f64| Overage {
            machine: machine.into(),
            pods: 1,
            excess: excess_mi * BYTES_PER_MI,
        };
        assert_eq!(
            over_memory_requests(&machines),
            Some(Finding::OverMemoryRequests {
                machines: vec![
                    overage("beta", 1536.0),
                    overage("delta", 768.0),
                    overage("alpha", 344.0),
                ],
            })
        );
    }

    #[rstest]
    #[case::one_heavy_among_light(&[(100.0, "0"), (10.0, "0"), (1.0, "0")], 0, Some((3, 1, 111.0)))]
    #[case::exactly_at_the_line(&[(64.0, "0")], 0, Some((1, 1, 64.0)))]
    #[case::unread_pods_count(&[(200.0, "0")], 2, Some((3, 1, 200.0)))]
    #[case::requested_pods_left_out(&[(100.0, "0"), (900.0, "100Mi")], 0, Some((1, 1, 100.0)))]
    #[case::all_light(&[(63.0, "0"), (10.0, "0")], 0, None)]
    #[case::heavy_but_requested(&[(900.0, "100Mi")], 0, None)]
    #[case::none_read(&[], 4, None)]
    fn requesting_no_memory_counts_every_such_pod(
        #[case] pods: &[(f64, &str)],
        #[case] unread: usize,
        #[case] expected: Option<(usize, usize, f64)>,
    ) {
        let machines = [with_unread_pods(alpha(pods), unread)];
        let expected = expected.map(|(pods, heavy, used_mi)| Finding::RequestNoMemory {
            pods,
            heavy,
            used: used_mi * BYTES_PER_MI,
        });
        assert_eq!(requesting_no_memory(&machines), expected);
    }

    #[rstest]
    fn requesting_no_memory_adds_up_across_machines() {
        let machines = [
            with_pods(machine("alpha", "1", "1Gi"), &[(100.0, "0")]),
            with_pods(machine("beta", "1", "1Gi"), &[(20.0, "0"), (300.0, "0")]),
        ];
        assert_eq!(
            requesting_no_memory(&machines),
            Some(Finding::RequestNoMemory {
                pods: 3,
                heavy: 2,
                used: 420.0 * BYTES_PER_MI,
            })
        );
    }
}
