//! Builds the rows below the workload in the section view's main
//! column: the workload's claims, grouped by storage backend with the
//! machines that mount them, and the machines that its pods run on.

use std::collections::{BTreeMap, BTreeSet};

use k8s_openapi::api::core::v1::{Pod, PodSpec};

use super::spine::{Backing, Held, Host};
use transect_graph::edges::Claim;
use transect_ground::machines::Machine;
use transect_ground::storage::{self, Backend, Storage};

/// Groups the claims that `pods` mount, or that `templates` name when
/// there is no pod, into one backing per backend, in the order the
/// workload page lists them.  It adds each inline NFS volume to the
/// backing of its server.  Two kinds of volume on one server, such as
/// NFS and iSCSI, share one backend, because the ground row draws one
/// box for the server.
pub fn backings(
    storage: &Storage,
    namespace: &str,
    pods: &[&Pod],
    templates: &[&PodSpec],
) -> Vec<Backing> {
    let mut mounted: BTreeMap<String, BTreeSet<String>> = BTreeMap::new();
    for pod in pods {
        let spec = pod.spec.as_ref();
        let (Some(name), Some(machine)) = (
            pod.metadata.name.as_deref(),
            spec.and_then(|spec| spec.node_name.as_deref()),
        ) else {
            continue;
        };
        for claim in storage::pod_claims(pod, namespace, name) {
            mounted
                .entry(claim.name)
                .or_default()
                .insert(machine.into());
        }
    }
    let mounts = storage::mounts(storage, namespace, pods, templates);
    let mut backings: Vec<Backing> = Vec::new();
    for group in mounts.groups {
        let backend = group.volume.as_ref().map(|volume| volume.backend.clone());
        let kind = group.volume.map(|volume| volume.kind);
        let backing = backing_for(&mut backings, backend);
        for mount in group.mounts {
            let claim = Claim::new(namespace, &mount.claim);
            let machines = mounted.get(&claim.name).into_iter().flatten().cloned();
            backing.machines.extend(machines);
            backing.claims.push(Held {
                href: transect_graph::links::claim(namespace, &claim.name),
                name: claim.name,
                size: mount.size,
                kind: kind.clone(),
            });
        }
    }
    for (volume, server, machine) in inline_nfs(pods, templates) {
        let backend = Some(Backend::Address(server.into()));
        let backing = backing_for(&mut backings, backend);
        if !backing.inline.iter().any(|name| name == volume) {
            backing.inline.push(volume.into());
        }
        backing.machines.extend(machine.map(String::from));
    }
    backings
}

/// The backing of `backend`, added last when there is none yet.
fn backing_for(backings: &mut Vec<Backing>, backend: Option<Backend>) -> &mut Backing {
    let index = match backings.iter().position(|b| b.backend == backend) {
        Some(index) => index,
        None => {
            backings.push(Backing {
                backend,
                claims: Vec::new(),
                inline: Vec::new(),
                machines: BTreeSet::new(),
            });
            backings.len() - 1
        }
    };
    &mut backings[index]
}

