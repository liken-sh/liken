//! The kind that a URL's name refers to, and how a page reads it.
//!
//! `links::kinds` writes each kind's name in a URL.  Kubernetes's own
//! kinds are listed here with their version, their scope, and how a
//! page reads each, in one table that the object lookup follows.  The
//! same facts for a custom kind come from its store.

use kube::core::ApiResource;

use transect_graph::edges::ClusterScoped;
use transect_graph::links::{self, kinds::url_name};

/// How a page reads an object of one of Kubernetes's own kinds.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum ReadFrom {
    /// The store that holds the kind, with the object's status.
    Status,
    /// The store that holds the kind's metadata alone.  A ConfigMap is
    /// read this way, so no page can show what it contains.
    Metadata,
    /// Nowhere here: the kind has a page of its own, which its URL
    /// reaches or redirects to.
    OwnPage,
    /// One GET when its page loads, because no store contains the kind.
    Get,
    /// Never: a Secret's page would show what transect may not read.
    Never,
}

/// Kubernetes's own kinds a page may show: group, version, kind,
/// whether each is namespaced, and how its page reads it.  Each kind's
/// plural, which its URL names, is in `links::kinds`.
const BUILT_IN: [(&str, &str, &str, bool, ReadFrom); 30] = [
    ("", "v1", "ConfigMap", true, ReadFrom::Metadata),
    ("", "v1", "LimitRange", true, ReadFrom::Get),
    ("", "v1", "Namespace", false, ReadFrom::OwnPage),
    ("", "v1", "Node", false, ReadFrom::OwnPage),
    ("", "v1", "PersistentVolume", false, ReadFrom::OwnPage),
    ("", "v1", "PersistentVolumeClaim", true, ReadFrom::OwnPage),
    ("", "v1", "Pod", true, ReadFrom::OwnPage),
    ("", "v1", "ResourceQuota", true, ReadFrom::Get),
    ("", "v1", "Secret", true, ReadFrom::Never),
    ("", "v1", "Service", true, ReadFrom::Status),
    ("", "v1", "ServiceAccount", true, ReadFrom::Metadata),
    ("apps", "v1", "DaemonSet", true, ReadFrom::Status),
    ("apps", "v1", "Deployment", true, ReadFrom::Status),
    ("apps", "v1", "ReplicaSet", true, ReadFrom::Metadata),
    ("apps", "v1", "StatefulSet", true, ReadFrom::Status),
    ("batch", "v1", "CronJob", true, ReadFrom::Status),
    ("batch", "v1", "Job", true, ReadFrom::Metadata),
    ("coordination.k8s.io", "v1", "Lease", true, ReadFrom::Get),
    ("networking.k8s.io", "v1", "Ingress", true, ReadFrom::Status),
    (
        "networking.k8s.io",
        "v1",
        "IngressClass",
        false,
        ReadFrom::Status,
    ),
    (
        "networking.k8s.io",
        "v1",
        "NetworkPolicy",
        true,
        ReadFrom::Status,
    ),
    (
        "policy",
        "v1",
        "PodDisruptionBudget",
        true,
        ReadFrom::Status,
    ),
    (
        "rbac.authorization.k8s.io",
        "v1",
        "ClusterRole",
        false,
        ReadFrom::Status,
    ),
    (
        "rbac.authorization.k8s.io",
        "v1",
        "ClusterRoleBinding",
        false,
        ReadFrom::Status,
    ),
    (
        "rbac.authorization.k8s.io",
        "v1",
        "Role",
        true,
        ReadFrom::Status,
    ),
    (
        "rbac.authorization.k8s.io",
        "v1",
        "RoleBinding",
        true,
        ReadFrom::Status,
    ),
    (
        "resource.k8s.io",
        "v1",
        "ResourceClaim",
        true,
        ReadFrom::Status,
    ),
    (
        "scheduling.k8s.io",
        "v1",
        "PriorityClass",
        false,
        ReadFrom::Metadata,
    ),
    ("storage.k8s.io", "v1", "CSIDriver", false, ReadFrom::Status),
    (
        "storage.k8s.io",
        "v1",
        "StorageClass",
        false,
        ReadFrom::Status,
    ),
];

