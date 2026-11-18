//! Remembers the top owner of each deleted pod. A pod's owner
//! references are deleted with the pod, but a person who opens the page
//! of a pod that a rollout replaced needs a link to its workload, which
//! runs the replacement. So each new `Model` records the owner of every
//! pod that was deleted since the previous `Model`, and copies the
//! owners that the previous `Model` recorded.

use std::collections::{HashMap, HashSet};
use std::sync::Arc;
use std::time::{Duration, Instant};

use k8s_openapi::api::core::v1::Pod;

use super::Ownership;
use transect_graph::edges::Object;

/// How long a deleted pod's owner is remembered. Without a limit,
/// memory grows with every rollout, to serve links that few people
/// still open.
const KEEP: Duration = Duration::from_secs(60 * 60);

/// A pod by its namespace and name.
type Key = (String, String);

#[derive(Debug, Default)]
pub struct Departed {
    /// Each deleted pod's top owner, and when a `Model` first found the
    /// pod missing.
    pods: HashMap<Key, (Object, Instant)>,
}

/// The previous `Model`'s pods, ownership, and deleted pods.
pub struct Before<'a> {
    pub departed: &'a Departed,
    pub pods: &'a [Arc<Pod>],
    pub ownership: &'a Ownership,
}

impl Departed {
    /// Records each pod of `before` that is not in `pods`, with its top
    /// owner, and keeps each earlier record that is younger than `KEEP`
    /// and whose pod has not come back. A pod with no owner has no
    /// workload to link to, so it is not recorded.
    pub fn after(before: Before, pods: &[Arc<Pod>], now: Instant) -> Self {
        let here: HashSet<(&str, &str)> = pods.iter().filter_map(|pod| key(pod)).collect();
        let present = |(namespace, name): &Key| here.contains(&(namespace.as_str(), name.as_str()));
        let kept = before.departed.pods.iter().filter(|(key, (_, since))| {
            !present(key) && now.saturating_duration_since(*since) < KEEP
        });
        let mut departed: HashMap<Key, (Object, Instant)> = kept
            .map(|(key, entry)| (key.clone(), entry.clone()))
            .collect();
        for (namespace, name) in before.pods.iter().filter_map(|pod| key(pod)) {
            if here.contains(&(namespace, name)) {
                continue;
            }
            let pod = Object::new("", "Pod", Some(namespace), name);
            let owner = before.ownership.top_owner(&pod);
            if owner != pod {
                departed.insert((namespace.into(), name.into()), (owner, now));
            }
        }
        Self { pods: departed }
    }

    pub fn owner(&self, namespace: &str, name: &str) -> Option<&Object> {
        let key = (namespace.to_owned(), name.to_owned());
        self.pods.get(&key).map(|(owner, _)| owner)
    }
}

fn key(pod: &Pod) -> Option<(&str, &str)> {
    let meta = &pod.metadata;
    Some((meta.namespace.as_deref()?, meta.name.as_deref()?))
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::super::fixtures::build;
    use super::*;

    fn pod(name: &str) -> Arc<Pod> {
        let yaml = format!("metadata: {{namespace: shop, name: {name}}}");
        Arc::new(serde_yaml::from_str(&yaml).unwrap())
    }

    /// The pod `web-1`, which the `Deployment` `web` owns through a
    /// `ReplicaSet`, and the pod `debug`, which has no owner.
    fn before() -> (Vec<Arc<Pod>>, Ownership) {
        let ownership = build(&[
            (
                "",
                "Pod",
                Some("shop"),
                "web-1",
                vec![("apps/v1", "ReplicaSet", "web-5d8f")],
            ),
            (
                "apps",
                "ReplicaSet",
                Some("shop"),
                "web-5d8f",
                vec![("apps/v1", "Deployment", "web")],
            ),
            ("", "Pod", Some("shop"), "debug", vec![]),
        ]);
        (vec![pod("web-1"), pod("debug")], ownership)
    }

    /// The owner recorded for `name` when the pods are `after`, in a
    /// `Model` built `later` than the one that first found `web-1`
    /// missing.
    fn kept(after: &[&str], later: Duration, name: &str) -> Option<Object> {
        let (pods, ownership) = before();
        let start = Instant::now();
        let first = Departed::after(
            Before {
                departed: &Departed::default(),
                pods: &pods,
                ownership: &ownership,
            },
            &[],
            start,
        );
        let after: Vec<Arc<Pod>> = after.iter().map(|name| pod(name)).collect();
        let ownership = Ownership::default();
        let second = Departed::after(
            Before {
                departed: &first,
                pods: &[],
                ownership: &ownership,
            },
            &after,
            start + later,
        );
        second.owner("shop", name).cloned()
    }

    fn web() -> Option<Object> {
        Some(Object::new("apps", "Deployment", Some("shop"), "web"))
    }

    #[rstest]
    #[case::a_gone_pod_of_a_workload(&[], Duration::ZERO, "web-1", web())]
    #[case::kept_for_an_hour(&[], Duration::from_secs(59 * 60), "web-1", web())]
    #[case::dropped_after_an_hour(&[], KEEP, "web-1", None)]
    #[case::back_again(&["web-1"], Duration::ZERO, "web-1", None)]
    #[case::a_gone_pod_with_no_owner(&[], Duration::ZERO, "debug", None)]
    fn keeps_the_owner_of_a_gone_pod(
        #[case] after: &[&str],
        #[case] later: Duration,
        #[case] name: &str,
        #[case] owner: Option<Object>,
    ) {
        assert_eq!(kept(after, later, name), owner);
    }
}
