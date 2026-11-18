//! The page for a URL whose object is not in the cluster, because it
//! was deleted after someone copied the link or because it never
//! existed.  The page says what the URL named and links to the nearest
//! places above it that still exist.  transect keeps no history of
//! deleted objects.  It records the owner of each pod with an owner
//! that it watched being deleted, so only such a pod "no longer exists".  Every
//! other object "was not found", which is true of a misspelled link and
//! of a deleted object.  The page subscribes to the event stream of the
//! object's own path, so when an object comes back under the same name,
//! as a StatefulSet's pod does, the object's page replaces this one.

use askama::Template;

use transect_frame::frame::crumbs::Crumb;
use transect_graph::edges::{Object, Plurals};
use transect_graph::links::page;

/// What the URL named.
#[derive(Debug, PartialEq)]
pub enum Named {
    Namespace(String),
    /// A workload, by its namespace, the resource name of its kind as
    /// it appears in the URL, and its name.
    Workload {
        namespace: String,
        resource: String,
        name: String,
    },
    Pod {
        namespace: String,
        name: String,
    },
    /// The list of a kind's objects in a namespace, by the kind's
    /// resource name.
    Kind {
        namespace: String,
        resource: String,
    },
    /// A machine, by the name of its `Node`.
    Machine(String),
    /// An outside system's box, by the name in the URL.
    Ground(String),
    /// An object that is not in a namespace, by the name of its kind in
    /// the URL and its name.
    ClusterObject {
        resource: String,
        name: String,
    },
    /// The page of a custom kind, by `<plural>.<group>`.
    AllOfKind(String),
    /// A group or user that no binding names, by `groups` or `users`
    /// and its name.
    Subject {
        kinds: String,
        name: String,
    },
    /// A vulnerability id that no current report lists.
    Vulnerability(String),
}

/// The places above the missing object, as they are in the cluster.
pub struct Above<'a> {
    /// Whether the namespace in the URL is in the cluster.
    pub namespace: bool,
    /// The recorded top owner of a missing pod, when that owner still
    /// exists.
    pub workload: Option<&'a Object>,
    /// Whether transect saw the object leave the cluster.
    pub seen: bool,
    pub plurals: &'a Plurals,
}

#[derive(Template)]
#[template(path = "map/gone.html", blocks = ["main"])]
pub struct GonePage {
    /// What the object was, such as "Pod shop/web-5d8f-x2x7q".
    pub what: String,
    /// The name that ends the crumb.
    pub name: String,
    /// The places above the object that are in the cluster, nearest
    /// first, ending with the cluster.
    pub up: Vec<Up>,
    /// The page's own path, which the events URL is built from.
    pub path: String,
    /// Whether transect saw the object leave the cluster.
    pub seen: bool,
}

#[derive(Debug, PartialEq)]
pub struct Up {
    pub text: String,
    pub href: String,
    /// Whether the text is an object's name, which the crumb sets in
    /// monospace.
    pub name: bool,
}

impl GonePage {
    pub fn build(named: &Named, above: Above) -> Self {
        let cluster = Up {
            text: "cluster".into(),
            href: "/".into(),
            name: false,
        };
        let namespace_link = |namespace: &str| {
            above.namespace.then(|| Up {
                text: namespace.into(),
                href: format!("/namespaces/{namespace}"),
                name: true,
            })
        };
        let (what, name, path, mut up) = match named {
            Named::Namespace(namespace) => (
                format!("Namespace {namespace}"),
                namespace.clone(),
                format!("/namespaces/{namespace}"),
                Vec::new(),
            ),
            Named::Workload {
                namespace,
                resource,
                name,
            } => (
                format!("{resource}/{name} in {namespace}"),
                name.clone(),
                format!(
                    "/namespaces/{namespace}/{resource}/{}",
                    transect_graph::links::segment(name)
                ),
                namespace_link(namespace).into_iter().collect(),
            ),
            Named::Pod { namespace, name } => {
                let workload = above.workload.and_then(|workload| {
                    Some(Up {
                        text: transect_graph::edges::kind_and_name(&workload.kind, &workload.name),
                        href: page(workload, above.plurals)?,
                        name: true,
                    })
                });
                (
                    format!("Pod {namespace}/{name}"),
                    name.clone(),
                    format!("/namespaces/{namespace}/pods/{name}"),
                    workload
                        .into_iter()
                        .chain(namespace_link(namespace))
                        .collect(),
                )
            }
            Named::Kind {
                namespace,
                resource,
            } => (
                format!("A kind named {resource} in {namespace}"),
                resource.clone(),
                transect_graph::links::objects(namespace, resource),
                namespace_link(namespace).into_iter().collect(),
            ),
            Named::ClusterObject { resource, name } => (
                format!("{resource}/{name}"),
                name.clone(),
                format!(
                    "/cluster/{resource}/{}",
                    transect_graph::links::segment(name)
                ),
                Vec::new(),
            ),
            Named::AllOfKind(resource) => (
                format!("A kind named {resource}"),
                resource.clone(),
                format!("/kinds/{resource}"),
                vec![Up {
                    text: "kinds".into(),
                    href: "/kinds".into(),
                    name: false,
                }],
            ),
            Named::Subject { kinds, name } => (
                match kinds.as_str() {
                    "groups" => format!("A Group named {name} that a binding refers to"),
                    _ => format!("A User named {name} that a binding refers to"),
                },
                name.clone(),
                format!(
                    "/access/accounts/{kinds}/{}",
                    transect_graph::links::segment(name)
                ),
                vec![Up {
                    text: "accounts".into(),
                    href: "/access/accounts".into(),
                    name: false,
                }],
            ),
            Named::Vulnerability(id) => (
                format!("A finding {id} that a current report lists"),
                id.clone(),
                transect_supply::vulnerabilities::link(id),
                vec![Up {
                    text: "vulnerabilities".into(),
                    href: "/vulnerabilities".into(),
                    name: false,
                }],
            ),
            Named::Machine(name) => (
                format!("Machine {name}"),
                name.clone(),
                format!("/machines/{name}"),
                Vec::new(),
            ),
            Named::Ground(name) => (
                format!("Nothing outside the cluster named {name}"),
                name.clone(),
                format!("/ground/{name}"),
                Vec::new(),
            ),
        };
        up.push(cluster);
        Self {
            what,
            name,
            up,
            path,
            seen: above.seen,
        }
    }

