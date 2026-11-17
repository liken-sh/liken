//! What changed in the cluster over the last day, newest first: the
//! rollouts, the Jobs that ran, the revisions that GitOps appliers
//! applied, the containers that restarted, the machines whose
//! conditions changed, and the alerts that started firing.
//!
//! transect keeps no history, so every entry comes from a time that the
//! cluster's objects still record: the creation of a `ReplicaSet`, a
//! `ControllerRevision`, or a `Job`, a `CronJob`'s last schedule, a
//! source's fetch, a container's last termination, a `Node` condition's
//! last transition, and an alert's start.  When the object of a change
//! is gone, such as a pod deleted after it restarted, the change has no
//! entry.
//!
//! A restart that follows a rollout of the same workload within
//! `FOLLOWS_MINUTES` is listed under that rollout, because a reader who
//! looks at a rollout needs first to see whether the new revision
//! crashes.

mod page;
mod read;
mod sentence;

use k8s_openapi::jiff::{SignedDuration, Timestamp};

use crate::forge::Commit;
pub use page::{ChangedPage, Listed};
pub use read::{Inputs, Recorded, read};
use transect_graph::edges::Object;

/// How far back the list reaches.
pub const WINDOW_HOURS: i64 = 24;

/// How long after a rollout a restart of the same workload is listed
/// under the rollout.  The time is long enough for a new pod to start,
/// crash, and restart once or twice under the kubelet's back-off.
pub const FOLLOWS_MINUTES: i64 = 10;

/// How many entries the sidebar shows.  `/changed` shows the rest.
pub const SIDEBAR: usize = 8;

/// One entry of the list.
#[derive(Clone, Debug, PartialEq)]
pub struct Change {
    pub at: Timestamp,
    pub what: What,
    /// The page of the object that changed, which the entry links to.
    pub href: Option<String>,
    /// The restarts that followed a rollout, oldest first.
    pub after: Vec<Change>,
}

#[derive(Clone, Debug, PartialEq)]
pub enum What {
    /// A new `ReplicaSet` or `ControllerRevision` of `workload`.
    Rolled {
        workload: Object,
        revision: Option<String>,
    },
    /// A `Job` that no `CronJob` started.
    Ran { job: Object },
    /// A `CronJob`'s last scheduled run, and whether it succeeded, or
    /// `None` while it runs.
    Scheduled {
        cron_job: Object,
        succeeded: Option<bool>,
    },
    /// Appliers that applied one revision of one source, with the ref
    /// that the revision names.
    Applied {
        source: Option<Object>,
        revision: String,
        reference: String,
        commit: Option<Commit>,
        appliers: Vec<Object>,
    },
    /// Containers of `workload` that restarted, the latest last.
    Restarted {
        workload: Object,
        restarts: Vec<Restart>,
    },
    /// The conditions of a machine that changed within a minute of each
    /// other, as all of them change when the machine reboots.
    Node { node: String, moved: Vec<Moved> },
    /// Alerts of one name about the same subject that started firing.
    Alert {
        name: String,
        about: String,
        count: usize,
    },
}

/// One condition of a machine, at its new status.
#[derive(Clone, Debug, PartialEq)]
pub struct Moved {
    pub condition: String,
    pub status: String,
    pub reason: Option<String>,
}

/// One container's last termination before its restart.
#[derive(Clone, Debug, PartialEq)]
pub struct Restart {
    pub pod: String,
    pub container: String,
    pub reason: Option<String>,
    pub exit_code: Option<i32>,
    pub at: Timestamp,
}

/// `changes` and `restarts` within the window before `now`, newest
/// first.  Each restart that followed a rollout of its workload is
/// listed under that rollout, and the other restarts are grouped into
/// one entry per workload.
pub fn arrange(
    changes: Vec<Change>,
    restarts: Vec<(Object, Restart, Option<String>)>,
    now: Timestamp,
) -> Vec<Change> {
    let since = now - SignedDuration::from_hours(WINDOW_HOURS);
    let within = |at: Timestamp| at >= since;
    let mut changes: Vec<Change> = changes.into_iter().filter(|c| within(c.at)).collect();
    let mut left: Vec<(Object, Restart, Option<String>)> = Vec::new();
    for (workload, restart, href) in restarts.into_iter().filter(|r| within(r.1.at)) {
        let follows = SignedDuration::from_mins(FOLLOWS_MINUTES);
        let rollout = changes.iter_mut().filter(|change| {
            matches!(&change.what, What::Rolled { workload: rolled, .. } if *rolled == workload)
                && change.at <= restart.at
                && restart.at <= change.at + follows
        });
        match rollout.max_by_key(|change| change.at) {
            Some(rollout) => rollout.after.push(Change {
                at: restart.at,
                what: What::Restarted {
                    workload,
                    restarts: vec![restart],
                },
                href,
                after: Vec::new(),
            }),
            None => left.push((workload, restart, href)),
        }
    }
    for change in &mut changes {
        change.after.sort_by_key(|after| after.at);
    }
    changes.extend(by_workload(left));
    changes.sort_by(|a, b| b.at.cmp(&a.at).then_with(|| a.text().cmp(&b.text())));
    changes
}

/// One entry per workload for the restarts that followed no rollout,
/// at the latest of them.
fn by_workload(restarts: Vec<(Object, Restart, Option<String>)>) -> Vec<Change> {
    let mut grouped: Vec<Change> = Vec::new();
    for (workload, restart, href) in restarts {
        let same = grouped.iter_mut().find(
            |change| matches!(&change.what, What::Restarted { workload: w, .. } if *w == workload),
        );
        match same {
            Some(change) => {
                change.at = change.at.max(restart.at);
                if let What::Restarted { restarts, .. } = &mut change.what {
                    restarts.push(restart);
                    restarts.sort_by_key(|restart| restart.at);
                }
            }
            None => grouped.push(Change {
                at: restart.at,
                what: What::Restarted {
                    workload,
                    restarts: vec![restart],
                },
                href,
                after: Vec::new(),
            }),
        }
    }
    grouped
}

/// The entries that the sidebar shows: the newest entries except the
/// scheduled runs of a `CronJob`.  Those runs can occur every few
/// minutes, so they would push every other change out of a short list.
pub fn for_sidebar(changes: &[Change]) -> Vec<Change> {
    let shown = changes
        .iter()
        .filter(|change| !matches!(change.what, What::Scheduled { .. }));
    shown.take(SIDEBAR).cloned().collect()
}

#[cfg(test)]
mod tests;
