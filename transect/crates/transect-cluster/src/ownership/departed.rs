//! The top owners of pods that are gone.  A pod's owner reference is
//! deleted with the pod, but a reader on the page of a pod that a
//! rollout replaced still wants its workload, which runs the pod that
//! replaced it.  So each model keeps the owner of every owned pod that
//! left since the model before it, and copies forward what that model
//! kept.

use std::collections::{HashMap, HashSet};
use std::sync::Arc;
use std::time::{Duration, Instant};

use k8s_openapi::api::core::v1::Pod;

use super::Ownership;
use transect_graph::edges::Object;

/// How long a gone pod's owner is kept.  A link to a pod that a rollout
/// replaced an hour ago still leads to its workload.  Past that, the
/// memory would grow with every rollout for links few readers still
/// hold.
const KEEP: Duration = Duration::from_secs(60 * 60);

/// A pod by its namespace and name.
type Key = (String, String);

#[derive(Debug, Default)]
pub struct Departed {
    /// Each gone pod's top owner, and when the pod was first missed.
    pods: HashMap<Key, (Object, Instant)>,
}

/// The pods of one model and what owned each.
pub struct Before<'a> {
    pub departed: &'a Departed,
    pub pods: &'a [Arc<Pod>],
    pub ownership: &'a Ownership,
}

impl Departed {
    /// The pods `before` held, or had kept, that `pods` does not hold,
    /// each with its top owner.  A pod with no owner has no workload to
    /// point to, so it is not kept, and one kept longer than `KEEP` is
    /// dropped.
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

    /// The top owner of the gone pod `namespace/name`, if it had one.
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

    /// The pods `web-1` of the `Deployment` `web`, and `debug`, alone.
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

    /// What is kept of `name` once the pods are `after`, `later` after
    /// the model that first missed `web-1`.
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
