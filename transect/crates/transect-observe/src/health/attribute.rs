//! Groups a namespace's health by workload.  The pod and workload rules
//! read only Kubernetes's own kinds, so each reason goes to the top owner
//! of the object it is about.  A reason about the `StatefulSet` that a
//! custom resource runs, or about one of its pods, shows on that custom
//! resource.

use std::collections::HashMap;

use super::{Health, Kind, Origin, Reason};
use transect_cluster::ownership::Ownership;
use transect_graph::edges::Object;

impl Reason {
    /// The page that a card links this reason's line to: the pod's page
    /// for a reason about a pod, and otherwise the page of the top owner
    /// of the reason's subject.  Returns `None` when that object is
    /// `own`, the object whose card this is, or when `page` has no page
    /// for it.
    pub fn page(
        &self,
        namespace: &str,
        ownership: &Ownership,
        own: &Object,
        page: impl Fn(&Object) -> Option<String>,
    ) -> Option<String> {
        let subject = self.subject(namespace);
        let shown = match subject.kind.as_str() {
            "Pod" => subject,
            _ => ownership.top_owner(&subject),
        };
        (&shown != own).then(|| page(&shown)).flatten()
    }

    /// The object the reason is about, in `namespace`.
    pub fn subject(&self, namespace: &str) -> Object {
        match self {
            Self::NoReadyReplicas { workload, .. }
            | Self::Starting { workload, .. }
            | Self::NotReady { workload, .. }
            | Self::LastRunFailed { workload } => {
                let (group, kind) = workload.kind.group_and_kind();
                Object::new(group, kind, Some(namespace), &workload.name)
            }
            Self::CrashLooping { pod, .. }
            | Self::Restarting { pod, .. }
            | Self::PodPending { pod, .. } => Object::new("", "Pod", Some(namespace), pod),
            Self::RestartedAcross { workload, .. }
            | Self::Condition { workload, .. }
            | Self::Unfollowed { workload, .. }
            | Self::LoggedErrors { workload, .. } => workload.clone(),
            // Lines from a pod that no longer exists, or from no pod, have
            // no page to link to, so they count for the namespace.
            Self::Matched { origin, .. } => match origin {
                Origin::Workload(workload) => workload.clone(),
                Origin::GonePod(_) | Origin::NoPod => Object::new("", "Namespace", None, namespace),
            },
            Self::Alert { subject, .. } => subject.clone(),
        }
    }
}

impl Kind {
    fn group_and_kind(self) -> (&'static str, &'static str) {
        match self {
            Self::Deployment => ("apps", "Deployment"),
            Self::StatefulSet => ("apps", "StatefulSet"),
            Self::DaemonSet => ("apps", "DaemonSet"),
            Self::CronJob => ("batch", "CronJob"),
        }
    }
}

/// The health of each of `namespace`'s workloads that has a reason,
/// keyed by top owner.  A workload missing from the map is healthy.
pub fn by_workload(
    health: &Health,
    namespace: &str,
    ownership: &Ownership,
) -> HashMap<Object, Health> {
    let mut reasons: HashMap<Object, Vec<Reason>> = HashMap::new();
    for reason in &health.reasons {
        let owner = ownership.top_owner(&reason.subject(namespace));
        reasons.entry(owner).or_default().push(reason.clone());
    }
    reasons
        .into_iter()
        .map(|(owner, reasons)| (owner, Health::from_reasons(reasons)))
        .collect()
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::super::{Level, Workload};
    use super::*;
    use transect_cluster::ownership::Entry;
    use transect_cluster::ownership::fixtures::meta;

    fn cache_ownership() -> Ownership {
        let metas = [
            meta(
                Some("shop"),
                "cache-0",
                &[("apps/v1", "StatefulSet", "cache")],
            ),
            meta(
                Some("shop"),
                "cache",
                &[("cache.example.io/v1", "Cache", "sessions")],
            ),
        ];
        Ownership::build([
            Entry {
                group: "",
                kind: "Pod",
                meta: &metas[0],
            },
            Entry {
                group: "apps",
                kind: "StatefulSet",
                meta: &metas[1],
            },
        ])
    }

    fn not_ready(kind: Kind, name: &str) -> Reason {
        Reason::NotReady {
            workload: Workload {
                kind,
                name: name.into(),
            },
            ready: 1,
            desired: 2,
        }
    }

    fn pending(pod: &str) -> Reason {
        Reason::PodPending {
            pod: pod.into(),
            minutes: 9,
        }
    }

    fn object(group: &str, kind: &str, name: &str) -> Object {
        Object::new(group, kind, Some("shop"), name)
    }

    #[rstest]
    #[case::a_custom_kinds_stateful_set(
        not_ready(Kind::StatefulSet, "cache"),
        object("cache.example.io", "Cache", "sessions")
    )]
    #[case::a_custom_kinds_pod(pending("cache-0"), object("cache.example.io", "Cache", "sessions"))]
    #[case::an_unowned_deployment(
        not_ready(Kind::Deployment, "web"),
        object("apps", "Deployment", "web")
    )]
    fn a_reason_belongs_to_the_top_owner_of_its_subject(
        #[case] reason: Reason,
        #[case] owner: Object,
    ) {
        let health = Health::from_reasons(vec![reason]);
        let by_workload = by_workload(&health, "shop", &cache_ownership());
        assert_eq!(by_workload.keys().collect::<Vec<_>>(), vec![&owner]);
        assert_eq!(by_workload[&owner].level, Level::Warning);
    }

    #[rstest]
    fn reasons_about_one_owner_roll_up_together() {
        let health = Health::from_reasons(vec![
            not_ready(Kind::StatefulSet, "cache"),
            pending("cache-0"),
        ]);
        let by_workload = by_workload(&health, "shop", &cache_ownership());
        let sessions = object("cache.example.io", "Cache", "sessions");
        assert_eq!(by_workload.len(), 1);
        assert_eq!(by_workload[&sessions].reasons.len(), 2);
    }

    /// The page a card links `reason`'s line to, on the card of `own`,
    /// where every object has a page named for it.
    fn linked(reason: Reason, own: Object) -> Option<String> {
        let page = |object: &Object| Some(format!("/{}/{}", object.kind, object.name));
        reason.page("shop", &cache_ownership(), &own, page)
    }

    #[rstest]
    #[case::a_pod_on_its_namespaces_card(
        pending("cache-0"),
        Object::new("", "Namespace", None, "shop"),
        Some("/Pod/cache-0")
    )]
    #[case::a_pod_on_its_owners_card(
        pending("cache-0"),
        object("cache.example.io", "Cache", "sessions"),
        Some("/Pod/cache-0")
    )]
    #[case::its_owner_on_another_card(
        not_ready(Kind::StatefulSet, "cache"),
        Object::new("", "Namespace", None, "shop"),
        Some("/Cache/sessions")
    )]
    #[case::the_cards_own_object(
        not_ready(Kind::StatefulSet, "cache"),
        object("cache.example.io", "Cache", "sessions"),
        None
    )]
    fn a_reasons_line_links_to_what_it_is_about_but_its_own_card(
        #[case] reason: Reason,
        #[case] own: Object,
        #[case] expected: Option<&str>,
    ) {
        assert_eq!(linked(reason, own).as_deref(), expected);
    }
}
