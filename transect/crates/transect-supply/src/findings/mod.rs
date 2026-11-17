//! Findings are facts about the cluster that are worth knowing but are
//! not yet a problem.  transect finds them with rules over the machines,
//! the workloads, and the labels of the running images, and it also
//! reports the loops that the edge graph finds.
//!
//! Each rule returns a `Finding` that records what the rule matched as
//! data, and the templates in `sentence.rs` render the finding as a
//! title and a body.  Every machine rule is a pure function of the
//! machines, so the sidebar changes exactly when the machines band
//! changes.  The workload rules are in `workloads/`.

mod images;
mod logged;
mod machine_logs;
mod memory;
mod restarts;
mod sentence;
mod workloads;

pub use logged::{Matched, worth_knowing};
pub use machine_logs::{MachineLogged, of as machine_logs};
pub use restarts::Restarted;
pub use workloads::{Rule, WorkloadFinding, of_workload};

use crate::images::{Census, Gap, Unreadable};
use transect_graph::edges::{Loop, Object};
use transect_ground::machines::{Amount, Machine};
use transect_observe::logs::evidence::Evidence;

/// The share of a machine's allocatable CPU or memory that its pods may
/// request before the machine counts as nearly full.  Past it, a pod of
/// ordinary size starts failing to schedule there.
const NEARLY_FULL_PERCENT: i128 = 85;

#[derive(Clone, Debug, PartialEq)]
pub enum Finding {
    /// Requests fill more than `NEARLY_FULL_PERCENT` of the machine's CPU,
    /// its memory, or both.  A resource below the line is `None`.
    NearlyFull {
        machine: String,
        cpu: Option<Fill>,
        memory: Option<Fill>,
    },
    /// On each machine listed, the pods that set a memory request use,
    /// together, at least `memory::OVER_REQUESTS_BYTES` more than they
    /// requested.  One finding names every such machine, the largest
    /// excess first, so the sidebar shows one card in place of a
    /// near-identical card for each machine.
    OverMemoryRequests { machines: Vec<Overage> },
    /// Across the cluster, some pod that requests no memory uses at least
    /// `memory::HEAVY_UNREQUESTED_BYTES`.
    RequestNoMemory {
        /// Every pod that requests no memory, read or not.
        pods: usize,
        /// How many of them use `HEAVY_UNREQUESTED_BYTES` or more.
        heavy: usize,
        /// The bytes all of them use together, from those with a reading.
        used: f64,
    },
    /// The workloads one workload rule found across the cluster, by
    /// namespace and name.
    Workloads { rule: Rule, workloads: Vec<Linked> },
    /// The running images whose labels name no source, by registry.
    Unlabelled(Vec<Gap>),
    /// The running images whose labels transect cannot read, by
    /// registry and reason.
    Unreadable(Vec<Unreadable>),
    /// Containers whose last restart was too long ago to count toward
    /// health, and recent enough to be worth knowing, the latest first.
    RestartedEarlier(Vec<Restarted>),
    /// A log pattern only worth knowing matched in these namespaces
    /// over its window, or an error or warning pattern matched there
    /// longer ago than counts as now, the most matches first.
    Logged {
        pattern: String,
        meaning: String,
        window: String,
        namespaces: Vec<Matched>,
    },
    /// The machines' own logs contained error lines over `window`,
    /// listed by machine, the most first.
    MachineLogs {
        window: String,
        machines: Vec<MachineLogged>,
    },
    /// A set of namespaces that each need another of them to come up,
    /// one finding per loop.
    Loop(Loop),
}

impl Finding {
    /// The loop that the page draws above a loop finding's body.
    pub fn as_loop(&self) -> Option<&Loop> {
        match self {
            Self::Loop(found) => Some(found),
            _ => None,
        }
    }
}

/// A workload a finding names, with the page a link to it opens.
#[derive(Clone, Debug, PartialEq)]
pub struct Linked {
    pub object: Object,
    pub href: Option<String>,
}

