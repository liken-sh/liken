//! Reads the times of change from the cluster's objects in their
//! current state.  Each reader is a pure function of the objects that
//! it reads.

use std::sync::Arc;

use k8s_openapi::api::apps::v1::{ControllerRevision, ReplicaSet};
use k8s_openapi::api::batch::v1::{CronJob, Job};
use k8s_openapi::api::core::v1::{Node, Pod};
use k8s_openapi::apimachinery::pkg::apis::meta::v1::{ObjectMeta, OwnerReference};
use k8s_openapi::jiff::{SignedDuration, Timestamp};
use kube::api::PartialObjectMeta;

use super::{Change, Moved, Restart, WINDOW_HOURS, What, arrange};
use crate::forge::{Commit, Forges, commit};
use crate::gitops::Revision;
use transect_cluster::ownership::Ownership;
use transect_graph::edges::{Object, Plurals};
use transect_observe::alerts::Firing;

/// The annotation where the `Deployment` controller writes the revision
/// number of each of its ReplicaSets.
const REVISION: &str = "deployment.kubernetes.io/revision";

/// The objects that the list is read from.
pub struct Inputs<'a> {
    pub replica_sets: &'a [Arc<PartialObjectMeta<ReplicaSet>>],
    pub controller_revisions: &'a [Arc<PartialObjectMeta<ControllerRevision>>],
    pub jobs: &'a [Arc<PartialObjectMeta<Job>>],
    pub cron_jobs: &'a [Arc<CronJob>],
    pub pods: &'a [Arc<Pod>],
    pub nodes: &'a [Arc<Node>],
    /// Each applier's last applied revision, and its time.
    pub revisions: Vec<Revision>,
    pub ownership: &'a Ownership,
    pub plurals: &'a Plurals,
    pub forges: &'a Forges,
}

/// Every change that the objects record, before `arranged` applies the
/// window and adds the alerts that are firing.  None of it depends on
/// the time, so transect reads it once for each reading of the cluster
/// and uses it for every render.
#[derive(Debug, Default)]
pub struct Recorded {
    changes: Vec<Change>,
    restarts: Vec<(Object, Restart, Option<String>)>,
}

impl Recorded {
    /// The list as of `now`, with a line for each alert in `firing`.
    pub fn arranged(&self, firing: &[Firing], now: Timestamp) -> Vec<Change> {
        let since = now - SignedDuration::from_hours(WINDOW_HOURS);
        let changes = self.changes.iter().filter(|change| change.at >= since);
        let mut changes: Vec<Change> = changes.cloned().collect();
        changes.extend(firing.iter().filter_map(|firing| alert(firing, now)));
        let restarts = self.restarts.iter().filter(|restart| restart.1.at >= since);
        arrange(changes, restarts.cloned().collect(), now)
    }
}

/// Every change the objects in `inputs` still record.
pub fn read(inputs: &Inputs) -> Recorded {
    // A workload that a custom resource owns has no page of its own, so
    // the entry links to the page of its top owner, which shows it.
    let link = |object: &Object| {
        let top = inputs.ownership.top_owner(object);
        transect_graph::links::link(&top, inputs.plurals)
    };
    let mut changes = Vec::new();
    let rolled = inputs.replica_sets.iter().map(|rs| &rs.metadata);
    let revisions = inputs.controller_revisions.iter().map(|cr| &cr.metadata);
    for meta in rolled.chain(revisions) {
        if let Some(change) = rollout(meta, &link) {
            changes.push(change);
        }
    }
    for job in inputs.jobs {
        if let Some(change) = ran(&job.metadata, inputs.ownership, &link) {
            changes.push(change);
        }
    }
    changes.extend(inputs.cron_jobs.iter().filter_map(|c| scheduled(c, &link)));
    changes.extend(applied(&inputs.revisions, inputs.forges, &link));
    changes.extend(inputs.nodes.iter().flat_map(|node| conditions(node)));
    let restarts = inputs
        .pods
        .iter()
        .flat_map(|pod| restarts(pod, inputs.ownership, inputs.plurals));
    Recorded {
        changes,
        restarts: restarts.collect(),
    }
}

