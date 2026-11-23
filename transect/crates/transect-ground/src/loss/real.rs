//! Machines that are really lost, because their `Node`'s `Ready`
//! condition is not `True`.  The cluster page shows their loss before
//! the reader selects any machine, because a reader who opens the page
//! during an outage needs that first.  A cordoned machine that is still
//! ready keeps running its pods, so it is not lost.

use std::sync::Arc;

use k8s_openapi::api::core::v1::Node;
use k8s_openapi::jiff::Timestamp;

use super::Lost;

/// A machine that is not ready, and for how long.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct NotReadyMachine {
    pub machine: String,
    /// Seconds since its `Ready` condition last changed, or `None` when
    /// the `Node` has no `Ready` condition.
    pub seconds: Option<i64>,
}

/// The machines of `nodes` that are not ready at `now`, in name order.
pub fn not_ready(nodes: &[Arc<Node>], now: Timestamp) -> Vec<NotReadyMachine> {
    let mut gone: Vec<NotReadyMachine> = nodes
        .iter()
        .filter_map(|node| {
            let machine = node.metadata.name.clone()?;
            let conditions = node.status.as_ref().and_then(|s| s.conditions.as_ref());
            let mut conditions = conditions.into_iter().flatten();
            let ready = conditions.find(|c| c.type_ == "Ready");
            if ready.is_some_and(|c| c.status == "True") {
                return None;
            }
            let since = ready.and_then(|c| c.last_transition_time.as_ref());
            let seconds = since.map(|since| (now.as_second() - since.0.as_second()).max(0));
            Some(NotReadyMachine { machine, seconds })
        })
        .collect();
    gone.sort_by(|a, b| a.machine.cmp(&b.machine));
    gone
}

/// How many of etcd's members remain after a loss, and how many there
/// are in all.  The Kubernetes API does not list etcd's members, so
/// transect counts the machines with the `ETCD_LABEL` that k3s sets.
/// When no machine has that label, transect counts each control-plane
/// machine as a member, and the sidebar marks the count as a guess.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct Majority {
    pub remain: usize,
    pub of: usize,
    pub counted: Counted,
}

/// Which machines a majority counts as etcd's members.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Counted {
    /// Those with the etcd role label.
    Etcd,
    /// Each control-plane machine, which is a guess.
    ControlPlane,
}

impl Majority {
    /// The member count after the loss of `lost`.  Each item of
    /// `machines` is a machine's name, whether it is in the control
    /// plane, and whether it has the etcd role label.  Returns `None`
    /// when the loss includes no member.
    pub fn of<'a>(
        machines: impl IntoIterator<Item = (&'a str, bool, bool)>,
        lost: &Lost,
    ) -> Option<Self> {
        let machines: Vec<(&str, bool, bool)> = machines.into_iter().collect();
        let labelled = machines.iter().any(|(_, _, etcd)| *etcd);
        let counted = match labelled {
            true => Counted::Etcd,
            false => Counted::ControlPlane,
        };
        let members: Vec<&str> = machines
            .iter()
            .filter(|(_, control_plane, etcd)| match counted {
                Counted::Etcd => *etcd,
                Counted::ControlPlane => *control_plane,
            })
            .map(|(name, _, _)| *name)
            .collect();
        let taken = members.iter().filter(|name| lost.contains(name)).count();
        (taken > 0).then_some(Self {
            remain: members.len() - taken,
            of: members.len(),
            counted,
        })
    }

    /// Whether more than half of the members remain, which etcd needs
    /// to keep serving.
    pub fn has_quorum(&self) -> bool {
        self.remain * 2 > self.of
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    /// A Node named `name`, cordoned when `cordoned`, with `ready` as its
    /// Ready condition's status, or no Ready condition when `ready` is
    /// `None`.
    fn node(name: &str, cordoned: bool, ready: Option<&str>) -> Arc<Node> {
        let conditions = match ready {
            Some(status) => format!(
                "[{{type: Ready, status: '{status}', lastTransitionTime: '2026-01-01T00:00:00Z'}}]"
            ),
            None => "[{type: MemoryPressure, status: 'False'}]".into(),
        };
        Arc::new(
            serde_yaml_ng::from_str(&format!(
                "{{metadata: {{name: {name}}}, spec: {{unschedulable: {cordoned}}}, status: {{conditions: {conditions}}}}}"
            ))
            .unwrap(),
        )
    }

    fn now() -> Timestamp {
        "2026-01-01T00:03:20Z".parse().unwrap()
    }

    #[rstest]
    #[case::ready(node("alpha", false, Some("True")), None)]
    #[case::cordoned_but_ready(node("alpha", true, Some("True")), None)]
    #[case::not_ready(node("alpha", false, Some("False")), Some(Some(200)))]
    #[case::unknown(node("alpha", false, Some("Unknown")), Some(Some(200)))]
    #[case::cordoned_and_unknown(node("alpha", true, Some("Unknown")), Some(Some(200)))]
    #[case::no_ready_condition(node("alpha", false, None), Some(None))]
    fn a_machine_is_lost_when_it_is_not_ready(
        #[case] node: Arc<Node>,
        #[case] seconds: Option<Option<i64>>,
    ) {
        let expected = seconds.map(|seconds| NotReadyMachine {
            machine: "alpha".into(),
            seconds,
        });
        assert_eq!(
            not_ready(&[node], now()),
            expected.into_iter().collect::<Vec<_>>()
        );
    }

    #[rstest]
    fn lists_every_lost_machine_by_name() {
        let nodes = [
            node("gamma", false, Some("Unknown")),
            node("beta", false, Some("True")),
            node("alpha", false, Some("False")),
        ];
        let names: Vec<String> = not_ready(&nodes, now())
            .into_iter()
            .map(|g| g.machine)
            .collect();
        assert_eq!(names, vec!["alpha", "gamma"]);
    }

    /// Three control-plane machines and two workers.  When `labelled` is
    /// true, the three control-plane machines and the worker delta have
    /// the etcd role label.
    fn machines(labelled: bool) -> Vec<(&'static str, bool, bool)> {
        vec![
            ("alpha", true, labelled),
            ("beta", true, labelled),
            ("gamma", true, labelled),
            ("delta", false, labelled),
            ("worker", false, false),
        ]
    }

    #[rstest]
    #[case::a_worker(false, &["worker"], None)]
    #[case::one_of_three(false, &["alpha"], Some((2, 3, true, Counted::ControlPlane)))]
    #[case::two_of_three(false, &["alpha", "beta"], Some((1, 3, false, Counted::ControlPlane)))]
    #[case::one_of_four_labelled(true, &["alpha"], Some((3, 4, true, Counted::Etcd)))]
    #[case::two_of_four_labelled(true, &["alpha", "delta"], Some((2, 4, false, Counted::Etcd)))]
    fn counts_the_members_that_remain(
        #[case] labelled: bool,
        #[case] lost: &[&str],
        #[case] expected: Option<(usize, usize, bool, Counted)>,
    ) {
        let machines = machines(labelled);
        let names = machines.iter().map(|(name, _, _)| *name);
        let lost = Lost::of(lost.iter().copied(), names);
        let found = Majority::of(machines.clone(), &lost);
        let found = found.map(|m| (m.remain, m.of, m.has_quorum(), m.counted));
        assert_eq!(found, expected);
    }
}