/// One line that the page lists above a finding's body: the name of
/// what the finding names, the page the name links to, and the text
/// after the name.
#[derive(Clone, Debug, PartialEq)]
pub struct Line {
    pub label: String,
    pub href: Option<String>,
    pub text: String,
    /// Where a log finding's matches came from, and a few of its lines.
    pub evidence: Option<Evidence>,
}

/// One machine's pods over their memory requests.
#[derive(Clone, Debug, PartialEq)]
pub struct Overage {
    pub machine: String,
    /// How many pods use more than the request they set.
    pub pods: usize,
    /// The bytes those pods use past their requests, in total.
    pub excess: f64,
}

/// How much of one resource a machine's pods request, against what it can
/// allocate and, with a Prometheus reading, what it uses.
#[derive(Clone, Copy, Debug, PartialEq)]
pub struct Fill {
    pub requested: Amount,
    pub allocatable: Amount,
    /// Cores for CPU, bytes for memory.
    pub used: Option<f64>,
}

impl Fill {
    /// What a new pod may still request and schedule.  A pod that names
    /// its node skips the scheduler, so requests can pass what the
    /// machine allocates.  Then nothing is free.
    pub fn free(&self) -> Amount {
        (self.allocatable - self.requested).max(Amount::default())
    }

    /// Compared in whole thousandths, not floats, so a machine exactly at
    /// the line is not past it.
    fn nearly_full(&self) -> bool {
        let (requested, allocatable) = (self.requested.millis(), self.allocatable.millis());
        allocatable > 0 && requested * 100 > allocatable * NEARLY_FULL_PERCENT
    }
}

/// What happened in the pods that the findings list: each pattern's
/// finding, and the containers that restarted earlier.
#[derive(Default)]
pub struct Happened {
    pub patterns: Vec<Finding>,
    pub restarted: Vec<Restarted>,
}

/// Every finding about `machines`, `workloads`, the findings about
/// each workload, `images`, what `happened` in the pods, and `loops`:
/// the one about the whole cluster's memory first, then the nearly full
/// machines in the machines' order, then the one about every machine
/// with pods over their memory requests, then a count per workload
/// rule, then the unlabelled images and the unreadable ones, then the
/// earlier restarts, then each pattern's, then each loop.
pub fn find(
    machines: &[Machine],
    workloads: impl IntoIterator<Item = (WorkloadFinding, Linked)>,
    images: Census,
    happened: Happened,
    loops: Vec<Loop>,
) -> Vec<Finding> {
    let cluster = memory::requesting_no_memory(machines);
    let nearly_full = machines.iter().filter_map(nearly_full);
    let over_memory = memory::over_memory_requests(machines);
    let counts = workloads::counts(workloads)
        .into_iter()
        .map(|(rule, workloads)| Finding::Workloads { rule, workloads });
    cluster
        .into_iter()
        .chain(nearly_full)
        .chain(over_memory)
        .chain(counts)
        .chain((!images.gaps.is_empty()).then_some(Finding::Unlabelled(images.gaps)))
        .chain((!images.unreadable.is_empty()).then_some(Finding::Unreadable(images.unreadable)))
        .chain(restarts::finding(happened.restarted))
        .chain(happened.patterns)
        .chain(loops.into_iter().map(Finding::Loop))
        .collect()
}

fn nearly_full(machine: &Machine) -> Option<Finding> {
    let fill = |requested, allocatable, used| {
        let fill = Fill {
            requested,
            allocatable,
            used,
        };
        fill.nearly_full().then_some(fill)
    };
    let cpu = fill(
        machine.requested.cpu,
        machine.allocatable.cpu,
        machine.used.cpu,
    );
    let memory = fill(
        machine.requested.memory,
        machine.allocatable.memory,
        machine.used.memory,
    );
    (cpu.is_some() || memory.is_some()).then(|| Finding::NearlyFull {
        machine: machine.name.clone(),
        cpu,
        memory,
    })
}

