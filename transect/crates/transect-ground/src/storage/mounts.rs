//! The claims that one workload mounts, for the last column of the
//! workload page.  The column lists each claim with its size, grouped
//! under the backend that stores its data, so the column shows the path
//! from the pods down to the ground.

use k8s_openapi::api::core::v1::{Pod, PodSpec};

use super::{Storage, Volume};
use transect_graph::edges::Claim;

#[derive(Debug, PartialEq)]
pub struct Mount {
    pub claim: String,
    pub size: Option<String>,
}

/// The claims whose data is stored in one place.
#[derive(Debug, PartialEq)]
pub struct Group {
    /// Where the claims' data is stored, or `None` for claims that wait
    /// for a volume or that do not exist in the cluster.
    pub volume: Option<Volume>,
    pub mounts: Vec<Mount>,
}

/// Whether a workload's pods mount the same claims or each mount their
/// own.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Sharing {
    /// Every pod mounts every claim, as replicas on one NFS share do.
    Shared,
    /// Each claim belongs to one pod, as with the claims that a
    /// `StatefulSet`'s claim templates create.
    PerPod,
    /// One pod, or a mix of the two.
    Neither,
}

#[derive(Debug, PartialEq)]
pub struct Mounts {
    pub groups: Vec<Group>,
    pub sharing: Sharing,
}

impl Mounts {
    pub fn is_empty(&self) -> bool {
        self.groups.is_empty()
    }
}

/// The claims that `pods` mount in `namespace`, or, when there is no
/// pod, the claims that `templates` name.  A template's ephemeral volume
/// is left out, because its claim is named for a pod that does not exist
/// yet.
pub fn mounts(storage: &Storage, namespace: &str, pods: &[&Pod], templates: &[&PodSpec]) -> Mounts {
    let per_pod: Vec<Vec<Claim>> = if pods.is_empty() {
        let templates = templates
            .iter()
            .map(|spec| template_claims(spec, namespace));
        templates.collect()
    } else {
        pods.iter()
            .filter_map(|pod| Some(pod_claims(pod, namespace, pod.metadata.name.as_deref()?)))
            .collect()
    };
    let mut claims: Vec<(Claim, usize)> = Vec::new();
    for claim in per_pod.iter().flatten() {
        match claims.iter_mut().find(|(seen, _)| seen == claim) {
            Some((_, count)) => *count += 1,
            None => claims.push((claim.clone(), 1)),
        }
    }
    let sharing = match (pods.len(), claims.is_empty()) {
        (0 | 1, _) | (_, true) => Sharing::Neither,
        (pods, _) if claims.iter().all(|(_, count)| *count == pods) => Sharing::Shared,
        _ if claims.iter().all(|(_, count)| *count == 1) => Sharing::PerPod,
        _ => Sharing::Neither,
    };
    let mut groups: Vec<Group> = Vec::new();
    for (claim, _) in claims {
        let volume = storage.volume_of(&claim).cloned();
        let mount = Mount {
            size: storage.size_of(&claim).map(Into::into),
            claim: claim.name,
        };
        match groups.iter_mut().find(|group| group.volume == volume) {
            Some(group) => group.mounts.push(mount),
            None => groups.push(Group {
                volume,
                mounts: vec![mount],
            }),
        }
    }
    // Claims with no volume to store their data yet go last, below the
    // groups that have a volume.
    groups.sort_by_key(|group| group.volume.is_none());
    Mounts { groups, sharing }
}

/// The claims `pod` mounts.  An ephemeral volume's claim is named for
/// the pod and the volume, as Kubernetes' ephemeral volume controller
/// names it.
pub fn pod_claims(pod: &Pod, namespace: &str, name: &str) -> Vec<Claim> {
    let volumes = pod.spec.as_ref().and_then(|spec| spec.volumes.as_ref());
    volumes
        .into_iter()
        .flatten()
        .filter_map(|volume| {
            if let Some(claim) = &volume.persistent_volume_claim {
                Some(Claim::new(namespace, &claim.claim_name))
            } else {
                volume.ephemeral.as_ref()?;
                Some(Claim::new(namespace, &format!("{name}-{}", volume.name)))
            }
        })
        .collect()
}

