//! The name of a kind in a page URL, in the form kubectl accepts: the
//! plural alone for the core group, `apps`, and `batch`, and
//! `<plural>.<group>` for every other group, such as
//! `caches.cache.example.io`, so that two kinds with the same plural in
//! different groups get different URLs. The plurals of the built-in
//! kinds are in a table here, and a custom kind's plural comes from API
//! discovery.

use crate::edges::{Object, Plurals};

/// The built-in kinds a page may show, as (group, kind, plural).
/// `transect_map::objects::kinds` reads each one.
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

/// The built-in kinds that can be a top owner, as (group, kind, plural).
/// A bare `Pod` is left out because its link opens the pod page, which
/// has a different URL.
const WORKLOADS: [(&str, &str, &str); 5] = [
    ("apps", "Deployment", "deployments"),
    ("apps", "StatefulSet", "statefulsets"),
    ("apps", "DaemonSet", "daemonsets"),
    ("batch", "CronJob", "cronjobs"),
    ("batch", "Job", "jobs"),
];

fn plural_alone(group: &str) -> bool {
    matches!(group, "" | "apps" | "batch")
}

/// The URL name of the kind with this `plural` in `group`.
pub fn url_name(group: &str, plural: &str) -> String {
    match plural_alone(group) {
        true => plural.into(),
        false => format!("{plural}.{group}"),
    }
}

/// The plural of the built-in kind `group`/`kind`, or none when a page
/// does not show that kind.
pub fn plural(group: &str, kind: &str) -> Option<&'static str> {
    let found = BUILT_IN.iter().find(|(g, k, _)| *g == group && *k == kind);
    found.map(|(_, _, plural)| *plural)
}

/// Every built-in kind a page may show, as (group, kind), so a test can
/// check that `objects::kinds` reads each one.
#[cfg(any(test, feature = "fixtures"))]
pub fn built_in() -> impl Iterator<Item = (&'static str, &'static str)> {
    BUILT_IN.iter().map(|(group, kind, _)| (*group, *kind))
}

/// The URL name of `object`'s kind, or none when the kind has no URL.
pub fn url_of(object: &Object, plurals: &Plurals) -> Option<String> {
    match plural(&object.group, &object.kind) {
        Some(plural) => Some(url_name(&object.group, plural)),
        None => workload(object, plurals),
    }
}

/// The URL name of the kind of `object`, a top owner, or none when the
/// kind has no workload page. The web pages find the workload for a URL
/// by calling this for each top owner and comparing, so no URL is parsed
/// back into a kind. A custom kind that discovery has not returned has
/// no URL name, because a guessed plural would link to a page that does
/// not exist.
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