    pub fn sentence(&self) -> String {
        match self.seen {
            true => format!("{} no longer exists.", self.what),
            false => format!("{} was not found.", self.what),
        }
    }

    /// The crumb: each place above the object, from the cluster down,
    /// then the missing object's name.
    pub fn crumbs(&self) -> Vec<Crumb> {
        let above = self.up.iter().rev().map(|up| match up.name {
            true => Crumb::link_name(&up.text, up.href.clone()),
            false => Crumb::link(&up.text, up.href.clone()),
        });
        above.chain([Crumb::current_name(&self.name)]).collect()
    }

    pub fn events_url(&self) -> String {
        format!("/events{}", self.path)
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn namespace(name: &str) -> Named {
        Named::Namespace(name.into())
    }

    fn deployment(name: &str) -> Named {
        Named::Workload {
            namespace: "shop".into(),
            resource: "deployments".into(),
            name: name.into(),
        }
    }

    fn pod(name: &str) -> Named {
        Named::Pod {
            namespace: "shop".into(),
            name: name.into(),
        }
    }

    fn up(text: &str, href: &str) -> (String, String) {
        (text.into(), href.into())
    }

    fn web() -> Object {
        Object::new("apps", "Deployment", Some("shop"), "web")
    }

    #[rstest]
    #[case::a_namespace(namespace("shop"), false, None, "Namespace shop was not found.", vec![up("cluster", "/")])]
    #[case::a_workload(
        deployment("web"),
        true,
        None,
        "deployments/web in shop was not found.",
        vec![up("shop", "/namespaces/shop"), up("cluster", "/")]
    )]
    #[case::a_workload_in_a_gone_namespace(
        deployment("web"),
        false,
        None,
        "deployments/web in shop was not found.",
        vec![up("cluster", "/")]
    )]
    #[case::a_pod_of_a_workload(
        pod("web-5d8f-x2x7q"),
        true,
        Some(web()),
        "Pod shop/web-5d8f-x2x7q no longer exists.",
        vec![up("Deployment web", "/namespaces/shop/deployments/web"), up("shop", "/namespaces/shop"), up("cluster", "/")]
    )]
    #[case::a_machine(Named::Machine("alpha".into()), false, None, "Machine alpha was not found.", vec![up("cluster", "/")])]
    #[case::a_pod_run_alone(
        pod("debug"),
        true,
        None,
        "Pod shop/debug was not found.",
        vec![up("shop", "/namespaces/shop"), up("cluster", "/")]
    )]
    fn says_what_is_gone_and_links_up(
        #[case] named: Named,
        #[case] namespace: bool,
        #[case] workload: Option<Object>,
        #[case] sentence: &str,
        #[case] links: Vec<(String, String)>,
    ) {
        let above = Above {
            namespace,
            seen: workload.is_some(),
            workload: workload.as_ref(),
            plurals: &Plurals::default(),
        };
        let page = GonePage::build(&named, above);
        let up: Vec<(String, String)> = page
            .up
            .iter()
            .map(|u| (u.text.clone(), u.href.clone()))
            .collect();
        assert_eq!((page.sentence(), up), (sentence.to_owned(), links));
    }
}
