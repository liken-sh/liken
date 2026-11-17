//! Which workload runs the controller that serves an `Ingress`.
//!
//! An `Ingress` names its class, and the class's `IngressClass` names
//! its controller by an identifier.  An `IngressController` that
//! declares that identifier names the image the controller runs, so
//! the workload is the one whose pods run that image.  With no such
//! resource, transect guesses the workload from the workloads that a
//! `LoadBalancer` `Service` sends web traffic to, because an ingress
//! controller receives its traffic that way.  The guess is the
//! workload whose name contains the class's name, or the only such
//! workload.  The page marks a guessed workload as a guess.

use std::collections::{BTreeSet, HashMap};
use std::sync::Arc;

use k8s_openapi::api::core::v1::Pod;
use k8s_openapi::api::networking::v1::IngressClass;
use kube::ResourceExt;

use super::controller::IngressController;
use transect_cluster::ownership::{Ownership, runs_image};
use transect_graph::edges::Object;
use transect_graph::pods::finished;

/// The data that finds the workload that runs the controller of each
/// `Ingress`'s class.
#[derive(Debug, Default)]
pub struct Serving {
    /// Each `IngressClass`'s name, with the controller its spec names.
    classes: HashMap<String, String>,
    /// Each declared controller's identifier, with the workloads that
    /// run its image.
    declared: HashMap<String, BTreeSet<Object>>,
    /// The workloads a `LoadBalancer` `Service` sends web traffic to.
    way_in: Vec<Object>,
}

/// The workload that runs an `Ingress`'s controller, and how it was
/// found.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Served {
    /// It runs the image an `IngressController` declares for the
    /// class's controller.
    Declared(Object),
    /// It receives the cluster's web traffic, and its name contains
    /// the class's name or it is the only workload that receives that
    /// traffic.
    Guessed(Object),
}

impl Serving {
    pub fn build(
        classes: &[Arc<IngressClass>],
        controllers: &[IngressController],
        pods: &[Arc<Pod>],
        ownership: &Ownership,
        way_in: Vec<Object>,
    ) -> Self {
        let classes = classes
            .iter()
            .filter_map(|class| {
                let controller = class.spec.as_ref()?.controller.clone()?;
                Some((class.name_any(), controller))
            })
            .collect();
        let running: Vec<&Pod> = pods
            .iter()
            .map(AsRef::as_ref)
            .filter(|p| !finished(p))
            .collect();
        let declared = controllers
            .iter()
            .map(|controller| {
                let workloads = running
                    .iter()
                    .filter(|pod| runs(pod, &controller.image))
                    .map(|pod| {
                        let namespace = pod.namespace();
                        let object = Object::new("", "Pod", namespace.as_deref(), &pod.name_any());
                        ownership.top_owner(&object)
                    })
                    .collect();
                (controller.controller.clone(), workloads)
            })
            .collect();
        Self {
            classes,
            declared,
            way_in,
        }
    }

    /// The workload that runs `class`'s controller, or none when no
    /// one workload does.
    pub fn of(&self, class: Option<&str>) -> Option<Served> {
        let controller = class.and_then(|class| self.classes.get(class));
        if let Some(workloads) = controller.and_then(|c| self.declared.get(c)) {
            let mut workloads = workloads.iter();
            return match (workloads.next(), workloads.next()) {
                (Some(one), None) => Some(Served::Declared(one.clone())),
                _ => None,
            };
        }
        guess(class, &self.way_in).map(|found| Served::Guessed(found.clone()))
    }
}

/// Whether a container of `pod` runs the image repository `image`.
pub fn runs(pod: &Pod, image: &str) -> bool {
    let spec = pod.spec.iter();
    let containers = spec.flat_map(|spec| &spec.containers);
    containers
        .filter_map(|container| container.image.as_deref())
        .any(|running| runs_image(running, image))
}

