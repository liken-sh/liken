//! The page for a URL whose object is not in the cluster: a namespace,
//! a workload, a pod, or a machine deleted since its link was copied, or
//! one that never existed.  The page names what the URL named and links
//! to the nearest places above it that still exist.  transect keeps no
//! history, so the page says an object "no longer exists" only for a pod
//! that transect saw leave since it started.  For anything else the page
//! says the object was not found, which is true of a misspelled link and
//! of a deleted object.  The page keeps the view's event stream, so an
//! object that comes back under the same name, as a StatefulSet's pod
//! does, replaces this page with its own.

use askama::Template;

use transect_frame::frame::crumbs::Crumb;
use transect_graph::edges::{Object, Plurals};
use transect_graph::links::page;

/// What the URL named.
#[derive(Debug, PartialEq)]
pub enum Named {
    Namespace(String),
    /// A workload by its namespace, its kind's resource name in the
    /// URL, and its name.
    Workload {
        namespace: String,
        resource: String,
        name: String,
    },
    Pod {
        namespace: String,
        name: String,
    },
    /// One kind's list in a namespace, by the kind's resource name.
    Kind {
        namespace: String,
        resource: String,
    },
    /// A machine, by its Node's name.
    Machine(String),
    /// A ground box, by the name in its URL.
    Ground(String),
    /// An object outside any namespace, by its kind's name in the URL.
    ClusterObject {
        resource: String,
        name: String,
    },
    /// Every object of a custom kind, by `<plural>.<group>`.
    AllOfKind(String),
    /// A group or a user no binding names, by `groups` or `users` and
    /// its name.
    Subject {
        kinds: String,
        name: String,
    },
    /// A finding id that no current report includes.
    Vulnerability(String),
}

/// The places above the gone object, as they are in the cluster now.
pub struct Above<'a> {
    /// Whether the namespace the URL names is still in the cluster.
    pub namespace: bool,
    /// The workload that ran a gone pod, when one did and still exists.
    pub workload: Option<&'a Object>,
    /// Whether transect saw the object leave.
    pub seen: bool,
    pub plurals: &'a Plurals,
}

#[derive(Template)]
#[template(path = "map/gone.html", blocks = ["main"])]
pub struct GonePage {
    /// What it was, as "Pod web-5d8f-x2x7q in shop".
    pub what: String,
    /// The name the crumb ends with.
    pub name: String,
    /// The places above it that still exist, nearest first, ending with
    /// the cluster.
    pub up: Vec<Up>,
    /// The page's own path, which its event stream's URL includes.
    pub path: String,
    /// Whether transect saw the object leave.
    pub seen: bool,
}

#[derive(Debug, PartialEq)]
pub struct Up {
    pub text: String,
    pub href: String,
    /// Whether it names an object, which the crumb draws in monospace.
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
                    "groups" => format!("A Group named {name} that a binding names"),
                    _ => format!("A User named {name} that a binding names"),
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

    /// The crumb: each place above that still exists, from the cluster
    /// down, then the gone object's name.
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
