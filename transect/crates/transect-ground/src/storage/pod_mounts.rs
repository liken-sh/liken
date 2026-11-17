//! The mounts of one pod, for the pod page: each path a container
//! mounts, the volume behind it, and for a claim, its size, where its
//! data is stored, and which other pods mount it too.
//!
//! Everything here comes from the pod spec and the claims transect
//! already follows.  transect shows the name of a `Secret` or
//! `ConfigMap` volume and never reads its contents.

use k8s_openapi::api::core::v1::{Pod, Volume as PodVolume};

use super::mounts::pod_claims;
use super::{Claim, Storage, Volume};

#[derive(Debug, PartialEq)]
pub struct PodMount {
    pub path: String,
    /// The pod's volume, by the name its spec gives it.
    pub volume: String,
    pub source: Source,
    /// The containers that mount it, when not every container does.
    pub containers: Option<Vec<String>>,
    pub read_only: bool,
}

/// What a volume is.
#[derive(Debug, PartialEq)]
pub enum Source {
    Claim {
        name: String,
        size: Option<String>,
        /// Where the claim's data is stored, or `None` while the claim
        /// waits for a volume.
        volume: Option<Volume>,
        /// The other running pods in the namespace that mount it.
        shared_with: Vec<String>,
    },
    Secret(String),
    ConfigMap(String),
    EmptyDir {
        memory: bool,
    },
    HostPath(String),
    /// A projected volume, such as the service account token every pod
    /// mounts by default.
    Projected,
    /// Any other kind, by its field name in the spec, such as "csi" or
    /// "nfs".
    Other(&'static str),
}

/// The mounts of `pod`, in the order its containers list them.  `pods`
/// are the namespace's running pods, which this function searches for
/// the other pods that mount each claim.
pub fn pod_mounts(storage: &Storage, pod: &Pod, pods: &[&Pod]) -> Vec<PodMount> {
    let (Some(spec), Some(namespace), Some(name)) = (
        pod.spec.as_ref(),
        pod.metadata.namespace.as_deref(),
        pod.metadata.name.as_deref(),
    ) else {
        return Vec::new();
    };
    let containers: Vec<_> = spec
        .containers
        .iter()
        .chain(spec.init_containers.iter().flatten())
        .collect();
    let volumes = spec.volumes.as_deref().unwrap_or_default();
    let mut mounts: Vec<PodMount> = Vec::new();
    for container in &containers {
        for mount in container.volume_mounts.iter().flatten() {
            let found = mounts
                .iter_mut()
                .find(|m| m.path == mount.mount_path && m.volume == mount.name);
            if let Some(found) = found {
                found
                    .containers
                    .get_or_insert_default()
                    .push(container.name.clone());
                continue;
            }
            let Some(volume) = volumes.iter().find(|v| v.name == mount.name) else {
                continue;
            };
            mounts.push(PodMount {
                path: mount.mount_path.clone(),
                volume: mount.name.clone(),
                source: source(storage, volume, pod, namespace, name, pods),
                containers: Some(vec![container.name.clone()]),
                read_only: mount.read_only.unwrap_or(false),
            });
        }
    }
    for mount in &mut mounts {
        if mount
            .containers
            .as_ref()
            .is_some_and(|c| c.len() == containers.len())
        {
            mount.containers = None;
        }
    }
    mounts
}

fn source(
    storage: &Storage,
    volume: &PodVolume,
    pod: &Pod,
    namespace: &str,
    name: &str,
    pods: &[&Pod],
) -> Source {
    let claim = if let Some(claim) = &volume.persistent_volume_claim {
        Some(claim.claim_name.clone())
    } else {
        volume
            .ephemeral
            .as_ref()
            .map(|_| format!("{name}-{}", volume.name))
    };
    if let Some(claim) = claim {
        let claim = Claim::new(namespace, &claim);
        return Source::Claim {
            size: storage.size_of(&claim).map(Into::into),
            volume: storage.volume_of(&claim).cloned(),
            shared_with: sharing(&claim, pod, pods),
            name: claim.name,
        };
    }
    if let Some(secret) = &volume.secret {
        return Source::Secret(secret.secret_name.clone().unwrap_or_default());
    }
    if let Some(config_map) = &volume.config_map {
        return Source::ConfigMap(config_map.name.clone());
    }
    if let Some(empty) = &volume.empty_dir {
        let memory = empty.medium.as_deref() == Some("Memory");
        return Source::EmptyDir { memory };
    }
    if let Some(host) = &volume.host_path {
        return Source::HostPath(host.path.clone());
    }
    if volume.projected.is_some() {
        return Source::Projected;
    }
    Source::Other(other_kind(volume))
}

/// The other pods that mount `claim`, by name.
fn sharing(claim: &Claim, pod: &Pod, pods: &[&Pod]) -> Vec<String> {
    let mut names: Vec<String> = pods
        .iter()
        .filter(|other| other.metadata.name != pod.metadata.name)
        .filter_map(|other| {
            let name = other.metadata.name.as_deref()?;
            let claims = pod_claims(other, &claim.namespace, name);
            claims.contains(claim).then(|| name.to_owned())
        })
        .collect();
    names.sort();
    names
}

fn other_kind(volume: &PodVolume) -> &'static str {
    let kinds: [(&'static str, bool); 6] = [
        ("csi", volume.csi.is_some()),
        ("nfs", volume.nfs.is_some()),
        ("iscsi", volume.iscsi.is_some()),
        ("downwardAPI", volume.downward_api.is_some()),
        ("image", volume.image.is_some()),
        ("fc", volume.fc.is_some()),
    ];
    kinds
        .into_iter()
        .find_map(|(kind, set)| set.then_some(kind))
        .unwrap_or("volume")
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;
    use crate::storage::fixtures::storage;
    use crate::storage::{Backend, Type};

    fn pod(name: &str, spec: &str) -> Pod {
        serde_yaml::from_str(&format!(
            "metadata: {{namespace: shop, name: {name}}}\nspec: {spec}"
        ))
        .unwrap()
    }

    fn web(name: &str) -> Pod {
        pod(
            name,
            r#"{
  containers: [
    {name: app, volumeMounts: [
      {name: media, mountPath: /media},
      {name: config, mountPath: /etc/app, readOnly: true},
      {name: scratch, mountPath: /tmp}
    ]},
    {name: proxy, volumeMounts: [{name: scratch, mountPath: /tmp}, {name: token, mountPath: /run/token}]}
  ],
  volumes: [
    {name: media, persistentVolumeClaim: {claimName: media}},
    {name: config, secret: {secretName: app-config}},
    {name: scratch, emptyDir: {medium: Memory}},
    {name: token, projected: {sources: []}}
  ]
}"#,
        )
    }