/// Each NFS volume that `pods`, or `templates` when there is no pod,
/// declare inline: its name, its server, and the machine of the pod
/// that mounts it.  Such a volume has no claim, so the volumes row has
/// no box for it, and its backing records the pod's machine instead.
fn inline_nfs<'a>(
    pods: &[&'a Pod],
    templates: &[&'a PodSpec],
) -> Vec<(&'a str, &'a str, Option<&'a str>)> {
    let specs: Vec<(&PodSpec, Option<&str>)> = match pods.is_empty() {
        false => pods
            .iter()
            .filter_map(|pod| pod.spec.as_ref())
            .map(|spec| (spec, spec.node_name.as_deref()))
            .collect(),
        true => templates.iter().map(|&spec| (spec, None)).collect(),
    };
    let mut found = Vec::new();
    for (spec, machine) in specs {
        for volume in spec.volumes.iter().flatten() {
            if let Some(nfs) = &volume.nfs {
                found.push((volume.name.as_str(), nfs.server.as_str(), machine));
            }
        }
    }
    found
}

/// The machines `pods` run on, by name, with how many of the pods each
/// runs.  A pod that is not scheduled yet counts for no machine.
pub fn hosts(pods: &[&Pod], machines: &[Machine]) -> Vec<Host> {
    let mut counts: BTreeMap<&str, usize> = BTreeMap::new();
    for pod in pods {
        if let Some(machine) = pod.spec.as_ref().and_then(|s| s.node_name.as_deref()) {
            *counts.entry(machine).or_default() += 1;
        }
    }
    counts
        .into_iter()
        .map(|(name, pods)| Host {
            name: name.into(),
            pods,
            control_plane: machines
                .iter()
                .any(|machine| machine.name == name && machine.control_plane),
        })
        .collect()
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;
    use transect_ground::storage::fixtures::storage;
    use transect_ground::storage::{Backend, Type};

    /// A pod in shop named `name` on `machine` that mounts `claims`.
    fn pod(name: &str, machine: &str, claims: &[&str]) -> Pod {
        let volumes: Vec<String> = claims
            .iter()
            .map(|claim| {
                format!("{{name: {claim}, persistentVolumeClaim: {{claimName: {claim}}}}}")
            })
            .collect();
        serde_yaml::from_str(&format!(
            "metadata: {{namespace: shop, name: {name}}}\nspec: {{nodeName: {machine}, containers: [], volumes: [{}]}}",
            volumes.join(", ")
        ))
        .unwrap()
    }

    /// Each backing as its backend, its claims' names, and its machines.
    fn outline(backings: &[Backing]) -> Vec<(Option<Backend>, Vec<&str>, Vec<&str>)> {
        backings.iter().map(outline_one).collect()
    }

    fn outline_one(b: &Backing) -> (Option<Backend>, Vec<&str>, Vec<&str>) {
        let claims = b.claims.iter().map(|c| c.name.as_str()).collect();
        let machines = b.machines.iter().map(String::as_str).collect();
        (b.backend.clone(), claims, machines)
    }

    fn nas() -> Option<Backend> {
        Some(Backend::Address("nas.example.com".into()))
    }

    fn csi() -> Option<Backend> {
        Some(Backend::Driver("csi.example.com".into()))
    }

    #[rstest]
    #[case::one_pod(
        vec![pod("web-1", "alpha", &["media", "database"])],
        vec![(nas(), vec!["media"], vec!["alpha"]), (csi(), vec!["database"], vec!["alpha"])]
    )]
    #[case::replicas_on_two_machines(
        vec![pod("web-1", "alpha", &["media"]), pod("web-2", "beta", &["media", "backups"])],
        vec![(nas(), vec!["media", "backups"], vec!["alpha", "beta"])]
    )]
    #[case::waiting_claims_wait_apart(
        vec![pod("web-1", "alpha", &["waiting", "media"])],
        vec![(nas(), vec!["media"], vec!["alpha"]), (None, vec!["waiting"], vec!["alpha"])]
    )]
    fn groups_claims_by_backend(
        #[case] pods: Vec<Pod>,
        #[case] expected: Vec<(Option<Backend>, Vec<&str>, Vec<&str>)>,
    ) {
        let pods: Vec<&Pod> = pods.iter().collect();
        let found = backings(&storage(), "shop", &pods, &[]);
        assert_eq!(outline(&found), expected);
    }

    /// A pod in shop named `name` on `machine` that names NFS volumes
    /// inline on `server`.
    fn inline(name: &str, machine: &str, server: &str, volumes: &[&str]) -> Pod {
        let volumes: Vec<String> = volumes
            .iter()
            .map(|v| format!("{{name: {v}, nfs: {{server: {server}, path: /{v}}}}}"))
            .collect();
        serde_yaml::from_str(&format!(
            "metadata: {{namespace: shop, name: {name}}}\nspec: {{nodeName: {machine}, containers: [], volumes: [{}]}}",
            volumes.join(", ")
        ))
        .unwrap()
    }

    fn inline_outline(backings: &[Backing]) -> Vec<(Option<Backend>, Vec<&str>, Vec<&str>)> {
        backings.iter().map(inline_one).collect()
    }

    fn inline_one(b: &Backing) -> (Option<Backend>, Vec<&str>, Vec<&str>) {
        let names = b.inline.iter().map(String::as_str).collect();
        let machines = b.machines.iter().map(String::as_str).collect();
        (b.backend.clone(), names, machines)
    }

    #[rstest]
    #[case::beside_its_claims(
        vec![pod("web-1", "alpha", &["media"]), inline("web-2", "beta", "nas.example.com", &["movies", "series"])],
        vec![(nas(), vec!["movies", "series"], vec!["alpha", "beta"])]
    )]
    #[case::on_a_server_of_its_own(
        vec![inline("web-1", "alpha", "files.example.com", &["movies"]), inline("web-2", "beta", "files.example.com", &["movies"])],
        vec![(Some(Backend::Address("files.example.com".into())), vec!["movies"], vec!["alpha", "beta"])]
    )]
    fn groups_inline_nfs_volumes_by_server(
        #[case] pods: Vec<Pod>,
        #[case] expected: Vec<(Option<Backend>, Vec<&str>, Vec<&str>)>,
    ) {
        let pods: Vec<&Pod> = pods.iter().collect();
        let found = backings(&storage(), "shop", &pods, &[]);
        assert_eq!(inline_outline(&found), expected);
    }

    #[rstest]
    fn keeps_each_claims_kind() {
        let pods = [pod("web-1", "alpha", &["media"])];
        let found = backings(&storage(), "shop", &[&pods[0]], &[]);
        assert_eq!(found[0].claims[0].kind, Some(Type::Nfs));
    }

    #[rstest]
    #[case::one_each(&["alpha", "beta"], vec![("alpha", 1), ("beta", 1)])]
    #[case::two_on_one(&["beta", "alpha", "beta"], vec![("alpha", 1), ("beta", 2)])]
    fn counts_pods_per_machine(#[case] machines: &[&str], #[case] expected: Vec<(&str, usize)>) {
        let pods: Vec<Pod> = machines
            .iter()
            .enumerate()
            .map(|(i, machine)| pod(&format!("web-{i}"), machine, &[]))
            .collect();
        let pods: Vec<&Pod> = pods.iter().collect();
        let found = hosts(&pods, &[]);
        let found: Vec<(&str, usize)> = found.iter().map(|h| (h.name.as_str(), h.pods)).collect();
        assert_eq!(found, expected);
    }
}
