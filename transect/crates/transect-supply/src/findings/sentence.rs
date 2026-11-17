//! The title and the body that the page shows for each finding.  The
//! words in these templates are the only words a finding shows, and the
//! rest of each sentence is a name or a number from the finding.

use super::memory::{HEAVY_UNREQUESTED_BYTES, OVER_REQUESTS_BYTES};
use super::{Fill, Finding, Line, Overage};
use super::{images, logged, machine_logs, restarts};
use transect_ground::machines::{BYTES_PER_GI, trimmed};

const BYTES_PER_MI: f64 = (1u64 << 20) as f64;

impl Finding {
    pub fn title(&self) -> String {
        match self {
            Self::NearlyFull { machine, .. } => format!("{machine} is nearly full of requests"),
            Self::OverMemoryRequests { machines } => match machines.as_slice() {
                [one] if one.pods == 1 => {
                    format!("{} has a pod over its memory request", one.machine)
                }
                [one] => format!("{} has pods over their memory requests", one.machine),
                _ => format!(
                    "{} machines have pods {} or more over their memory requests",
                    machines.len(),
                    mi_or_gi(OVER_REQUESTS_BYTES)
                ),
            },
            Self::RequestNoMemory { pods, .. } => {
                if *pods == 1 {
                    "1 pod requests no memory".into()
                } else {
                    format!("{pods} pods request no memory")
                }
            }
            Self::Workloads { rule, workloads } => rule.count_title(workloads.len()),
            Self::Unlabelled(gaps) => images::unlabelled_title(gaps),
            Self::Unreadable(found) => images::unreadable_title(found),
            Self::RestartedEarlier(found) => restarts::title(found),
            Self::Logged {
                pattern,
                window,
                namespaces,
                ..
            } => logged::title(pattern, window, namespaces),
            Self::MachineLogs { window, machines } => machine_logs::title(window, machines),
            Self::Loop(found) => format!("⟲ {}", found.title()),
        }
    }

    pub fn body(&self) -> String {
        match self {
            Self::NearlyFull { cpu, memory, .. } => {
                let sentences = [cpu.map(cpu_fill), memory.map(memory_fill)];
                sentences
                    .into_iter()
                    .flatten()
                    .collect::<Vec<_>>()
                    .join(" ")
            }
            Self::OverMemoryRequests { machines } => match machines.as_slice() {
                [one] => format!(
                    "{} {} {EVICTS_OVER_REQUESTS}",
                    over_requests(one.pods, one.excess),
                    threshold()
                ),
                _ => format!("{} {EVICTS_OVER_REQUESTS}", threshold()),
            },
            Self::RequestNoMemory { pods, heavy, used } => format!(
                "{} {}",
                heavy_among(*pods, *heavy, *used),
                if *pods == 1 {
                    "The scheduler places it as if it used no memory, and the kubelet evicts it first."
                } else {
                    "The scheduler places them as if they used no memory, and the kubelet evicts them first."
                }
            ),
            Self::Workloads { rule, .. } => rule.count_body().into(),
            Self::Unlabelled(_) => images::unlabelled_body(),
            Self::Unreadable(_) => images::unreadable_body().into(),
            Self::RestartedEarlier(_) => restarts::body(),
            Self::Logged { meaning, .. } => meaning.clone(),
            Self::MachineLogs { .. } => machine_logs::body().into(),
            Self::Loop(found) => found.meaning().into(),
        }
    }

