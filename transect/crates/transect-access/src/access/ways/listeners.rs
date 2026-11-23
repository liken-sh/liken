//! Listener pods of a distribution's load balancer.  A load balancer
//! can serve a LoadBalancer Service by running a pod on each machine
//! that opens the Service's ports as host ports.  These pods are part
//! of the Service's way in, so they get no rows of their own.
//!
//! `transect` has no code for any one distribution.  It finds a
//! listener when the pod's DaemonSet has an owner reference to the
//! Service, or when a `LoadBalancerController` names the label or
//! annotation of the pod that holds the Service's name and namespace:
//!
//! ```yaml
//! apiVersion: transect.liken.sh/v1alpha1
//! kind: LoadBalancerController
//! metadata:
//!   name: a-load-balancer
//! spec:
//!   listener:
//!     name: {label: lb.example.io/service-name}
//!     namespace: {label: lb.example.io/service-namespace}
//! ```

use k8s_openapi::api::apps::v1::DaemonSet;
use k8s_openapi::api::core::v1::Pod;
use k8s_openapi::apimachinery::pkg::apis::meta::v1::ObjectMeta;
use serde::Deserialize;

use transect_resources::knowledge::MetaKey;
use transect_resources::resources::DocId;

/// One `LoadBalancerController`, validated.
#[derive(Clone, Debug, PartialEq)]
pub struct LoadBalancerController {
    pub doc: DocId,
    pub name: String,
    /// The label or annotation of a listener pod that holds the
    /// Service's name.
    pub service: MetaKey,
    /// The label or annotation of a listener pod that holds the
    /// Service's namespace.
    pub namespace: MetaKey,
}

#[derive(Deserialize)]
struct Document {
    metadata: Metadata,
    spec: Spec,
}

#[derive(Deserialize)]
struct Metadata {
    name: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Spec {
    listener: Listener,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Listener {
    name: Key,
    namespace: Key,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Key {
    label: Option<String>,
    annotation: Option<String>,
}

impl Key {
    fn read(self, field: &str) -> Result<MetaKey, String> {
        match (self.label, self.annotation) {
            (Some(label), None) if !label.is_empty() => Ok(MetaKey::Label(label)),
            (None, Some(annotation)) if !annotation.is_empty() => {
                Ok(MetaKey::Annotation(annotation))
            }
            _ => Err(format!(
                "listener.{field} must set exactly one of label and annotation, to a non-empty key"
            )),
        }
    }
}

/// The `LoadBalancerController` that `value` declares, or why `value`
/// is not valid.
pub fn parse(doc: DocId, value: serde_yaml_ng::Value) -> Result<LoadBalancerController, String> {
    let document: Document = serde_yaml_ng::from_value(value).map_err(|error| error.to_string())?;
    let listener = document.spec.listener;
    Ok(LoadBalancerController {
        doc,
        name: document.metadata.name,
        service: listener.name.read("name")?,
        namespace: listener.namespace.read("namespace")?,
    })
}

/// The value of the label or annotation `key` in `meta`.
fn value<'a>(meta: &'a ObjectMeta, key: &MetaKey) -> Option<&'a str> {
    let map = match key {
        MetaKey::Label(_) => meta.labels.as_ref(),
        MetaKey::Annotation(_) => meta.annotations.as_ref(),
    };
    let key = match key {
        MetaKey::Label(key) | MetaKey::Annotation(key) => key,
    };
    map?.get(key).map(String::as_str)
}

/// The namespace and name of the Service that `pod` is a listener for,
/// or `None` when it is not a listener.  A `LoadBalancerController`'s
/// label or annotation is checked first, and then the Service that owns
/// the pod's DaemonSet.
pub fn service_of(
    pod: &Pod,
    daemon_sets: &[std::sync::Arc<DaemonSet>],
    controllers: &[LoadBalancerController],
) -> Option<(String, String)> {
    for controller in controllers {
        let name = value(&pod.metadata, &controller.service);
        let namespace = value(&pod.metadata, &controller.namespace);
        if let (Some(name), Some(namespace)) = (name, namespace) {
            return Some((namespace.into(), name.into()));
        }
    }
    let namespace = pod.metadata.namespace.as_deref()?;
    let owners = pod.metadata.owner_references.iter().flatten();
    let set = owners
        .filter(|owner| owner.kind == "DaemonSet")
        .find_map(|owner| {
            daemon_sets.iter().find(|set| {
                set.metadata.namespace.as_deref() == Some(namespace)
                    && set.metadata.name.as_deref() == Some(owner.name.as_str())
            })
        })?;
    let owners = set.metadata.owner_references.iter().flatten();
    let service = owners
        .into_iter()
        .find(|owner| owner.kind == "Service" && owner.api_version == "v1")?;
    Some((namespace.into(), service.name.clone()))
}

/// Whether any of `pods` has both the label or annotation that
/// `controller` reads for a Service's name and the one it reads for its
/// namespace.
pub fn matches_any(controller: &LoadBalancerController, pods: &[std::sync::Arc<Pod>]) -> bool {
    pods.iter().any(|pod| {
        value(&pod.metadata, &controller.service).is_some()
            && value(&pod.metadata, &controller.namespace).is_some()
    })
}

#[cfg(test)]
mod tests {
    use std::sync::Arc;

