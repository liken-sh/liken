//! Combines the restarts of one container in several pods of a workload
//! into one reason. A `DaemonSet` container that restarts on every
//! machine at once would otherwise fill "Now" with a near-identical
//! line for each pod, and the number of pods is more useful than their
//! names.

use std::collections::{BTreeMap, HashMap};

use super::Reason;
use super::rolls::AfterRoll;
use transect_graph::edges::Object;

/// A `Reason::Restarting` for one pod, with the pod's namespace and the
/// top owner that the restart counts toward.
pub struct Restarted {
    pub namespace: String,
    pub owner: Object,
    pub reason: Reason,
}

/// The reasons for `restarts`, each with its namespace. A restart stays
/// its own reason when its pod is the only pod of its owner where that
/// container restarted. Otherwise the restarts of an owner's container
/// become one `Reason::RestartedAcross`. `running` is the number of
/// pods that each top owner runs, and `after` holds the rollout, if
/// any, that each owner's container restarted after.
pub fn group(
    restarts: Vec<Restarted>,
    running: &HashMap<Object, usize>,
    after: &BTreeMap<(Object, String), AfterRoll>,
) -> Vec<(String, Reason)> {
    let mut groups: BTreeMap<(String, Object, String), Vec<Reason>> = BTreeMap::new();
    for restarted in restarts {
        let Reason::Restarting { container, .. } = &restarted.reason else {
            continue;
        };
        let key = (restarted.namespace, restarted.owner, container.clone());
        groups.entry(key).or_default().push(restarted.reason);
    }
    groups
        .into_iter()
        .map(|((namespace, owner, container), mut reasons)| {
            if reasons.len() == 1 {
                return (namespace, reasons.remove(0));
            }
            let pods = reasons.len();
            let of = running.get(&owner).copied().unwrap_or(0).max(pods);
            let after = after.get(&(owner.clone(), container.clone())).cloned();
            let reason = Reason::RestartedAcross {
                workload: owner,
                container,
                pods,
                of,
                minutes_ago: reasons.iter().filter_map(minutes_ago).min().unwrap_or(0),
                clean: reasons
                    .iter()
                    .all(|reason| matches!(reason, Reason::Restarting { clean: true, .. })),
                after,
            };
            (namespace, reason)
        })
        .collect()
}

fn minutes_ago(reason: &Reason) -> Option<i64> {
    match reason {
        Reason::Restarting { minutes_ago, .. } => Some(*minutes_ago),
        _ => None,
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn daemon_set(name: &str) -> Object {
        Object::new("apps", "DaemonSet", Some("audio"), name)
    }

    fn restart(
        owner: &str,
        pod: &str,
        container: &str,
        minutes_ago: i64,
        clean: bool,
    ) -> Restarted {
        Restarted {
            namespace: "audio".into(),
            owner: daemon_set(owner),
            reason: Reason::Restarting {
                pod: pod.into(),
                container: container.into(),
                restarts: 1,
                minutes_ago,
                clean,
            },
        }
    }

    fn running() -> HashMap<Object, usize> {
        HashMap::from([(daemon_set("speaker"), 10), (daemon_set("radio"), 3)])
    }

    fn lines(restarts: Vec<Restarted>) -> Vec<String> {
        group(restarts, &running(), &BTreeMap::new())
            .into_iter()
            .map(|(_, reason)| reason.to_string())
            .collect()
    }

    #[rstest]
    #[case::one_pod_keeps_its_own_line(
        vec![restart("speaker", "speaker-a", "mixer", 3, false)],
        vec!["mixer in speaker-a restarted 3m ago, 1 restart in all"]
    )]
    #[case::several_pods_combine_into_one_line(
        vec![
            restart("speaker", "speaker-a", "mixer", 13, false),
            restart("speaker", "speaker-b", "mixer", 15, false),
            restart("speaker", "speaker-c", "mixer", 14, true),
        ],
        vec!["mixer restarted in 3 of 10 speaker pods, 13m ago"]
    )]
    #[case::all_clean_says_so(
        vec![
            restart("speaker", "speaker-a", "mixer", 2, true),
            restart("speaker", "speaker-b", "mixer", 5, true),
        ],
        vec!["mixer exited cleanly and restarted in 2 of 10 speaker pods, 2m ago"]
    )]
    #[case::containers_and_owners_stay_apart(
        vec![
            restart("speaker", "speaker-a", "mixer", 2, false),
            restart("speaker", "speaker-b", "session", 2, false),
            restart("radio", "radio-a", "mixer", 4, false),
            restart("radio", "radio-b", "mixer", 6, false),
        ],
        vec![
            "mixer restarted in 2 of 3 radio pods, 4m ago",
            "mixer in speaker-a restarted 2m ago, 1 restart in all",
            "session in speaker-b restarted 2m ago, 1 restart in all",
        ]
    )]
    fn restarts_combine_by_owner_and_container(
        #[case] restarts: Vec<Restarted>,
        #[case] expected: Vec<&str>,
    ) {
        assert_eq!(lines(restarts), expected);
    }

    #[rstest]
    fn a_combined_line_belongs_to_its_workload_and_warns() {
        let restarts = vec![
            restart("speaker", "speaker-a", "mixer", 2, true),
            restart("speaker", "speaker-b", "mixer", 5, true),
        ];
        let (_, reason) = group(restarts, &running(), &BTreeMap::new()).remove(0);
        assert_eq!(reason.subject("audio"), daemon_set("speaker"));
        assert_eq!(reason.level(), super::super::Level::Warning);
    }
}
