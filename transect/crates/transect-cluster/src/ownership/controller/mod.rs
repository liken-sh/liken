//! Finds the workload that reconciles each custom resource that is a
//! top owner.
//!
//! The API does not record an object's controller, so this infers it.
//! The candidates are the top owners of the pods that run as a
//! `ServiceAccount` that RBAC lets write the kind's status. When a
//! candidate's name matches a field manager that wrote the object's
//! status in `managedFields`, only the matching candidates are kept.
//! When the kind's knowledge gives its controller's image and a
//! candidate runs it, the candidates that run it are used instead. When
//! more than one candidate is left, each edge is marked as a guess.
//! `by_kind` attributes the objects that this leaves without a
//! controller.

mod by_kind;
mod grants;
mod managers;
mod packaged;

use std::collections::{BTreeMap, BTreeSet, HashMap};

use k8s_openapi::api::core::v1::Pod;

use super::{Ownership, custom};
pub use by_kind::{Sources as KindSources, controlled_by_kind};
use grants::ServiceAccount;
pub use grants::{Grants, Rbac};
pub use managers::runs_image;
use managers::{Candidate, narrow, running};
pub use packaged::{Package, Packages, kept as package_key, package};
use transect_graph::edges::{Edge, Endpoint, Kind, Object, Plurals, Provenance};
use transect_graph::pods::finished;
use transect_resources::knowledge::Kinds;

/// The field managers that write each custom resource's status, read
/// from its `managedFields` entries for the `status` subresource.
pub type StatusManagers = HashMap<Object, Vec<String>>;

/// Returns a controlled-by edge from each custom top owner in
/// `ownership` to each candidate that is left as its controller.
///
/// `pods` is every pod in the cluster. When RBAC lets no account write
/// the kind's status, the search by image looks at every workload that
/// runs a pod. `cluster_scoped` adds the custom resources outside any
/// namespace, because one that owns no namespaced object is not a
/// workload, but it can still have a controller, such as an operator's
/// record of a device.
pub fn controlled_by(
    grants: &Grants,
    managers: &StatusManagers,
    plurals: &Plurals,
    pods: &[&Pod],
    ownership: &Ownership,
    kinds: &Kinds,
    cluster_scoped: &[Object],
) -> Vec<Edge> {
    let by_account = workloads_by_account(pods, ownership);
    let workloads = ownership.all_workloads().filter(|o| custom(o));
    let outside = cluster_scoped
        .iter()
        .filter(|o| custom(o) && o.namespace.is_none());
    let resources: BTreeSet<&Object> = workloads.chain(outside).collect();
    let mut edges = Vec::new();
    for resource in resources {
        // RBAC rules name a kind by its plural, so a kind without a
        // discovered plural cannot be matched to a rule.
        let Some(plural) = plurals.get(&(resource.group.clone(), resource.kind.clone())) else {
            continue;
        };
        let writers = grants.writers(&resource.group, plural, resource.namespace.as_deref());
        let mut candidates: BTreeMap<&Object, Candidate> = BTreeMap::new();
        for account in writers {
            for (workload, candidate) in by_account.get(account).into_iter().flatten() {
                if workload == resource {
                    continue;
                }
                candidates
                    .entry(workload)
                    .or_insert_with(|| candidate.clone());
            }
        }
        let status_writers = managers
            .get(resource)
            .map(Vec::as_slice)
            .unwrap_or_default();
        let rbac: Vec<Candidate> = candidates.into_values().collect();
        let knowledge = kinds.of_object(resource);
        let image = knowledge.and_then(|k| Some((k, k.controller_image.as_deref()?)));
        let by_image = image.and_then(|(knowledge, image)| {
            let pool = match rbac.is_empty() {
                true => other_workloads(&by_account, resource),
                false => rbac.clone(),
            };
            Some((
                running(&pool, image)?,
                knowledge.provenance("controller.image"),
            ))
        });
        let (left, provenance) =
            by_image.unwrap_or_else(|| (narrow(rbac, status_writers), Provenance::Kubernetes));
        let guessed = left.len() > 1;
        edges.extend(left.into_iter().map(|candidate| Edge {
            kind: Kind::ControlledBy,
            from: Endpoint::Object(resource.clone()),
            to: Endpoint::Object(candidate.workload),
            guessed,
            provenance: provenance.clone(),
        }));
    }
    edges
}

/// Every workload that runs a pod, except `resource` itself.
fn other_workloads(
    by_account: &HashMap<ServiceAccount, BTreeMap<Object, Candidate>>,
    resource: &Object,
) -> Vec<Candidate> {
    let all = by_account.values().flat_map(|workloads| workloads.values());
    let others = all.filter(|candidate| candidate.workload != *resource);
    let mut found: BTreeMap<&Object, Candidate> = BTreeMap::new();
    for candidate in others {
        found
            .entry(&candidate.workload)
            .or_insert_with(|| candidate.clone());
    }
    found.into_values().collect()
}

/// Groups the top owners of running pods by the `ServiceAccount` that
/// the pods run as. A finished pod is skipped because it runs no
/// controller.
fn workloads_by_account<'a>(
    pods: &[&'a Pod],
    ownership: &'a Ownership,
) -> HashMap<ServiceAccount, BTreeMap<Object, Candidate>> {
    let mut found: HashMap<ServiceAccount, BTreeMap<Object, Candidate>> = HashMap::new();
    for &pod in pods.iter().filter(|pod| !finished(pod)) {
        let meta = &pod.metadata;
        let (Some(namespace), Some(name)) = (&meta.namespace, &meta.name) else {
            continue;
        };
        let spec = pod.spec.as_ref();
        let account = spec.and_then(|spec| spec.service_account_name.clone());
        let account = (
            namespace.clone(),
            account.unwrap_or_else(|| "default".into()),
        );
        let object = Object::new("", "Pod", Some(namespace), name);
        let top = ownership.top_owner(&object);
        let workloads = found.entry(account).or_default();
        let candidate = workloads
            .entry(top.clone())
            .or_insert_with(|| Candidate::new(top));
        candidate.add_pod(pod);
    }
    found
}

#[cfg(test)]
mod tests;
