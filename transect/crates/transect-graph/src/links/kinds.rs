//! Each kind's name in a URL, in kubectl's form: the plural alone for
//! the core group and for `apps` and `batch`, and `<plural>.<group>`
//! for every other group, so no two kinds share one, such as
//! `caches.cache.example.io`.
//! Kubernetes's own kinds have their plurals here; a custom kind's
//! comes from discovery.

use crate::edges::{Object, Plurals};

/// Kubernetes's own kinds a page may show, by group and kind, with
/// their plurals.  `objects::kinds` defines how a page reads each.
const BUILT_IN: [(&str, &str, &str); 30] = [
    ("", "ConfigMap", "configmaps"),
    ("", "LimitRange", "limitranges"),
    ("", "Namespace", "namespaces"),
    ("", "Node", "nodes"),
    ("", "PersistentVolume", "persistentvolumes"),
    ("", "PersistentVolumeClaim", "persistentvolumeclaims"),
    ("", "Pod", "pods"),
    ("", "ResourceQuota", "resourcequotas"),
    ("", "Secret", "secrets"),
    ("", "Service", "services"),
    ("", "ServiceAccount", "serviceaccounts"),
    ("apps", "DaemonSet", "daemonsets"),
    ("apps", "Deployment", "deployments"),
    ("apps", "ReplicaSet", "replicasets"),
    ("apps", "StatefulSet", "statefulsets"),
    ("batch", "CronJob", "cronjobs"),
    ("batch", "Job", "jobs"),
    ("coordination.k8s.io", "Lease", "leases"),
    ("networking.k8s.io", "Ingress", "ingresses"),
    ("networking.k8s.io", "IngressClass", "ingressclasses"),
    ("networking.k8s.io", "NetworkPolicy", "networkpolicies"),
    ("policy", "PodDisruptionBudget", "poddisruptionbudgets"),
    ("rbac.authorization.k8s.io", "ClusterRole", "clusterroles"),
    (
        "rbac.authorization.k8s.io",
        "ClusterRoleBinding",
        "clusterrolebindings",
    ),
    ("rbac.authorization.k8s.io", "Role", "roles"),
    ("rbac.authorization.k8s.io", "RoleBinding", "rolebindings"),
    ("resource.k8s.io", "ResourceClaim", "resourceclaims"),
    ("scheduling.k8s.io", "PriorityClass", "priorityclasses"),
    ("storage.k8s.io", "CSIDriver", "csidrivers"),
    ("storage.k8s.io", "StorageClass", "storageclasses"),
];

/// The built-in kinds that can be a top owner, by group and kind, with
/// their plurals.  A bare `Pod` is left out, because a link to it opens
/// the pod page, which is a different URL.
const WORKLOADS: [(&str, &str, &str); 5] = [
    ("apps", "Deployment", "deployments"),
    ("apps", "StatefulSet", "statefulsets"),
    ("apps", "DaemonSet", "daemonsets"),
    ("batch", "CronJob", "cronjobs"),
    ("batch", "Job", "jobs"),
];

/// Whether a URL names a kind of `group` by its plural alone.
fn plural_alone(group: &str) -> bool {
    matches!(group, "" | "apps" | "batch")
}

/// `plural` of `group` as a URL names it.
pub fn url_name(group: &str, plural: &str) -> String {
    match plural_alone(group) {
        true => plural.into(),
        false => format!("{plural}.{group}"),
    }
}

/// The plural of Kubernetes's own kind `group`/`kind`, when it is one
/// a page shows.
pub fn plural(group: &str, kind: &str) -> Option<&'static str> {
    let found = BUILT_IN.iter().find(|(g, k, _)| *g == group && *k == kind);
    found.map(|(_, _, plural)| *plural)
}

/// Every kind of Kubernetes's own a page may show, by group and kind,
/// for the test that `objects::kinds` reads each one.
#[cfg(any(test, feature = "fixtures"))]
pub fn built_in() -> impl Iterator<Item = (&'static str, &'static str)> {
    BUILT_IN.iter().map(|(group, kind, _)| (*group, *kind))
}

/// The name in a URL of `object`'s kind, or none for a kind with no
/// URL: Kubernetes's own kinds by the table, and a custom kind by the
/// plural discovery gave it.
pub fn url_of(object: &Object, plurals: &Plurals) -> Option<String> {
    match plural(&object.group, &object.kind) {
        Some(plural) => Some(url_name(&object.group, plural)),
        None => workload(object, plurals),
    }
}

/// The name in a URL of the kind of `object`, a top owner, or none for
/// a kind with no page.  The page for a URL finds its workload by naming
/// each of the namespace's top owners this way and taking the one that
/// matches, so no name has to be parsed back into a kind.  A custom
/// kind discovery has not resolved has none either, because its plural
/// comes from the API, and a guess at it makes a link to a page that
/// does not exist.
pub fn workload(object: &Object, plurals: &Plurals) -> Option<String> {
    if let Some((_, _, plural)) = WORKLOADS
        .iter()
        .find(|(group, kind, _)| object.group == *group && object.kind == *kind)
    {
        return Some((*plural).into());
    }
    if plural_alone(&object.group) {
        return None;
    }
    let plural = plurals.get(&(object.group.clone(), object.kind.clone()))?;
    Some(url_name(&object.group, plural))
}

#[cfg(test)]
mod tests {
    use std::collections::HashMap;

    use rstest::rstest;

    use super::*;

    fn plurals() -> Plurals {
        HashMap::from([(("cache.example.io".into(), "Cache".into()), "caches".into())])
    }

    #[rstest]
    #[case::a_core_kind("", "Service", Some("services"))]
    #[case::a_workload("apps", "Deployment", Some("deployments"))]
    #[case::another_group("networking.k8s.io", "Ingress", Some("ingresses.networking.k8s.io"))]
    #[case::a_custom_kind("cache.example.io", "Cache", Some("caches.cache.example.io"))]
    #[case::an_unknown_kind("unknown.example.io", "Thing", None)]
    fn names_each_kind_in_a_url(
        #[case] group: &str,
        #[case] kind: &str,
        #[case] expected: Option<&str>,
    ) {
        let object = Object::new(group, kind, Some("shop"), "a");
        assert_eq!(url_of(&object, &plurals()).as_deref(), expected);
    }

    #[rstest]
    #[case::a_deployment("apps", "Deployment", Some("deployments"))]
    #[case::a_stateful_set("apps", "StatefulSet", Some("statefulsets"))]
    #[case::a_daemon_set("apps", "DaemonSet", Some("daemonsets"))]
    #[case::a_cron_job("batch", "CronJob", Some("cronjobs"))]
    #[case::a_job("batch", "Job", Some("jobs"))]
    #[case::a_bare_pod("", "Pod", None)]
    #[case::a_discovered_custom_kind("cache.example.io", "Cache", Some("caches.cache.example.io"))]
    #[case::an_undiscovered_custom_kind("agents.example.io", "Session", None)]
    fn names_a_top_owners_kind_as_kubectl_does(
        #[case] group: &str,
        #[case] kind: &str,
        #[case] expected: Option<&str>,
    ) {
        let object = Object::new(group, kind, Some("shop"), "web");
        assert_eq!(workload(&object, &plurals()).as_deref(), expected);
    }
}