/// One kind a URL can name.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Served {
    pub resource: ApiResource,
    pub namespaced: bool,
    /// How a page reads an object of a kind of Kubernetes's own, or
    /// none for a custom kind, which its store holds.
    pub read: Option<ReadFrom>,
}

impl Served {
    /// The kind's name in a URL.
    pub fn url(&self) -> String {
        url_name(&self.resource.group, &self.resource.plural)
    }
}

/// Kubernetes's own kind `group`/`kind`, when it is one a page shows.
pub fn built_in(group: &str, kind: &str) -> Option<Served> {
    let (group, version, kind, namespaced, read) = BUILT_IN
        .iter()
        .find(|(g, _, k, _, _)| *g == group && *k == kind)?;
    let plural = links::kinds::plural(group, kind)?;
    let api_version = match *group {
        "" => (*version).to_string(),
        _ => format!("{group}/{version}"),
    };
    Some(Served {
        resource: ApiResource {
            group: (*group).into(),
            version: (*version).into(),
            api_version,
            kind: (*kind).into(),
            plural: plural.into(),
        },
        namespaced: *namespaced,
        read: Some(*read),
    })
}

/// Every kind of Kubernetes's own a page may show.
pub fn every_built_in() -> impl Iterator<Item = Served> {
    let all = BUILT_IN
        .iter()
        .map(|(group, _, kind, ..)| built_in(group, kind));
    all.flatten()
}

/// Every kind a URL can name: Kubernetes's own, then each custom kind
/// in `custom`.
pub struct Catalog<'a> {
    pub custom: &'a [ApiResource],
    pub scoped: &'a ClusterScoped,
}

impl Catalog<'_> {
    /// The kind `url` names, as `url_name` writes it.
    pub fn resolve(&self, url: &str) -> Option<Served> {
        if let Some(found) = every_built_in().find(|served| served.url() == url) {
            return Some(found);
        }
        let mut custom = self.custom.iter();
        let found = custom.find(|resource| url_name(&resource.group, &resource.plural) == url)?;
        let key = (found.group.clone(), found.kind.clone());
        Some(Served {
            resource: found.clone(),
            namespaced: !self.scoped.contains(&key),
            read: None,
        })
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    #[rstest]
    fn every_kind_a_page_shows_has_a_plural_and_no_other_does() {
        let shown: Vec<(String, String)> = every_built_in()
            .map(|served| (served.resource.group, served.resource.kind))
            .collect();
        let named: Vec<(String, String)> = links::kinds::built_in()
            .map(|(group, kind)| (group.into(), kind.into()))
            .collect();
        assert_eq!(shown, named);
    }

    fn cache() -> ApiResource {
        ApiResource {
            group: "cache.example.io".into(),
            version: "v1".into(),
            api_version: "cache.example.io/v1".into(),
            kind: "Cache".into(),
            plural: "caches".into(),
        }
    }

    #[rstest]
    #[case::a_core_kind("serviceaccounts", Some(("ServiceAccount", true)))]
    #[case::cluster_scoped("clusterroles.rbac.authorization.k8s.io", Some(("ClusterRole", false)))]
    #[case::a_custom_kind("caches.cache.example.io", Some(("Cache", true)))]
    #[case::a_plural_without_its_group("caches", None)]
    fn reads_the_kind_a_url_names(#[case] url: &str, #[case] expected: Option<(&str, bool)>) {
        let custom = [cache()];
        let catalog = Catalog {
            custom: &custom,
            scoped: &ClusterScoped::default(),
        };
        let found = catalog.resolve(url);
        let found = found.as_ref();
        let found = found.map(|served| (served.resource.kind.as_str(), served.namespaced));
        assert_eq!(found, expected);
    }
}