    #[rstest]
    fn reads_each_mount_of_a_pod() {
        let (one, two, other) = (
            web("web-1"),
            web("web-2"),
            pod("cart-1", "{containers: []}"),
        );
        let pods = [&one, &two, &other];
        assert_eq!(
            pod_mounts(&storage(), &one, &pods),
            vec![
                PodMount {
                    path: "/media".into(),
                    volume: "media".into(),
                    source: Source::Claim {
                        name: "media".into(),
                        size: None,
                        volume: Some(Volume {
                            backend: Backend::Address("nas.example.com".into()),
                            kind: Type::Nfs,
                            stated: None,
                        }),
                        shared_with: vec!["web-2".into()],
                    },
                    containers: Some(vec!["app".into()]),
                    read_only: false,
                },
                PodMount {
                    path: "/etc/app".into(),
                    volume: "config".into(),
                    source: Source::Secret("app-config".into()),
                    containers: Some(vec!["app".into()]),
                    read_only: true,
                },
                PodMount {
                    path: "/tmp".into(),
                    volume: "scratch".into(),
                    source: Source::EmptyDir { memory: true },
                    containers: None,
                    read_only: false,
                },
                PodMount {
                    path: "/run/token".into(),
                    volume: "token".into(),
                    source: Source::Projected,
                    containers: Some(vec!["proxy".into()]),
                    read_only: false,
                },
            ]
        );
    }

    #[rstest]
    #[case::an_ephemeral_claim(
        "{name: data, ephemeral: {volumeClaimTemplate: {spec: {}}}}",
        Source::Claim { name: "web-1-data".into(), size: None, volume: None, shared_with: vec![] }
    )]
    #[case::a_config_map("{name: data, configMap: {name: settings}}", Source::ConfigMap("settings".into()))]
    #[case::a_host_path("{name: data, hostPath: {path: /var/run}}", Source::HostPath("/var/run".into()))]
    #[case::a_csi_volume("{name: data, csi: {driver: csi.example.com}}", Source::Other("csi"))]
    fn names_each_kind_of_volume(#[case] volume: &str, #[case] expected: Source) {
        let one = pod(
            "web-1",
            &format!(
                "{{containers: [{{name: app, volumeMounts: [{{name: data, mountPath: /data}}]}}], volumes: [{volume}]}}"
            ),
        );
        let mounts = pod_mounts(&storage(), &one, &[&one]);
        assert_eq!(
            mounts.into_iter().map(|m| m.source).collect::<Vec<_>>(),
            vec![expected]
        );
    }
}
