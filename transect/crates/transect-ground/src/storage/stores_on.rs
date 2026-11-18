//! The `StoresOn` edges, from each workload to where the volumes that
//! its pods mount store their data.

use std::collections::HashSet;

use k8s_openapi::api::core::v1::Pod;

use super::mounts::pod_claims;
use super::{Backend, Claim, Storage};
use transect_cluster::ownership::Ownership;
use transect_graph::edges::{Edge, Endpoint, Kind, Object, Provenance};

/// One edge for each workload and claim, from the top owner of the pods
/// that mount the claim to the claim's backend: a server outside the
/// cluster, or the machine a local volume is on.  Replicas that share a
/// claim give one edge, so a workload's edges count its claims.  A claim
/// that no pod mounts, such as a scaled-down workload's, gets no edge.
pub fn stores_on<'a>(
    storage: &Storage,
    pods: impl IntoIterator<Item = &'a Pod>,
    ownership: &Ownership,
) -> Vec<Edge> {
    let mut seen: HashSet<(Object, Claim)> = HashSet::new();
    let mut edges = Vec::new();
    for pod in pods {
        let (Some(namespace), Some(name)) = (
            pod.metadata.namespace.as_deref(),
            pod.metadata.name.as_deref(),
        ) else {
            continue;
        };
        let owner = ownership.top_owner(&Object::new("", "Pod", Some(namespace), name));
        for claim in pod_claims(pod, namespace, name) {
            let Some(volume) = storage.volume_of(&claim) else {
                continue;
            };
            let Some(to) = endpoint(&volume.backend, pod) else {
                continue;
            };
            if seen.insert((owner.clone(), claim)) {
                edges.push(Edge {
                    kind: Kind::StoresOn,
                    from: Endpoint::Object(owner.clone()),
                    to,
                    guessed: false,
                    provenance: volume.declared_in.clone().unwrap_or(Provenance::Kubernetes),
                });
            }
        }
    }
    edges
}

/// Each pair of a workload and an NFS server that one of its pods mounts
/// directly in its volumes, with no claim.  `stores_on` gives these
/// mounts no edge, so the server's ground page adds them to the
/// workloads that store on the server.
pub fn inline_nfs<'a>(
    pods: impl IntoIterator<Item = &'a Pod>,
    ownership: &Ownership,
) -> Vec<(Object, &'a str)> {
    let mut found: Vec<(Object, &str)> = Vec::new();
    for pod in pods {
        let (Some(namespace), Some(name), Some(spec)) = (
            pod.metadata.namespace.as_deref(),
            pod.metadata.name.as_deref(),
            pod.spec.as_ref(),
        ) else {
            continue;
        };
        let owner = ownership.top_owner(&Object::new("", "Pod", Some(namespace), name));
        let servers = spec.volumes.iter().flatten();
        for server in servers.filter_map(|volume| volume.nfs.as_ref()) {
            let pair = (owner.clone(), server.server.as_str());
            if !found.contains(&pair) {
                found.push(pair);
            }
        }
    }
    found
}