    /// The lines that the page lists above a finding's body, one for each
    /// machine, registry, workload, or namespace that the finding names.
    /// A finding about memory over requests lists its machines only when
    /// there are several, because a finding about one machine names it
    /// in the title.  A finding about images lists every registry it
    /// counts, so the count for images built here gets its own line.
    pub fn lines(&self) -> Vec<Line> {
        let line = |label: &str, href: Option<String>, text: String| Line {
            label: label.into(),
            href,
            text,
            evidence: None,
        };
        match self {
            Self::Unlabelled(gaps) => gaps
                .iter()
                .map(|gap| line(&gap.registry, None, images::gap_line(gap)))
                .collect(),
            Self::Unreadable(found) => found
                .iter()
                .map(|found| line(&found.registry, None, images::unreadable_line(found)))
                .collect(),
            Self::RestartedEarlier(found) => found
                .iter()
                .map(|one| {
                    let label = named(&one.earlier.workload);
                    line(&label, one.href.clone(), restarts::line(&one.earlier))
                })
                .collect(),
            Self::MachineLogs { machines, .. } => machines
                .iter()
                .map(|one| {
                    let href = Some(machine_page(&one.machine));
                    line(&one.machine, href, machine_logs::line(one))
                })
                .collect(),
            Self::Logged { namespaces, .. } => namespaces
                .iter()
                .map(|one| Line {
                    evidence: (!one.evidence.is_empty()).then(|| one.evidence.clone()),
                    ..line(&logged::label(one), one.href.clone(), logged::line(one))
                })
                .collect(),
            Self::OverMemoryRequests { machines } if machines.len() > 1 => machines
                .iter()
                .map(|over| {
                    line(
                        &over.machine,
                        Some(machine_page(&over.machine)),
                        over_line(over),
                    )
                })
                .collect(),
            Self::Workloads { workloads, .. } => workloads
                .iter()
                .map(|found| line(&named(&found.object), found.href.clone(), String::new()))
                .collect(),
            _ => Vec::new(),
        }
    }
}

/// A workload as a line names it: "shop/web".
fn named(object: &transect_graph::edges::Object) -> String {
    match &object.namespace {
        Some(namespace) => format!("{namespace}/{}", object.name),
        None => object.name.clone(),
    }
}

/// The page that a machine's name links to, as in the machines band.
fn machine_page(machine: &str) -> String {
    format!("/machines/{machine}")
}

const EVICTS_OVER_REQUESTS: &str =
    "Under memory pressure, the kubelet evicts the pods over their requests first.";

/// The sentence that states which machines the finding lists, because the
/// finding leaves out the smaller drift that most machines show.
fn threshold() -> String {
    format!(
        "A machine is listed when its pods use {} or more past their requests, together.",
        mi_or_gi(OVER_REQUESTS_BYTES)
    )
}

/// What follows a machine's name in its line, as "1 pod, 866 Mi over".
fn over_line(over: &Overage) -> String {
    let pods = if over.pods == 1 {
        "1 pod".to_string()
    } else {
        format!("{} pods", over.pods)
    };
    format!("{pods}, {} over", mi_or_gi(over.excess))
}

fn over_requests(pods: usize, excess: f64) -> String {
    if pods == 1 {
        format!("1 pod uses {} more than it requested.", mi_or_gi(excess))
    } else {
        format!(
            "{pods} pods use {} more than they requested.",
            mi_or_gi(excess)
        )
    }
}

/// How many of the pods that request no memory are heavy, then what all
/// of them use.  The total names "all {pods}" so no reader takes it for
/// what the heavy ones alone use.
fn heavy_among(pods: usize, heavy: usize, used: f64) -> String {
    let line = mi_or_gi(HEAVY_UNREQUESTED_BYTES);
    let used = mi_or_gi(used);
    match (pods, heavy) {
        (1, _) => format!("It uses {used}."),
        (_, 1) => format!("1 of them uses {line} or more, and all {pods} use {used} together."),
        (_, _) if heavy == pods => {
            format!("Each of the {pods} uses {line} or more, and they use {used} together.")
        }
        (_, _) => {
            format!("{heavy} of them each use {line} or more, and all {pods} use {used} together.")
        }
    }
}

fn cpu_fill(fill: Fill) -> String {
    let cores = fill.allocatable.units();
    let free = fill.free().millis();
    format!(
        "{:.1} of its {} {} requested{}. {}",
        fill.requested.units(),
        trimmed(cores),
        if cores == 1.0 { "core is" } else { "cores are" },
        though_it_uses(fill.used.map(|used| format!("{used:.1}"))),
        if free > 0 {
            format!("A new pod that requests more than {free}m of CPU won't schedule there.")
        } else {
            "A new pod that requests any CPU won't schedule there.".into()
        }
    )
}

fn memory_fill(fill: Fill) -> String {
    let free = fill.free().units();
    format!(
        "{:.1} of its {} are requested{}. {}",
        fill.requested.units() / BYTES_PER_GI,
        gi(fill.allocatable.units()),
        though_it_uses(fill.used.map(|used| format!("{:.1}", used / BYTES_PER_GI))),
        if free > 0.0 {
            format!(
                "A new pod that requests more than {} of memory won't schedule there.",
                free_memory(free)
            )
        } else {
            "A new pod that requests any memory won't schedule there.".into()
        }
    )
}