fn owner(meta: &ObjectMeta) -> Option<&OwnerReference> {
    let owners = meta.owner_references.as_ref()?;
    let controller = owners.iter().find(|o| o.controller == Some(true));
    controller.or(owners.first())
}

fn object_of(reference: &OwnerReference, namespace: Option<&str>) -> Object {
    let group = reference.api_version.split_once('/').map_or("", |(g, _)| g);
    Object::new(group, &reference.kind, namespace, &reference.name)
}

/// The creation of a `ReplicaSet` or a `ControllerRevision`, as a
/// rollout of its owner.
pub fn rollout(meta: &ObjectMeta, link: &impl Fn(&Object) -> Option<String>) -> Option<Change> {
    let at = meta.creation_timestamp.as_ref()?.0;
    let workload = object_of(owner(meta)?, meta.namespace.as_deref());
    let revision = meta.annotations.as_ref().and_then(|a| a.get(REVISION));
    Some(Change {
        at,
        href: link(&workload),
        what: What::Rolled {
            workload,
            revision: revision.cloned(),
        },
        after: Vec::new(),
    })
}

/// The creation of a `Job`, unless a `CronJob` started it, because the
/// `CronJob`'s own entry shows the runs of its schedule.
pub fn ran(
    meta: &ObjectMeta,
    ownership: &Ownership,
    link: &impl Fn(&Object) -> Option<String>,
) -> Option<Change> {
    if owner(meta).is_some_and(|owner| owner.kind == "CronJob") {
        return None;
    }
    let at = meta.creation_timestamp.as_ref()?.0;
    let job = Object::new(
        "batch",
        "Job",
        meta.namespace.as_deref(),
        meta.name.as_deref()?,
    );
    let href = link(&ownership.top_owner(&job));
    let href = href.or_else(|| Some(format!("/namespaces/{}", job.namespace.as_deref()?)));
    Some(Change {
        at,
        href,
        what: What::Ran { job },
        after: Vec::new(),
    })
}

/// A `CronJob`'s last scheduled run.
pub fn scheduled(cron_job: &CronJob, link: &impl Fn(&Object) -> Option<String>) -> Option<Change> {
    let status = cron_job.status.as_ref()?;
    let at = status.last_schedule_time.as_ref()?.0;
    let meta = &cron_job.metadata;
    let object = Object::new(
        "batch",
        "CronJob",
        meta.namespace.as_deref(),
        meta.name.as_deref()?,
    );
    let active = status.active.as_ref().is_some_and(|a| !a.is_empty());
    let succeeded = status.last_successful_time.as_ref().map(|t| t.0);
    Some(Change {
        at,
        href: link(&object),
        what: What::Scheduled {
            cron_job: object,
            succeeded: (!active).then(|| succeeded.is_some_and(|s| s >= at)),
        },
        after: Vec::new(),
    })
}

/// One entry for each source and revision that the appliers applied,
/// at the latest of their times.  The entry has the commit when the
/// revision names a git commit on a forge that a `Forge` declares.
pub fn applied(
    revisions: &[Revision],
    forges: &Forges,
    link: &impl Fn(&Object) -> Option<String>,
) -> Vec<Change> {
    let mut changes: Vec<Change> = Vec::new();
    for found in revisions {
        let same = changes.iter_mut().find(|change| {
            matches!(&change.what, What::Applied { source, revision, .. }
                if *source == found.source && *revision == found.revision)
        });
        if let Some(change) = same {
            change.at = change.at.max(found.at);
            if let What::Applied { appliers, .. } = &mut change.what {
                appliers.push(found.applier.clone());
            }
            continue;
        }
        let (reference, sha) = found.parts();
        let linked = sha
            .zip(found.url.as_deref())
            .map(|(sha, url)| commit(url, sha, forges));
        let linked = linked.filter(|c| matches!(c, Commit::Link { .. }));
        let shown = found.source.as_ref().unwrap_or(&found.applier);
        changes.push(Change {
            at: found.at,
            href: link(shown),
            what: What::Applied {
                source: found.source.clone(),
                revision: found.revision.clone(),
                reference: reference.to_string(),
                commit: linked,
                appliers: vec![found.applier.clone()],
            },
            after: Vec::new(),
        });
    }
    for change in &mut changes {
        if let What::Applied { appliers, .. } = &change.what
            && let [one] = appliers.as_slice()
        {
            change.href = link(one).or(change.href.take());
        }
    }
    changes
}

