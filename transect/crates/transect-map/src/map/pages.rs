//! Decides which objects have a page, so that the map links only to a
//! page that exists. `links` builds the URL of each page.

use std::collections::BTreeMap;

use transect_cluster::ownership::Ownership;
use transect_graph::edges::{Object, Plurals};
use transect_graph::links;
use transect_resources::knowledge::Relation;
use transect_supply::gitops::GitOps;

/// The inputs that decide which objects have a page. Every Node and
/// every Pod with a namespace has one. So does every object in a store,
/// every workload, every custom resource of a kind that transect has
/// knowledge of, and every GitOps object.
#[derive(Clone, Copy)]
pub struct Pages<'a> {
    /// The objects in the stores that have a page.
    pub with_pages: &'a std::collections::HashSet<Object>,
    pub ownership: &'a Ownership,
    /// The custom resources of kinds that transect has knowledge of.
    pub relations: &'a BTreeMap<Object, Vec<Relation>>,
    pub gitops: &'a GitOps,
    pub plurals: &'a Plurals,
}

impl Pages<'_> {
    /// The URL of the page of `object`, or `None` when it has no page.
    /// Every Namespace gets a URL, even one that `has` returns false for.
    pub fn href(&self, object: &Object) -> Option<String> {
        match (object.group.as_str(), object.kind.as_str()) {
            ("", "Node") => Some(links::machine(&object.name)),
            ("", "Pod") => Some(links::pod(object.namespace.as_deref()?, &object.name)),
            ("", "Namespace") => Some(links::namespace(&object.name)),
            _ => self
                .has(object)
                .then(|| links::page(object, self.plurals))
                .flatten(),
        }
    }

    /// Whether `object` has a page of its own.
    pub fn has(&self, object: &Object) -> bool {
        match (object.group.as_str(), object.kind.as_str()) {
            ("", "Node") => true,
            ("", "Pod") => object.namespace.is_some(),
            _ => {
                let namespace = object.namespace.as_deref().unwrap_or_default();
                self.with_pages.contains(object)
                    || self.ownership.workloads(namespace).any(|w| w == object)
                    || self.relations.contains_key(object)
                    || self.gitops.found(object).is_some()
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use std::collections::HashMap;

    use super::*;
    use transect_cluster::ownership::Entry;

    fn plurals() -> Plurals {
        HashMap::from([(("cache.example.io".into(), "Cache".into()), "caches".into())])
    }

    /// The ownership of one `Deployment`, `web` in `shop`.
    fn ownership() -> Ownership {
        let meta = serde_yaml_ng::from_str("{namespace: shop, name: web, uid: web}").unwrap();
        Ownership::build([Entry {
            group: "apps",
            kind: "Deployment",
            meta: &meta,
        }])
    }

    #[rstest]
    #[case::a_workload(
        Object::new("apps", "Deployment", Some("shop"), "web"),
        Some("/namespaces/shop/deployments/web")
    )]
    #[case::a_workload_not_in_the_cluster(
        Object::new("apps", "Deployment", Some("shop"), "gone"),
        None
    )]
    #[case::a_pod(
        Object::new("", "Pod", Some("shop"), "web-1"),
        Some("/namespaces/shop/pods/web-1")
    )]
    #[case::a_node(Object::new("", "Node", None, "alpha"), Some("/machines/alpha"))]
    #[case::a_service_a_store_holds(
        Object::new("", "Service", Some("shop"), "web"),
        Some("/namespaces/shop/services/web")
    )]
    #[case::a_service_no_store_holds(Object::new("", "Service", Some("shop"), "gone"), None)]
    #[case::a_cluster_scoped_object(
        Object::new("rbac.authorization.k8s.io", "ClusterRole", None, "view"),
        Some("/cluster/clusterroles.rbac.authorization.k8s.io/view")
    )]
    #[case::a_name_that_could_end_the_path(
        Object::new("rbac.authorization.k8s.io", "ClusterRole", None, "a?b #c"),
        Some("/cluster/clusterroles.rbac.authorization.k8s.io/a%3Fb%20%23c")
    )]
    #[case::a_name_with_a_colon(
        Object::new("rbac.authorization.k8s.io", "ClusterRole", None, "system:view"),
        Some("/cluster/clusterroles.rbac.authorization.k8s.io/system:view")
    )]
    #[case::a_volume(
        Object::new("", "PersistentVolume", None, "pv-1"),
        Some("/volumes/pv-1")
    )]
    #[case::a_custom_resource_of_no_known_kind(
        Object::new("cache.example.io", "Cache", Some("shop"), "sessions"),
        None
    )]
    fn links_only_an_object_with_a_page(#[case] object: Object, #[case] expected: Option<&str>) {
        let (ownership, plurals) = (ownership(), plurals());
        let relations = BTreeMap::new();
        let gitops = GitOps::off(transect_supply::gitops::Off::NoKnowledge);
        let with_pages = std::collections::HashSet::from([
            Object::new("", "Service", Some("shop"), "web"),
            Object::new("rbac.authorization.k8s.io", "ClusterRole", None, "view"),
            Object::new("", "PersistentVolume", None, "pv-1"),
            Object::new("rbac.authorization.k8s.io", "ClusterRole", None, "a?b #c"),
            Object::new(
                "rbac.authorization.k8s.io",
                "ClusterRole",
                None,
                "system:view",
            ),
        ]);
        let pages = Pages {
            with_pages: &with_pages,
            ownership: &ownership,
            relations: &relations,
            gitops: &gitops,
            plurals: &plurals,
        };
        assert_eq!(pages.href(&object).as_deref(), expected);
    }
}