#[cfg(any(test, feature = "fixtures"))]
pub mod fixtures {
    use transect_ground::machines::{Amount, BYTES_PER_GI, Machine, PodMemory, Resources};
    use transect_observe::usage::Used;

    pub const BYTES_PER_MI: f64 = (1u64 << 20) as f64;

    pub fn amount(text: &str) -> Amount {
        Amount::parse(text).unwrap()
    }

    /// A ready machine with 4 cores and 16 Gi allocatable, its pods
    /// requesting `cpu` and `memory`, and no usage reading.
    pub fn machine(name: &str, cpu: &str, memory: &str) -> Machine {
        Machine {
            name: name.into(),
            control_plane: false,
            etcd: false,
            ready: true,
            allocatable: Resources {
                cpu: amount("4"),
                memory: amount("16Gi"),
            },
            requested: Resources {
                cpu: amount(cpu),
                memory: amount(memory),
            },
            used: Used::default(),
            pods: 1,
            here: None,
            lost: false,
            really_lost: false,
            lost_minutes: None,
            held: Vec::new(),
            pod_memory: Vec::new(),
            alerted: None,
        }
    }

    /// The machine with pods that each use `used_mi` Mi of memory and
    /// request `requested`, as Prometheus reads them.
    pub fn with_pods(machine: Machine, pods: &[(f64, &str)]) -> Machine {
        let pod_memory = pods
            .iter()
            .enumerate()
            .map(|(index, (used_mi, requested))| PodMemory {
                namespace: "shop".into(),
                name: format!("web-{index}"),
                used: Some(used_mi * BYTES_PER_MI),
                requested: amount(requested),
            })
            .collect();
        Machine {
            pods: pods.len(),
            pod_memory,
            ..machine
        }
    }

    /// The machine with `count` more pods that request no memory and
    /// that Prometheus has no reading for.
    pub fn with_unread_pods(machine: Machine, count: usize) -> Machine {
        let unread = (0..count).map(|index| PodMemory {
            namespace: "shop".into(),
            name: format!("unread-{index}"),
            used: None,
            requested: Amount::default(),
        });
        let pod_memory: Vec<_> = machine.pod_memory.into_iter().chain(unread).collect();
        Machine {
            pods: pod_memory.len(),
            pod_memory,
            ..machine
        }
    }

