//! The page that a URL requests. The event stream renders once per
//! page, so two URLs that request the same page share one rendering.

use serde::Deserialize;

use transect_supply::vulnerabilities::Shown as Severities;

#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub enum Page {
    /// The cluster map, with one namespace selected or none.
    Cluster { select: Option<String> },
    /// The cluster map as it would look if the named machines were
    /// lost, with one namespace selected or none.
    Loss {
        lose: Vec<String>,
        select: Option<String>,
    },
    /// One namespace, opened.
    Namespace { name: String },
    /// One namespace's access tab.
    NamespaceAccess { name: String },
    /// Every object of one kind in one namespace, by the kind's
    /// resource name.
    Objects { namespace: String, resource: String },
    /// One workload, by its namespace, its kind's resource name, and
    /// its name.
    Workload {
        namespace: String,
        resource: String,
        name: String,
    },
    /// One elevation of a custom resource whose kind's knowledge states
    /// elevations, by the object's namespace, its kind's resource name,
    /// its name, and the elevation's slug.
    Elevation {
        namespace: String,
        resource: String,
        name: String,
        slug: String,
    },
    /// One pod, by its namespace and name.
    Pod { namespace: String, name: String },
    /// One cluster-scoped object, by its kind's name in the URL and its
    /// own name.
    ClusterObject { resource: String, name: String },
    /// `transect`'s own resources and their conditions.
    Resources,
    /// What changed in the cluster over the last day.
    Changed,
    /// Every custom kind the cluster serves.
    Kinds,
    /// Every shape that a sketch can draw a part in, in every state.
    Shapes,
    /// What may reach each port from outside the cluster.
    WaysIn,
    /// What each account may do, and where.
    Accounts,
    /// Every finding id in the current vulnerability reports, filtered
    /// to the severities that the query names.
    Vulnerabilities { severities: Severities },
    /// One finding id.
    Vulnerability { id: String },
    /// The access of one group or one user, by `groups` or `users` and
    /// its name.
    Subject { kinds: String, name: String },
    /// One custom kind's objects across namespaces, by `<plural>.<group>`.
    Kind { resource: String },
    /// One machine, by its `Node`'s name.
    Machine { name: String },
    /// One system outside the cluster, from the outside-systems band of the
    /// cluster map, by the name of its `Outside` or of its storage
    /// backend.
    Ground { name: String },
    /// One `PersistentVolume`, by name.
    Volume { name: String },
    /// The section view of one workload, which draws it from its
    /// `Ingress`es and `Service`s down to its machines and storage, by
    /// the same parts as its page.
    Section {
        namespace: String,
        resource: String,
        name: String,
    },
    /// One workload's access tab, by the same parts as its page.
    Access {
        namespace: String,
        resource: String,
        name: String,
    },
    /// One workload's vulnerabilities tab, by the same parts as its
    /// page, filtered to the severities that the query names.
    WorkloadVulnerabilities {
        namespace: String,
        resource: String,
        name: String,
        severities: Severities,
    },
    /// The logs of a pod, whose resource is `pods`, or of a workload,
    /// limited to the pod and the container that the query names.
    Logs {
        namespace: String,
        resource: String,
        name: String,
        pod: Option<String>,
        container: Option<String>,
    },
}

/// The query string every view reads.
#[derive(Debug, Default, Deserialize)]
pub struct Query {
    select: Option<String>,
    lose: Option<String>,
    pod: Option<String>,
    container: Option<String>,
    severity: Option<String>,
}

impl Query {
    /// The namespace to select. An empty `select` selects nothing,
    /// because an HTML form with a blank field sends one.
    pub fn select(&self) -> Option<String> {
        self.select
            .clone()
            .filter(|namespace| !namespace.is_empty())
    }

    /// The machines to lose, sorted and without duplicates, so that two
    /// URLs that list the same machines in another order share a view.
    pub fn lose(&self) -> Vec<String> {
        let names = self.lose.iter().flat_map(|names| names.split(','));
        let mut names: Vec<String> = names
            .map(str::trim)
            .filter(|name| !name.is_empty())
            .map(Into::into)
            .collect();
        names.sort();
        names.dedup();
        names
    }

    /// The pod that the query selects in a workload's logs, if any.
    pub fn pod(&self) -> Option<String> {
        self.pod.clone().filter(|pod| !pod.is_empty())
    }

    /// The container that the query selects in the logs, if any.
    pub fn container(&self) -> Option<String> {
        self.container
            .clone()
            .filter(|container| !container.is_empty())
    }