fn though_it_uses(used: Option<String>) -> String {
    used.map(|used| format!(", though it uses {used}"))
        .unwrap_or_default()
}

fn gi(bytes: f64) -> String {
    format!("{:.1} Gi", bytes / BYTES_PER_GI)
}

fn mi_or_gi(bytes: f64) -> String {
    if bytes >= BYTES_PER_GI {
        gi(bytes)
    } else {
        format!("{:.0} Mi", bytes / BYTES_PER_MI)
    }
}

/// Rounded up, never down, so "more than" stays true: a pod that requests
/// more than the shown amount never fits in what is free.
fn free_memory(bytes: f64) -> String {
    if bytes >= BYTES_PER_GI {
        format!("{:.1} Gi", (bytes / BYTES_PER_GI * 10.0).ceil() / 10.0)
    } else {
        format!("{} Mi", (bytes / BYTES_PER_MI).ceil())
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::super::fixtures::{amount, machine, using, with_pods, with_unread_pods};
    use super::super::{Happened, find};
    use super::*;

    fn first(machine: transect_ground::machines::Machine) -> Finding {
        find(
            &[machine],
            [],
            Default::default(),
            Happened::default(),
            Vec::new(),
        )
        .remove(0)
    }

    #[rstest]
    #[case::cpu_with_usage(
        using(machine("alpha", "3.6", "1Gi"), 1.1, 2.0),
        "3.6 of its 4 cores are requested, though it uses 1.1. \
         A new pod that requests more than 400m of CPU won't schedule there."
    )]
    #[case::cpu_without_usage(
        machine("alpha", "3640m", "1Gi"),
        "3.6 of its 4 cores are requested. \
         A new pod that requests more than 360m of CPU won't schedule there."
    )]
    #[case::cpu_over_allocatable(
        machine("alpha", "4500m", "1Gi"),
        "4.5 of its 4 cores are requested. A new pod that requests any CPU won't schedule there."
    )]
    #[case::memory_rounds_free_up(
        machine("alpha", "1", "14.5Gi"),
        "14.5 of its 16.0 Gi are requested. \
         A new pod that requests more than 1.5 Gi of memory won't schedule there."
    )]
    #[case::memory_under_a_gi_free(
        machine("alpha", "1", "15.5Gi"),
        "15.5 of its 16.0 Gi are requested. \
         A new pod that requests more than 512 Mi of memory won't schedule there."
    )]
    #[case::both(
        using(machine("alpha", "3.9", "15Gi"), 0.5, 4.0),
        "3.9 of its 4 cores are requested, though it uses 0.5. \
         A new pod that requests more than 100m of CPU won't schedule there. \
         15.0 of its 16.0 Gi are requested, though it uses 4.0. \
         A new pod that requests more than 1.0 Gi of memory won't schedule there."
    )]
    fn nearly_full_body(#[case] machine: transect_ground::machines::Machine, #[case] body: &str) {
        let finding = first(machine);
        assert_eq!(finding.title(), "alpha is nearly full of requests");
        assert_eq!(finding.body(), body);
    }

    #[rstest]
    #[case::one_pod(
        &[(600.0, "256Mi")],
        "alpha has a pod over its memory request",
        "1 pod uses 344 Mi more than it requested. A machine is listed when its pods use \
         256 Mi or more past their requests, together. Under memory pressure, the kubelet \
         evicts the pods over their requests first."
    )]
    #[case::several_in_gi(
        &[(1024.0, "512Mi"), (1024.0, "256Mi"), (100.0, "1Gi")],
        "alpha has pods over their memory requests",
        "2 pods use 1.2 Gi more than they requested. A machine is listed when its pods use \
         256 Mi or more past their requests, together. Under memory pressure, the kubelet \
         evicts the pods over their requests first."
    )]
    fn over_memory_requests_sentences(
        #[case] pods: &[(f64, &str)],
        #[case] title: &str,
        #[case] body: &str,
    ) {
        let finding = first(with_pods(machine("alpha", "1", "1Gi"), pods));
        assert_eq!(
            (finding.title(), finding.body()),
            (title.into(), body.into())
        );
    }

    #[rstest]
    #[case::one_machine(&[("alpha", 1, 300.0)], "alpha has a pod over its memory request", &[])]
    #[case::two_machines(
        &[("beta", 1, 866.0), ("alpha", 3, 1229.0)],
        "2 machines have pods 256 Mi or more over their memory requests",
        &[("beta", "1 pod, 866 Mi over"), ("alpha", "3 pods, 1.2 Gi over")]
    )]
    fn over_memory_requests_lists_several_machines(
        #[case] machines: &[(&str, usize, f64)],
        #[case] title: &str,
        #[case] lines: &[(&str, &str)],
    ) {
        let finding = Finding::OverMemoryRequests {
            machines: machines
                .iter()
                .map(|(machine, pods, excess_mi)| Overage {
                    machine: (*machine).into(),
                    pods: *pods,
                    excess: excess_mi * BYTES_PER_MI,
                })
                .collect(),
        };
        let lines: Vec<Line> = lines
            .iter()
            .map(|(machine, line)| Line {
                label: (*machine).into(),
                href: Some(format!("/machines/{machine}")),
                text: (*line).into(),
                evidence: None,
            })
            .collect();
        assert_eq!((finding.title(), finding.lines()), (title.into(), lines));
    }

    #[rstest]
    fn several_machines_say_the_eviction_sentence_once() {
        let finding = Finding::OverMemoryRequests {
            machines: ["alpha", "beta"]
                .map(|machine| Overage {
                    machine: machine.into(),
                    pods: 1,
                    excess: 300.0 * BYTES_PER_MI,
                })
                .into(),
        };
        assert_eq!(
            finding.body(),
            "A machine is listed when its pods use 256 Mi or more past their requests, \
             together. Under memory pressure, the kubelet evicts the pods over their requests first."
        );
    }

    #[rstest]
    #[case::one_pod(
        &[(300.0, "0")],
        0,
        "1 pod requests no memory",
        "It uses 300 Mi. The scheduler places it as if it used no memory, and the \
         kubelet evicts it first."
    )]
    #[case::one_heavy(
        &[(300.0, "0"), (20.0, "0")],
        0,
        "2 pods request no memory",
        "1 of them uses 64 Mi or more, and all 2 use 320 Mi together. The scheduler \
         places them as if they used no memory, and the kubelet evicts them first."
    )]
    #[case::every_one_heavy(
        &[(300.0, "0"), (100.0, "0")],
        0,
        "2 pods request no memory",
        "Each of the 2 uses 64 Mi or more, and they use 400 Mi together. The scheduler \
         places them as if they used no memory, and the kubelet evicts them first."
    )]
    #[case::several_heavy_in_gi(
        &[(1024.0, "0"), (512.0, "0"), (8.0, "0")],
        2,
        "5 pods request no memory",
        "2 of them each use 64 Mi or more, and all 5 use 1.5 Gi together. The scheduler \
         places them as if they used no memory, and the kubelet evicts them first."
    )]
    fn request_no_memory_sentences(
        #[case] pods: &[(f64, &str)],
        #[case] unread: usize,
        #[case] title: &str,
        #[case] body: &str,
    ) {
        let alpha = with_unread_pods(with_pods(machine("alpha", "1", "1Gi"), pods), unread);
        let finding = first(alpha);
        assert_eq!(
            (finding.title(), finding.body()),
            (title.into(), body.into())
        );
    }

    #[rstest]
    fn one_core_is_singular() {
        let fill = Fill {
            requested: amount("900m"),
            allocatable: amount("1"),
            used: None,
        };
        assert_eq!(
            cpu_fill(fill),
            "0.9 of its 1 core is requested. \
             A new pod that requests more than 100m of CPU won't schedule there."
        );
    }

    #[rstest]
    fn a_loop_draws_the_cycle_its_members_close() {
        let found =
            transect_graph::edges::Graph::new([transect_graph::edges::loop_fixtures::forge_loop()])
                .loops();
        let finding = Finding::Loop(found[0].clone());
        let cycles: Vec<Vec<(String, String)>> = finding
            .as_loop()
            .unwrap()
            .cycles()
            .iter()
            .map(|cycle| {
                let steps = cycle.steps.iter();
                steps
                    .map(|step| (step.member.clone(), step.verb()))
                    .collect()
            })
            .collect();
        let step = |member: &str, verb: &str| (member.to_owned(), verb.to_owned());
        assert_eq!(finding.title(), "⟲ A loop: deploy and forge");
        assert_eq!(
            cycles,
            vec![vec![
                step("Deployment forge/forge", "reconciles from"),
                step("Tree forge/forge", "reconciles from"),
                step("Repo deploy/fleet", "depends on"),
            ]]
        );
    }
}
