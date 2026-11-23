//! Finds the workload that runs the controller of an `Ingress`.
//!
//! An `Ingress` has a class, and its `IngressClass` has a controller
//! identifier. An `IngressController` resource that declares the same
//! identifier gives the image that the controller runs, so the workload
//! is the one whose pods run that image.
//!
//! When no `IngressController` declares the identifier, transect guesses
//! among the workloads that a `LoadBalancer` `Service` sends web traffic
//! to, because an ingress controller usually receives its traffic that
//! way. The page marks the result as a guess.

use std::collections::{BTreeSet, HashMap};
use std::sync::Arc;

use k8s_openapi::api::core::v1::Pod;
use k8s_openapi::api::networking::v1::IngressClass;
use kube::ResourceExt;

use super::controller::IngressController;
use transect_cluster::ownership::{Ownership, runs_image};
use transect_graph::edges::Object;
use transect_graph::pods::finished;

/// The cluster state that `Serving::of` reads to find the workload
/// that runs an `IngressClass`'s controller.
#[derive(Debug, Default)]
pub struct Serving {
    /// The controller identifier from each `IngressClass`'s spec, by
    /// class name.
    classes: HashMap<String, String>,
    /// For each controller identifier that an `IngressController`
    /// declares, the workloads with a running pod that uses its image.
    declared: HashMap<String, BTreeSet<Object>>,
    /// The workloads that a `LoadBalancer` `Service` sends web traffic
    /// to, which are the candidates when a controller is not declared.
    load_balanced: Vec<Object>,
}

/// The workload that runs an `Ingress`'s controller, and how transect
/// found it.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Served {
    /// Its pods run the image that an `IngressController` declares for
    /// the class's controller.
    Declared(Object),
    /// Chosen by `guess` from the workloads that a `LoadBalancer`
    /// `Service` sends web traffic to.
    Guessed(Object),
}

impl Serving {
    pub fn build(
        classes: &[Arc<IngressClass>],
        controllers: &[IngressController],
        pods: &[Arc<Pod>],
        ownership: &Ownership,
        load_balanced: Vec<Object>,
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
            load_balanced,
        }
    }

    /// The workload that runs `class`'s controller. When an
    /// `IngressController` declares that controller, the result is the
    /// one workload that runs its image, or none if zero or several do.
    /// Otherwise the result is a guess, if there is one.
    pub fn of(&self, class: Option<&str>) -> Option<Served> {
        let controller = class.and_then(|class| self.classes.get(class));
        if let Some(workloads) = controller.and_then(|c| self.declared.get(c)) {
            let mut workloads = workloads.iter();
            return match (workloads.next(), workloads.next()) {
                (Some(one), None) => Some(Served::Declared(one.clone())),
                _ => None,
            };
        }
        guess(class, &self.load_balanced).map(|found| Served::Guessed(found.clone()))
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

/// Guesses which workload of `load_balanced` runs `class`'s controller.
/// The guess is the only workload whose name contains the class's name.
/// When no name contains it, the guess is the only workload in the list.
/// Otherwise there is no guess.
fn guess<'a>(class: Option<&str>, load_balanced: &'a [Object]) -> Option<&'a Object> {
    let named: Vec<&Object> = match class {
        Some(class) => {
            let class = class.to_lowercase();
            let named = load_balanced
                .iter()
                .filter(|w| w.name.to_lowercase().contains(&class));
            named.collect()
        }
        None => Vec::new(),
    };
    match (named.as_slice(), load_balanced) {
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
    #[case::the_only_load_balanced_workload(Some("web"), &["gateway"], Some("gateway"))]
    #[case::no_class_and_one_load_balanced_workload(None, &["gateway"], Some("gateway"))]
    #[case::several_and_none_named_for_the_class(Some("web"), &["gateway", "mail"], None)]
    #[case::no_load_balanced_workload(Some("web"), &[], None)]
    fn guesses_the_workload_that_serves_it(
        #[case] class: Option<&str>,
        #[case] load_balanced: &[&str],
        #[case] expected: Option<&str>,
    ) {
        let serving = Serving {
            load_balanced: load_balanced.iter().map(|name| deployment(name)).collect(),
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
        Arc::new(serde_yaml_ng::from_str(&text).unwrap())
    }

    fn class(name: &str, controller: &str) -> Arc<IngressClass> {
        let text = format!("{{metadata: {{name: {name}}}, spec: {{controller: {controller}}}}}");
        Arc::new(serde_yaml_ng::from_str(&text).unwrap())
    }

    /// A `Serving` where the class `web` has the controller
    /// `example.io/proxy`, and `router` and `mail` are the workloads a
    /// `LoadBalancer` `Service` sends web traffic to. `declared` sets
    /// whether an `IngressController` declares the controller.
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
    #[case::one_workload_runs_its_image(
        &[("router", "ghcr.io/example/proxy:3"), ("mail", "example/mail:1")],
        true,
        Some(Served::Declared(deployment("router")))
    )]
    #[case::two_workloads_run_it(
        &[("router", "example/proxy:3"), ("mail", "example/proxy:3")],
        true,
        None
    )]
    #[case::no_workload_runs_it(&[("mail", "example/mail:1")], true, None)]
    #[case::undeclared_and_not_named_for_the_class(&[("router", "example/proxy:3")], false, None)]
    fn finds_the_workload_that_runs_the_declared_image(
        #[case] pods: &[(&str, &str)],
        #[case] declared: bool,
        #[case] expected: Option<Served>,
    ) {
        let pods: Vec<Arc<Pod>> = pods.iter().map(|(w, i)| pod(w, i)).collect();
        assert_eq!(serving(&pods, declared).of(Some("web")), expected);
    }
}