    use rstest::rstest;

    use super::*;

    fn read(spec: &str) -> Result<LoadBalancerController, String> {
        let text = format!("metadata: {{name: lb}}\nspec: {spec}\n");
        parse(DocId::default(), serde_yaml_ng::from_str(&text).unwrap())
    }

    #[rstest]
    #[case::two_labels(
        "{listener: {name: {label: a/name}, namespace: {label: a/namespace}}}",
        None
    )]
    #[case::neither(
        "{listener: {name: {}, namespace: {label: a/namespace}}}",
        Some("listener.name must set exactly one")
    )]
    #[case::both(
        "{listener: {name: {label: a, annotation: b}, namespace: {label: c}}}",
        Some("listener.name must set exactly one")
    )]
    #[case::an_unknown_field(
        "{listener: {name: {label: a}, namespace: {label: b}}, port: 1}",
        Some("unknown field")
    )]
    fn parses_a_controller_or_reports_why_not(
        #[case] spec: &str,
        #[case] expected_error: Option<&str>,
    ) {
        let found = read(spec).err();
        assert_eq!(
            found.is_some(),
            expected_error.is_some(),
            "the error is {found:?}, want an error only for an invalid spec"
        );
        if let (Some(error), Some(expected_error)) = (found, expected_error) {
            assert!(
                error.contains(expected_error),
                "the error is {error:?}, want it to contain {expected_error:?}"
            );
        }
    }

    fn pod(text: &str) -> Pod {
        serde_yaml_ng::from_str(text).unwrap()
    }

    #[rstest]
    #[case::by_its_labels("{metadata: {namespace: system, name: lb-1, labels: {a/name: web, a/namespace: shop}}}", Some(("shop", "web")))]
    #[case::by_its_daemonsets_owner("{metadata: {namespace: shop, name: lb-2, ownerReferences: [{apiVersion: apps/v1, kind: DaemonSet, name: web-lb, uid: a}]}}", Some(("shop", "web")))]
    #[case::neither("{metadata: {namespace: shop, name: other}}", None)]
    fn finds_the_service_that_a_listener_serves(
        #[case] text: &str,
        #[case] expected: Option<(&str, &str)>,
    ) {
        let controllers = vec![
            read("{listener: {name: {label: a/name}, namespace: {label: a/namespace}}}").unwrap(),
        ];
        let set: DaemonSet = serde_yaml_ng::from_str(
            "{metadata: {namespace: shop, name: web-lb, ownerReferences: [{apiVersion: v1, kind: Service, name: web, uid: b}]}}",
        )
        .unwrap();
        let found = service_of(&pod(text), &[Arc::new(set)], &controllers);
        assert_eq!(found, expected.map(|(n, s)| (n.to_string(), s.to_string())));
    }
}
