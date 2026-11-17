//! The pod page's mounts: each of the pod's mounts from `storage/`, with
//! the other pods that share its claim named by their workloads, so a
//! claim three replicas share shows "also mounted by web ×2" instead of
//! three generated pod names.

use k8s_openapi::api::core::v1::Pod;

use transect_cluster::ownership::Ownership;
use transect_graph::edges::{Object, Plurals};
use transect_graph::links::Reference;
use transect_ground::storage::{MountSource, PodMount, Storage, pod_mounts};

#[derive(Debug, PartialEq)]
pub struct MountRow {
    pub mount: PodMount,
    /// A claim's name, linked to its page, when the mount is a claim.
    pub claim: Option<Reference>,
    /// "also mounted by web ×2, backup", when another pod mounts it.
    pub shared: Option<String>,
}

/// A row for each of `pod`'s mounts.  `pods` are the namespace's running
/// pods.
pub fn rows(
    storage: &Storage,
    pod: &Pod,
    pods: &[&Pod],
    (ownership, plurals): (&Ownership, &Plurals),
) -> Vec<MountRow> {
    let namespace = pod.metadata.namespace.as_deref();
    pod_mounts(storage, pod, pods)
        .into_iter()
        .map(|mount| {
            let others = match &mount.source {
                MountSource::Claim { shared_with, .. } => shared_with.as_slice(),
                _ => &[],
            };
            let owners = others.iter().map(|name| {
                let object = Object::new("", "Pod", namespace, name);
                ownership.top_owner(&object).name
            });
            let claim = mount.claim().map(|(name, _)| {
                let object = Object::new("", "PersistentVolumeClaim", namespace, name);
                Reference {
                    text: name.to_string(),
                    href: transect_graph::links::link(&object, plurals),
                }
            });
            MountRow {
                shared: shared(&counted(owners)),
                claim,
                mount,
            }
        })
        .collect()
}

/// Each name with how many times it occurs, in the order of each name's
/// first occurrence.
fn counted(names: impl Iterator<Item = String>) -> Vec<(String, usize)> {
    let mut counts: Vec<(String, usize)> = Vec::new();
    for name in names {
        match counts.iter_mut().find(|(seen, _)| *seen == name) {
            Some((_, count)) => *count += 1,
            None => counts.push((name, 1)),
        }
    }
    counts
}

fn shared(owners: &[(String, usize)]) -> Option<String> {
    if owners.is_empty() {
        return None;
    }
    let each = owners.iter().map(|(name, count)| match count {
        1 => name.clone(),
        count => format!("{name} ×{count}"),
    });
    Some(format!(
        "also mounted by {}",
        each.collect::<Vec<_>>().join(", ")
    ))
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;
    use transect_cluster::ownership::Entry;
    use transect_cluster::ownership::fixtures::meta;
    use transect_ground::storage::fixtures::storage;

    fn pod(name: &str, claim: &str) -> Pod {
        serde_yaml::from_str(&format!(
            "metadata: {{namespace: shop, name: {name}}}\nspec: {{containers: [{{name: app, volumeMounts: [{{name: data, mountPath: /data}}]}}], volumes: [{{name: data, persistentVolumeClaim: {{claimName: {claim}}}}}]}}"
        ))
        .unwrap()
    }

    #[rstest]
    fn names_the_other_pods_by_their_workloads() {
        let pods = [
            pod("web-1", "media"),
            pod("web-2", "media"),
            pod("web-3", "media"),
            pod("backup-1", "media"),
            pod("cart-1", "database"),
        ];
        let metas: Vec<_> = pods
            .iter()
            .map(|pod| {
                let name = pod.metadata.name.as_deref().unwrap();
                let owner = name.split('-').next().unwrap();
                meta(Some("shop"), name, &[("apps/v1", "Deployment", owner)])
            })
            .collect();
        let ownership = Ownership::build(metas.iter().map(|meta| Entry {
            group: "",
            kind: "Pod",
            meta,
        }));
        let running: Vec<&Pod> = pods.iter().collect();
        let plurals = Plurals::default();
        let found: Vec<(Option<String>, Option<Option<String>>)> =
            rows(&storage(), &pods[0], &running, (&ownership, &plurals))
                .into_iter()
                .map(|row| (row.shared, row.claim.map(|claim| claim.href)))
                .collect();
        assert_eq!(
            found,
            vec![(
                Some("also mounted by backup, web ×2".into()),
                Some(Some("/namespaces/shop/persistentvolumeclaims/media".into()))
            )]
        );
    }

    #[rstest]
    #[case::alone(vec![], None)]
    #[case::one(vec![("backup".into(), 1)], Some("also mounted by backup"))]
    fn the_shared_line(#[case] owners: Vec<(String, usize)>, #[case] line: Option<&str>) {
        assert_eq!(shared(&owners).as_deref(), line);
    }
}
