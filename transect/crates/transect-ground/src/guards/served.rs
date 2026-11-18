//! The pod templates of a workload that a `Service` sends traffic to.

use std::collections::BTreeMap;
use std::sync::Arc;

use k8s_openapi::api::core::v1::{PodSpec, Service};
use kube::ResourceExt;

use transect_cluster::workloads::Workload;

/// The templates of `workload` whose labels a `Service` in `namespace`
/// selects.  A `Service` with no selector, or an empty one, selects no
/// template, because Kubernetes does not choose its endpoints by label.
pub fn templates<'a>(
    workload: &Workload<'_, 'a>,
    services: &'a [Arc<Service>],
    namespace: &'a str,
) -> impl Iterator<Item = &'a PodSpec> {
    let selectors: Vec<&BTreeMap<String, String>> = services
        .iter()
        .filter(|service| service.namespace().as_deref() == Some(namespace))
        .filter_map(|service| service.spec.as_ref()?.selector.as_ref())
        .filter(|selector| !selector.is_empty())
        .collect();
    let labelled = workload.labelled_templates();
    labelled.into_iter().filter_map(move |(labels, spec)| {
        let selected = selectors.iter().any(|selector| {
            selector
                .iter()
                .all(|(key, value)| labels.get(key) == Some(value))
        });
        selected.then_some(spec)
    })
}