    /// The machine as Prometheus reads it: `cpu` cores and `memory_gi`
    /// Gi in use.
    pub fn using(machine: Machine, cpu: f64, memory_gi: f64) -> Machine {
        Machine {
            used: Used {
                cpu: Some(cpu),
                memory: Some(memory_gi * BYTES_PER_GI),
            },
            ..machine
        }
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::fixtures::*;
    use super::*;

    fn names(findings: &[Finding]) -> Vec<(&str, String)> {
        findings
            .iter()
            .map(|finding| match finding {
                Finding::NearlyFull { machine, .. } => ("nearly full", machine.clone()),
                Finding::OverMemoryRequests { machines } => {
                    let names: Vec<&str> = machines.iter().map(|m| m.machine.as_str()).collect();
                    ("over memory", names.join(" "))
                }
                Finding::RequestNoMemory { .. } => ("no memory request", "cluster".into()),
                Finding::Workloads { rule, workloads } => {
                    ("workloads", format!("{rule:?} {}", workloads.len()))
                }
                Finding::Unlabelled(gaps) => ("unlabelled", gaps[0].registry.clone()),
                Finding::Unreadable(found) => ("unreadable", found[0].registry.clone()),
                Finding::RestartedEarlier(found) => ("restarted", found.len().to_string()),
                Finding::Logged { pattern, .. } => ("logged", pattern.clone()),
                Finding::MachineLogs { .. } => ("machine logs", "cluster".into()),
                Finding::Loop(found) => ("loop", found.namespaces.join(" ")),
            })
            .collect()
    }

    /// Which resources a nearly full finding names.
    fn full(finding: Option<Finding>) -> Option<(bool, bool)> {
        match finding? {
            Finding::NearlyFull { cpu, memory, .. } => Some((cpu.is_some(), memory.is_some())),
            _ => None,
        }
    }

    #[rstest]
    #[case::cpu_past_the_line("3.6", "1Gi", true, false)]
    #[case::memory_past_the_line("1", "14Gi", false, true)]
    #[case::both_past_the_line("3.9", "15Gi", true, true)]
    #[case::cpu_at_the_line("3.4", "1Gi", false, false)]
    #[case::memory_at_the_line("1", "13.6Gi", false, false)]
    #[case::well_under("1", "4Gi", false, false)]
    fn nearly_full_names_each_resource_past_the_line(
        #[case] cpu: &str,
        #[case] memory: &str,
        #[case] cpu_full: bool,
        #[case] memory_full: bool,
    ) {
        let expected = (cpu_full || memory_full).then_some((cpu_full, memory_full));
        assert_eq!(full(nearly_full(&machine("alpha", cpu, memory))), expected);
    }

    /// One registry with an unlabelled image and one transect cannot
    /// read.
    fn census() -> Census {
        Census {
            gaps: vec![Gap {
                registry: "registry.example".into(),
                unlabelled: 1,
                read: 2,
            }],
            unreadable: vec![Unreadable {
                registry: "private.example".into(),
                images: 1,
                failure: crate::images::Failure::NeedsCredentials,
                mirror: None,
            }],
        }
    }

    #[rstest]
    fn lists_the_cluster_then_nearly_full_then_the_largest_excess_then_workloads_then_images_then_restarts_then_logs_then_loops()
     {
        let machines = [
            with_pods(machine("alpha", "1", "1Gi"), &[(1024.0, "512Mi")]),
            machine("beta", "3.9", "1Gi"),
            with_pods(machine("gamma", "4", "1Gi"), &[(2048.0, "512Mi")]),
            with_pods(machine("delta", "1", "1Gi"), &[(100.0, "0")]),
        ];
        let unguarded = |rule| {
            let object = Object::new("apps", "Deployment", Some("shop"), "web");
            let linked = Linked { object, href: None };
            (WorkloadFinding { rule, replicas: 2 }, linked)
        };
        let workloads = [
            unguarded(Rule::NoDisruptionBudget),
            unguarded(Rule::NoDisruptionBudget),
        ];
        let loops =
            transect_graph::edges::Graph::new([transect_graph::edges::loop_fixtures::forge_loop()])
                .loops();
        let earlier = transect_observe::health::Earlier {
            workload: Object::new("apps", "Deployment", Some("shop"), "web"),
            container: "app".into(),
            pods: 1,
            minutes_ago: 90,
            clean: false,
        };
        let happened = Happened {
            patterns: vec![Finding::Logged {
                pattern: "probe".into(),
                meaning: "A probe.".into(),
                window: "1h".into(),
                namespaces: Vec::new(),
            }],
            restarted: vec![Restarted {
                earlier,
                href: None,
            }],
        };
        assert_eq!(
            names(&find(&machines, workloads, census(), happened, loops)),
            vec![
                ("no memory request", "cluster".into()),
                ("nearly full", "beta".into()),
                ("nearly full", "gamma".into()),
                ("over memory", "gamma alpha".into()),
                ("workloads", "NoDisruptionBudget 2".into()),
                ("unlabelled", "registry.example".into()),
                ("unreadable", "private.example".into()),
                ("restarted", "1".into()),
                ("logged", "probe".into()),
                ("loop", "deploy forge".into()),
            ]
        );
    }

    #[rstest]
    #[case::some_left("4", "3640m", "360m")]
    #[case::over_allocatable("4", "4500m", "0")]
    fn free_is_what_is_left(
        #[case] allocatable: &str,
        #[case] requested: &str,
        #[case] free: &str,
    ) {
        let fill = Fill {
            requested: amount(requested),
            allocatable: amount(allocatable),
            used: None,
        };
        assert_eq!(fill.free(), amount(free));
    }
}