/// The workload of `way_in` that runs `class`'s controller: the one
/// whose name contains the class's name, or the only one there is.
fn guess<'a>(class: Option<&str>, way_in: &'a [Object]) -> Option<&'a Object> {
    let named: Vec<&Object> = match class {
        Some(class) => {
            let class = class.to_lowercase();
            let named = way_in
                .iter()
                .filter(|w| w.name.to_lowercase().contains(&class));
            named.collect()
        }
        None => Vec::new(),
    };
    match (named.as_slice(), way_in) {
        ([one], _) => Some(one),
        ([], [only]) => Some(only),
        _ => None,
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::super::controller::fixtures::controller;
    use super::*;

    fn deployment(name: &str) -> Object {
        Object::new("apps", "Deployment", Some("edge"), name)
    }

    #[rstest]
    #[case::named_for_its_class(Some("web"), &["web-proxy", "mail"], Some("web-proxy"))]
    #[case::the_only_way_in(Some("web"), &["gateway"], Some("gateway"))]
    #[case::no_class_and_the_only_way_in(None, &["gateway"], Some("gateway"))]
    #[case::several_none_named(Some("web"), &["gateway", "mail"], None)]
    #[case::no_way_in(Some("web"), &[], None)]
    fn guesses_the_workload_that_serves_it(
        #[case] class: Option<&str>,
        #[case] way_in: &[&str],
        #[case] expected: Option<&str>,
    ) {
        let serving = Serving {
            way_in: way_in.iter().map(|name| deployment(name)).collect(),
            ..Serving::default()
        };
        let expected = expected.map(|name| Served::Guessed(deployment(name)));
        assert_eq!(serving.of(class), expected);
    }

    /// A pod of the `Deployment` `edge/<workload>` that runs `image`.
    fn pod(workload: &str, image: &str) -> Arc<Pod> {
        let text = format!(
            "{{metadata: {{namespace: edge, name: {workload}-1, ownerReferences: [{{apiVersion: apps/v1, kind: Deployment, name: {workload}, uid: {workload}, controller: true}}]}}, spec: {{containers: [{{name: main, image: '{image}'}}]}}, status: {{phase: Running}}}}"
        );
        Arc::new(serde_yaml::from_str(&text).unwrap())
    }

    fn class(name: &str, controller: &str) -> Arc<IngressClass> {
        let text = format!("{{metadata: {{name: {name}}}, spec: {{controller: {controller}}}}}");
        Arc::new(serde_yaml::from_str(&text).unwrap())
    }

    /// The fixture: the class `web` names `example.io/proxy`, and the
    /// workloads `router` and `mail` receive web traffic.
    fn serving(pods: &[Arc<Pod>], declared: bool) -> Serving {
        let ownership =
            Ownership::build(pods.iter().map(|pod| transect_cluster::ownership::Entry {
                group: "",
                kind: "Pod",
                meta: &pod.metadata,
            }));
        let controllers: Vec<IngressController> = match declared {
            true => vec![controller(
                "{controller: example.io/proxy, image: example/proxy}",
            )],
            false => Vec::new(),
        };
        Serving::build(
            &[class("web", "example.io/proxy")],
            &controllers,
            pods,
            &ownership,
            vec![deployment("router"), deployment("mail")],
        )
    }

    #[rstest]
    #[case::the_one_that_runs_its_image(
        &[("router", "ghcr.io/example/proxy:3"), ("mail", "example/mail:1")],
        true,
        Some(Served::Declared(deployment("router")))
    )]
    #[case::two_that_run_it(
        &[("router", "example/proxy:3"), ("mail", "example/proxy:3")],
        true,
        None
    )]
    #[case::none_that_runs_it(&[("mail", "example/mail:1")], true, None)]
    #[case::undeclared_and_unnamed(&[("router", "example/proxy:3")], false, None)]
    fn finds_the_workload_that_runs_the_declared_image(
        #[case] pods: &[(&str, &str)],
        #[case] declared: bool,
        #[case] expected: Option<Served>,
    ) {
        let pods: Vec<Arc<Pod>> = pods.iter().map(|(w, i)| pod(w, i)).collect();
        assert_eq!(serving(&pods, declared).of(Some("web")), expected);
    }
}