/// A `Node`'s conditions at their last transitions.  Conditions that
/// changed within a minute of each other form one entry, at the time of
/// the latest of them.
pub fn conditions(node: &Node) -> Vec<Change> {
    let Some(name) = node.metadata.name.as_deref() else {
        return Vec::new();
    };
    let found = node
        .status
        .iter()
        .flat_map(|s| s.conditions.iter().flatten());
    let mut found: Vec<(Timestamp, Moved)> = found
        .filter_map(|condition| {
            let moved = Moved {
                condition: condition.type_.clone(),
                status: condition.status.clone(),
                reason: condition.reason.clone().filter(|r| !r.is_empty()),
            };
            Some((condition.last_transition_time.as_ref()?.0, moved))
        })
        .collect();
    found.sort_by(|a, b| {
        a.0.cmp(&b.0)
            .then_with(|| a.1.condition.cmp(&b.1.condition))
    });
    let together = SignedDuration::from_mins(1);
    let mut changes: Vec<Change> = Vec::new();
    for (at, moved) in found {
        let last = changes
            .last_mut()
            .filter(|change| at <= change.at + together);
        match last {
            Some(change) => {
                change.at = at;
                if let What::Node { moved: all, .. } = &mut change.what {
                    all.push(moved);
                }
            }
            None => changes.push(Change {
                at,
                href: Some(format!("/machines/{name}")),
                what: What::Node {
                    node: name.into(),
                    moved: vec![moved],
                },
                after: Vec::new(),
            }),
        }
    }
    changes
}

/// An entry for a line of firing alerts, at the time that the earliest
/// of them started.
pub fn alert(firing: &Firing, now: Timestamp) -> Option<Change> {
    let at = now - SignedDuration::from_mins(firing.minutes?);
    Some(Change {
        at,
        href: firing.about.href().or(firing.link.clone()),
        what: What::Alert {
            name: firing.name.clone(),
            about: firing.about.text(),
            count: firing.count,
        },
        after: Vec::new(),
    })
}

/// Each container of `pod` that terminated and restarted, with its
/// workload and the pod's page.
pub fn restarts(
    pod: &Pod,
    ownership: &Ownership,
    plurals: &Plurals,
) -> Vec<(Object, Restart, Option<String>)> {
    let meta = &pod.metadata;
    let (Some(namespace), Some(name)) = (&meta.namespace, &meta.name) else {
        return Vec::new();
    };
    let object = Object::new("", "Pod", Some(namespace), name);
    let workload = ownership.top_owner(&object);
    let href = transect_graph::links::link(&object, plurals);
    let statuses = pod
        .status
        .iter()
        .flat_map(|s| s.container_statuses.iter().flatten());
    statuses
        .filter_map(|status| {
            let ended = status.last_state.as_ref()?.terminated.as_ref()?;
            let restart = Restart {
                pod: name.clone(),
                container: status.name.clone(),
                reason: ended.reason.clone().filter(|r| !r.is_empty()),
                exit_code: Some(ended.exit_code),
                at: ended.finished_at.as_ref()?.0,
            };
            Some((workload.clone(), restart, href.clone()))
        })
        .collect()
}