    /// The severities to show on a vulnerabilities page, all of them
    /// when the query names none.
    pub fn severities(&self) -> Severities {
        Severities::parse(self.severity.as_deref())
    }
}

impl Page {
    /// Whether the page belongs to the access section, where a name
    /// links to its object's access page. These are the pages under
    /// `/access`, a namespace's or a workload's access tab, and a
    /// `ServiceAccount`'s page, which draws its access.
    pub fn in_access(&self) -> bool {
        match self {
            Self::WaysIn | Self::Accounts | Self::Subject { .. } => true,
            Self::NamespaceAccess { .. } | Self::Access { .. } => true,
            Self::Workload { resource, .. } => resource == "serviceaccounts",
            _ => false,
        }
    }

    /// The cluster map that a query on `/` requests. When the query
    /// both loses machines and selects a namespace, the page keeps the
    /// loss on the map and opens the namespace's card in the sidebar, so
    /// a click on a namespace stays in the loss.
    pub fn cluster(query: &Query) -> Self {
        match query.lose() {
            lose if lose.is_empty() => Self::Cluster {
                select: query.select(),
            },
            lose => Self::Loss {
                lose,
                select: query.select(),
            },
        }
    }

    /// The page at `path`, the URL path without its leading slash, or
    /// `None` when no page serves the path.
    pub fn at(path: &str, query: &Query) -> Option<Self> {
        match path.split('/').collect::<Vec<_>>()[..] {
            [""] => Some(Self::cluster(query)),
            ["resources"] => Some(Self::Resources),
            ["changed"] => Some(Self::Changed),
            ["kinds"] => Some(Self::Kinds),
            ["shapes"] => Some(Self::Shapes),
            ["access"] => Some(Self::WaysIn),
            ["access", "accounts"] => Some(Self::Accounts),
            ["vulnerabilities"] => Some(Self::Vulnerabilities {
                severities: query.severities(),
            }),
            // A scanner's id can contain a slash, so the id is the rest of
            // the path.
            ["vulnerabilities", ref id @ ..] if !id.concat().is_empty() => {
                Some(Self::Vulnerability { id: id.join("/") })
            }
            // A user's or a group's name can contain slashes, as an
            // identity provider's names do, and the router decodes `%2F`
            // in the path to `/`, so the name is the rest of the path.
            [
                "access",
                "accounts",
                kinds @ ("groups" | "users"),
                ref name @ ..,
            ] if !name.concat().is_empty() => Some(Self::Subject {
                kinds: kinds.into(),
                name: name.join("/"),
            }),
            ["kinds", resource] if !resource.is_empty() => Some(Self::Kind {
                resource: resource.into(),
            }),
            ["machines", name] if !name.is_empty() => Some(Self::Machine { name: name.into() }),
            ["ground", name] if !name.is_empty() => Some(Self::Ground { name: name.into() }),
            ["volumes", name] if !name.is_empty() => Some(Self::Volume { name: name.into() }),
            ["cluster", resource, name] if !resource.is_empty() && !name.is_empty() => {
                Some(Self::ClusterObject {
                    resource: resource.into(),
                    name: name.into(),
                })
            }
            ["namespaces", name] if !name.is_empty() => Some(Self::Namespace { name: name.into() }),
            ["namespaces", name, "access"] if !name.is_empty() => {
                Some(Self::NamespaceAccess { name: name.into() })
            }
            ["namespaces", namespace, resource]
                if !namespace.is_empty() && !resource.is_empty() =>
            {
                Some(Self::Objects {
                    namespace: namespace.into(),
                    resource: resource.into(),
                })
            }
            // `pods` is not the resource name of any workload, so this
            // arm must come before the workload arm below.
            ["namespaces", namespace, "pods", name]
                if !namespace.is_empty() && !name.is_empty() =>
            {
                Some(Self::Pod {
                    namespace: namespace.into(),
                    name: name.into(),
                })
            }
            ["namespaces", namespace, resource, name]
                if [namespace, resource, name]
                    .iter()
                    .all(|part| !part.is_empty()) =>
            {
                Some(Self::Workload {
                    namespace: namespace.into(),
                    resource: resource.into(),
                    name: name.into(),
                })
            }
            ["namespaces", namespace, resource, name, "section"]
                if resource != "pods"
                    && [namespace, resource, name]
                        .iter()
                        .all(|part| !part.is_empty()) =>
            {
                Some(Self::Section {
                    namespace: namespace.into(),
                    resource: resource.into(),
                    name: name.into(),
                })
            }
            ["namespaces", namespace, resource, name, "elevations", slug]
                if resource != "pods"
                    && [namespace, resource, name, slug]
                        .iter()
                        .all(|part| !part.is_empty()) =>
            {
                Some(Self::Elevation {
                    namespace: namespace.into(),
                    resource: resource.into(),
                    name: name.into(),
                    slug: slug.into(),
                })
            }
            ["namespaces", namespace, resource, name, "access"]
                if [namespace, resource, name]
                    .iter()
                    .all(|part| !part.is_empty()) =>
            {
                Some(Self::Access {
                    namespace: namespace.into(),
                    resource: resource.into(),
                    name: name.into(),
                })
            }
            ["namespaces", namespace, resource, name, "vulnerabilities"]
                if resource != "pods"
                    && [namespace, resource, name]
                        .iter()
                        .all(|part| !part.is_empty()) =>
            {
                Some(Self::WorkloadVulnerabilities {
                    namespace: namespace.into(),
                    resource: resource.into(),
                    name: name.into(),
                    severities: query.severities(),
                })
            }
            ["namespaces", namespace, resource, name, "logs"]
                if [namespace, resource, name]
                    .iter()
                    .all(|part| !part.is_empty()) =>
            {
                Some(Self::Logs {
                    namespace: namespace.into(),
                    resource: resource.into(),
                    name: name.into(),
                    pod: query.pod(),
                    container: query.container(),
                })
            }
            _ => None,
        }
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn query(select: Option<&str>) -> Query {
        Query {
            select: select.map(Into::into),
            ..Query::default()
        }
    }

    #[rstest]
    #[case::the_cluster("", None, Some(Page::Cluster { select: None }))]
    #[case::a_selection("", Some("shop"), Some(Page::Cluster { select: Some("shop".into()) }))]
    #[case::a_blank_selection("", Some(""), Some(Page::Cluster { select: None }))]
    #[case::a_namespace("namespaces/shop", None, Some(Page::Namespace { name: "shop".into() }))]
    #[case::a_namespace_ignores_the_selection("namespaces/shop", Some("blog"), Some(Page::Namespace { name: "shop".into() }))]
    #[case::no_namespace_named("namespaces/", None, None)]
    #[case::one_kind_in_a_namespace("namespaces/shop/widgets.example.io", None, Some(Page::Objects { namespace: "shop".into(), resource: "widgets.example.io".into() }))]
    #[case::a_kind_with_no_name("namespaces/shop/", None, None)]
    #[case::a_workload("namespaces/shop/deployments/web", None, Some(Page::Workload { namespace: "shop".into(), resource: "deployments".into(), name: "web".into() }))]
    #[case::a_custom_workload("namespaces/shop/caches.cache.example.io/sessions", Some("blog"), Some(Page::Workload { namespace: "shop".into(), resource: "caches.cache.example.io".into(), name: "sessions".into() }))]
    #[case::a_pod("namespaces/shop/pods/web-5d8f-x2x7q", None, Some(Page::Pod { namespace: "shop".into(), name: "web-5d8f-x2x7q".into() }))]
    #[case::a_pod_with_no_name("namespaces/shop/pods/", None, None)]
    #[case::a_workload_with_no_name("namespaces/shop/deployments/", None, None)]
    #[case::below_a_workload("namespaces/shop/deployments/web/more", None, None)]
    #[case::a_section("namespaces/shop/deployments/web/section", None, Some(Page::Section { namespace: "shop".into(), resource: "deployments".into(), name: "web".into() }))]
    #[case::an_elevation("namespaces/shop/kiosks.example.test/front/elevations/the-wiring", None, Some(Page::Elevation { namespace: "shop".into(), resource: "kiosks.example.test".into(), name: "front".into(), slug: "the-wiring".into() }))]
    #[case::no_slug("namespaces/shop/kiosks.example.test/front/elevations", None, None)]
    #[case::an_empty_slug("namespaces/shop/kiosks.example.test/front/elevations/", None, None)]
    #[case::no_section_of_a_pod("namespaces/shop/pods/web-5d8f-x2x7q/section", None, None)]
    #[case::a_section_with_no_name("namespaces/shop/deployments//section", None, None)]
    #[case::a_pods_logs("namespaces/shop/pods/web-5d8f-x2x7q/logs", None, Some(Page::Logs { namespace: "shop".into(), resource: "pods".into(), name: "web-5d8f-x2x7q".into(), pod: None, container: None }))]
    #[case::a_workloads_logs("namespaces/shop/deployments/web/logs", None, Some(Page::Logs { namespace: "shop".into(), resource: "deployments".into(), name: "web".into(), pod: None, container: None }))]
    #[case::the_resources("resources", None, Some(Page::Resources))]
    #[case::the_changes("changed", None, Some(Page::Changed))]
    #[case::the_kinds("kinds", None, Some(Page::Kinds))]
    #[case::the_shapes("shapes", None, Some(Page::Shapes))]
    #[case::the_ways_in("access", None, Some(Page::WaysIn))]
    #[case::a_workloads_access("namespaces/shop/deployments/web/access", None, Some(Page::Access { namespace: "shop".into(), resource: "deployments".into(), name: "web".into() }))]
    #[case::the_accounts("access/accounts", None, Some(Page::Accounts))]
    #[case::a_workloads_vulnerabilities("namespaces/shop/deployments/web/vulnerabilities", None, Some(Page::WorkloadVulnerabilities { namespace: "shop".into(), resource: "deployments".into(), name: "web".into(), severities: Severities::default() }))]
    #[case::the_vulnerabilities("vulnerabilities", None, Some(Page::Vulnerabilities { severities: Severities::default() }))]
    #[case::one_finding("vulnerabilities/CVE-2026-0001", None, Some(Page::Vulnerability { id: "CVE-2026-0001".into() }))]
    #[case::no_finding_named("vulnerabilities/", None, None)]
    #[case::a_namespaces_access("namespaces/shop/access", None, Some(Page::NamespaceAccess { name: "shop".into() }))]
    #[case::a_group("access/accounts/groups/system:masters", None, Some(Page::Subject { kinds: "groups".into(), name: "system:masters".into() }))]
    #[case::a_user_whose_name_holds_slashes("access/accounts/users/https://issuer.example.test/alice", None, Some(Page::Subject { kinds: "users".into(), name: "https://issuer.example.test/alice".into() }))]
    #[case::a_subject_with_no_name("access/accounts/users/", None, None)]
    #[case::not_a_kind_of_subject("access/accounts/robots/r2", None, None)]
    #[case::one_kind("kinds/widgets.example.io", None, Some(Page::Kind { resource: "widgets.example.io".into() }))]
    #[case::a_machine("machines/alpha", None, Some(Page::Machine { name: "alpha".into() }))]
    #[case::an_outside_system("ground/a-nas", None, Some(Page::Ground { name: "a-nas".into() }))]
    #[case::no_machine_named("machines/", None, None)]
    #[case::a_cluster_scoped_object("cluster/clusterroles.rbac.authorization.k8s.io/view", None, Some(Page::ClusterObject { resource: "clusterroles.rbac.authorization.k8s.io".into(), name: "view".into() }))]
    #[case::an_unknown_path("nowhere", None, None)]
    fn reads_the_view_from_the_url(
        #[case] path: &str,
        #[case] select: Option<&str>,
        #[case] page: Option<Page>,
    ) {
        assert_eq!(Page::at(path, &query(select)), page);
    }

    #[rstest]
    #[case::one_machine(Some("alpha"), None, Page::Loss { lose: vec!["alpha".into()], select: None })]
    #[case::a_selection_in_it(Some("alpha"), Some("shop"), Page::Loss { lose: vec!["alpha".into()], select: Some("shop".into()) })]
    #[case::a_set_in_any_order(Some("beta, alpha,beta"), None, Page::Loss { lose: vec!["alpha".into(), "beta".into()], select: None })]
    #[case::a_blank_loss(Some(","), Some("shop"), Page::Cluster { select: Some("shop".into()) })]
    fn reads_the_loss_from_the_url(
        #[case] lose: Option<&str>,
        #[case] select: Option<&str>,
        #[case] page: Page,
    ) {
        let query = Query {
            lose: lose.map(Into::into),
            ..query(select)
        };
        assert_eq!(Page::at("", &query), Some(page));
    }

    #[rstest]
    #[case::a_pod_and_a_container(Some("web-2"), Some("proxy"), (Some("web-2".to_string()), Some("proxy".to_string())))]
    #[case::blank(Some(""), Some(""), (None, None))]
    fn reads_the_pod_and_container_of_a_logs_lens(
        #[case] pod: Option<&str>,
        #[case] container: Option<&str>,
        #[case] expected: (Option<String>, Option<String>),
    ) {
        let query = Query {
            pod: pod.map(Into::into),
            container: container.map(Into::into),
            ..Query::default()
        };
        let page = Page::at("namespaces/shop/deployments/web/logs", &query);
        let found = match page {
            Some(Page::Logs { pod, container, .. }) => (pod, container),
            other => panic!("the page is {other:?}, want Page::Logs"),
        };
        assert_eq!(found, expected);
    }
}
