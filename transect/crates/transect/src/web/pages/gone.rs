//! The page for a URL whose object is no longer in the cluster.

use super::super::model::Model;
use transect_map::map::{Above, Gone, GonePage};

/// Links to the namespace when it still exists, and for a pod, to the
/// workload that owned it when that workload is still in the
/// namespace.
pub fn gone_page(model: &Model, gone: &Gone) -> GonePage {
    let snapshot = &model.snapshot;
    let (namespace, pod) = match gone {
        Gone::Namespace(namespace) => (Some(namespace), None),
        Gone::Workload { namespace, .. } | Gone::Kind { namespace, .. } => (Some(namespace), None),
        Gone::Pod { namespace, name } => (Some(namespace), Some(name)),
        Gone::Machine(_)
        | Gone::Ground(_)
        | Gone::ClusterObject { .. }
        | Gone::AllOfKind(_)
        | Gone::Subject { .. }
        | Gone::Vulnerability(_) => (None, None),
    };
    let exists = snapshot
        .namespaces
        .iter()
        .any(|object| object.metadata.name.as_ref() == namespace);
    let workload = namespace.zip(pod).and_then(|(namespace, name)| {
        let owner = model.departed.owner(namespace, name)?;
        snapshot
            .ownership
            .workloads(namespace)
            .any(|w| w == owner)
            .then_some(owner)
    });
    let seen = match gone {
        Gone::Pod { namespace, name } => model.departed.owner(namespace, name).is_some(),
        _ => false,
    };
    let above = Above {
        namespace: exists,
        workload,
        seen,
        plurals: &snapshot.plurals,
    };
    GonePage::build(gone, above)
}