fn template_claims(spec: &PodSpec, namespace: &str) -> Vec<Claim> {
    let volumes = spec.volumes.iter().flatten();
    volumes
        .filter_map(|volume| volume.persistent_volume_claim.as_ref())
        .map(|claim| Claim::new(namespace, &claim.claim_name))
        .collect()
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;
    use crate::storage::fixtures::storage;
    use crate::storage::{Backend, Type};

    /// A pod in shop named `name` that mounts each of `claims`.
    fn pod(name: &str, claims: &[&str]) -> Pod {
        let volumes: Vec<String> = claims
            .iter()
            .map(|claim| {
                format!("{{name: {claim}, persistentVolumeClaim: {{claimName: {claim}}}}}")
            })
            .collect();
        serde_yaml::from_str(&format!(
            "metadata: {{namespace: shop, name: {name}}}\nspec: {{containers: [], volumes: [{}]}}",
            volumes.join(", ")
        ))
        .unwrap()
    }

    fn template(claims: &[&str]) -> PodSpec {
        pod("template", claims).spec.unwrap()
    }

    fn nfs() -> Option<Volume> {
        Some(Volume {
            backend: Backend::Address("nas.example.com".into()),
            kind: Type::Nfs,
            stated: None,
        })
    }

    fn csi() -> Option<Volume> {
        Some(Volume {
            backend: Backend::Driver("csi.example.com".into()),
            kind: Type::Class("fast".into()),
            stated: None,
        })
    }

    /// Each group as its volume and its claims' names.
    fn grouped(mounts: &Mounts) -> Vec<(Option<Volume>, Vec<&str>)> {
        let groups = mounts.groups.iter();
        fn names(group: &Group) -> Vec<&str> {
            group.mounts.iter().map(|m| m.claim.as_str()).collect()
        }
        groups
            .map(|group| (group.volume.clone(), names(group)))
            .collect()
    }

    #[rstest]
    #[case::grouped_by_backend(
        vec![pod("web-1", &["media", "database", "backups"])],
        vec![(nfs(), vec!["media", "backups"]), (csi(), vec!["database"])]
    )]
    #[case::waiting_goes_last(
        vec![pod("web-1", &["waiting", "media"])],
        vec![(nfs(), vec!["media"]), (None, vec!["waiting"])]
    )]
    #[case::a_claim_the_cluster_lacks_waits(
        vec![pod("web-1", &["gone"])],
        vec![(None, vec!["gone"])]
    )]
    #[case::replicas_list_a_shared_claim_once(
        vec![pod("web-1", &["media"]), pod("web-2", &["media"])],
        vec![(nfs(), vec!["media"])]
    )]
    #[case::no_claims(vec![pod("web-1", &[])], vec![])]
    fn groups_a_workloads_claims(
        #[case] pods: Vec<Pod>,
        #[case] expected: Vec<(Option<Volume>, Vec<&str>)>,
    ) {
        let pods: Vec<&Pod> = pods.iter().collect();
        assert_eq!(grouped(&mounts(&storage(), "shop", &pods, &[])), expected);
    }

    #[rstest]
    fn reads_the_template_when_no_pod_runs() {
        let spec = template(&["media"]);
        let found = mounts(&storage(), "shop", &[], &[&spec]);
        assert_eq!(grouped(&found), vec![(nfs(), vec!["media"])]);
    }

    #[rstest]
    #[case::one_pod(vec![pod("web-1", &["media"])], Sharing::Neither)]
    #[case::shared(vec![pod("web-1", &["media"]), pod("web-2", &["media"])], Sharing::Shared)]
    #[case::one_per_pod(vec![pod("db-0", &["media"]), pod("db-1", &["backups"])], Sharing::PerPod)]
    #[case::mixed(
        vec![pod("db-0", &["media", "backups"]), pod("db-1", &["media", "database"])],
        Sharing::Neither
    )]
    #[case::no_claims(vec![pod("web-1", &[]), pod("web-2", &[])], Sharing::Neither)]
    fn tells_shared_claims_from_one_per_pod(#[case] pods: Vec<Pod>, #[case] sharing: Sharing) {
        let pods: Vec<&Pod> = pods.iter().collect();
        assert_eq!(mounts(&storage(), "shop", &pods, &[]).sharing, sharing);
    }
}