/// A local volume that names no machine stores its data on the pod's
/// machine, so this returns `None` for a pod that is not scheduled yet.
fn endpoint(backend: &Backend, pod: &Pod) -> Option<Endpoint> {
    match backend {
        Backend::Address(name) | Backend::Driver(name) => Some(Endpoint::Outside(name.clone())),
        Backend::Machine(Some(machine)) => Some(Endpoint::Machine(machine.clone())),
        Backend::Machine(None) => {
            let node = pod.spec.as_ref()?.node_name.clone()?;
            Some(Endpoint::Machine(node))
        }
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;
    use crate::storage::fixtures::{claim, storage, volume};
    use transect_cluster::ownership::Entry;
    use transect_cluster::ownership::fixtures::meta;

    /// A pod in shop, owned by the `ReplicaSet` `owner` when one is given,
    /// on `node`, with `volumes` as YAML.
    fn pod(name: &str, owner: Option<&str>, node: &str, volumes: &str) -> Pod {
        let mut pod: Pod = serde_yaml::from_str(&format!(
            "spec: {{nodeName: {node}, containers: [], volumes: {volumes}}}"
        ))
        .unwrap();
        let owners: Vec<(&str, &str, &str)> = owner
            .map(|owner| ("apps/v1", "ReplicaSet", owner))
            .into_iter()
            .collect();
        pod.metadata = meta(Some("shop"), name, &owners);
        pod
    }

    fn ownership(pods: &[Pod]) -> Ownership {
        let replica_set = meta(
            Some("shop"),
            "web-5d8f",
            &[("apps/v1", "Deployment", "web")],
        );
        let deployment = meta(Some("shop"), "web", &[]);
        let entries = pods
            .iter()
            .map(|pod| Entry {
                group: "",
                kind: "Pod",
                meta: &pod.metadata,
            })
            .chain([
                Entry {
                    group: "apps",
                    kind: "ReplicaSet",
                    meta: &replica_set,
                },
                Entry {
                    group: "apps",
                    kind: "Deployment",
                    meta: &deployment,
                },
            ]);
        Ownership::build(entries)
    }

    fn web() -> Endpoint {
        Endpoint::Object(Object::new("apps", "Deployment", Some("shop"), "web"))
    }

    fn edges(storage: &Storage, pods: &[Pod]) -> Vec<(Endpoint, Endpoint)> {
        let ownership = ownership(pods);
        stores_on(storage, pods, &ownership)
            .into_iter()
            .map(|edge| (edge.from, edge.to))
            .collect()
    }

    const MEDIA: &str = "[{name: media, persistentVolumeClaim: {claimName: media}}]";

    #[rstest]
    #[case::one_per_workload_and_server(
        vec![
            pod("web-1", Some("web-5d8f"), "beta", "[{name: a, nfs: {server: nas.example.com, path: /a}}]"),
            pod("web-2", Some("web-5d8f"), "gamma", "[{name: a, nfs: {server: nas.example.com, path: /a}}]"),
        ],
        vec![("web", "nas.example.com")]
    )]
    #[case::a_claim_is_not_inline(vec![pod("web-1", Some("web-5d8f"), "beta", MEDIA)], vec![])]
    fn finds_the_nfs_exports_a_pod_names_by_hand(
        #[case] pods: Vec<Pod>,
        #[case] expected: Vec<(&str, &str)>,
    ) {
        let ownership = ownership(&pods);
        let found = inline_nfs(&pods, &ownership);
        let found: Vec<(&str, &str)> = found
            .iter()
            .map(|(owner, server)| (owner.name.as_str(), *server))
            .collect();
        assert_eq!(found, expected);
    }

    #[rstest]
    #[case::from_the_top_owner(
        vec![pod("web-1", Some("web-5d8f"), "beta", MEDIA)],
        vec![(web(), Endpoint::Outside("nas.example.com".into()))]
    )]
    #[case::replicas_sharing_a_claim_give_one_edge(
        vec![
            pod("web-1", Some("web-5d8f"), "beta", MEDIA),
            pod("web-2", Some("web-5d8f"), "gamma", MEDIA),
        ],
        vec![(web(), Endpoint::Outside("nas.example.com".into()))]
    )]
    #[case::a_local_volume_stores_on_its_machine(
        vec![pod("cache", None, "beta", "[{name: cache, persistentVolumeClaim: {claimName: cache}}]")],
        vec![(
            Endpoint::Object(Object::new("", "Pod", Some("shop"), "cache")),
            Endpoint::Machine("alpha".into()),
        )]
    )]
    #[case::a_waiting_claim_stores_nowhere(
        vec![pod("web-1", Some("web-5d8f"), "beta", "[{name: w, persistentVolumeClaim: {claimName: waiting}}]")],
        vec![]
    )]
    #[case::a_config_map_is_not_storage(
        vec![pod("web-1", Some("web-5d8f"), "beta", "[{name: settings, configMap: {name: settings}}]")],
        vec![]
    )]
    fn a_workload_stores_on_its_claims_backends(
        #[case] pods: Vec<Pod>,
        #[case] expected: Vec<(Endpoint, Endpoint)>,
    ) {
        assert_eq!(edges(&storage(), &pods), expected);
    }

    #[rstest]
    fn an_ephemeral_claim_and_a_host_path_on_the_pods_machine() {
        let volumes = [volume("pv-scratch", "hostPath: {path: /scratch}")];
        let claims = [claim("shop", "web-1-scratch", Some("pv-scratch"))];
        let storage = Storage::build(&claims, &volumes, &[]);
        let pods = [pod(
            "web-1",
            Some("web-5d8f"),
            "beta",
            "[{name: scratch, ephemeral: {volumeClaimTemplate: {spec: {}}}}]",
        )];
        assert_eq!(
            edges(&storage, &pods),
            vec![(web(), Endpoint::Machine("beta".into()))]
        );
    }
}
