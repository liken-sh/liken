//! The machines that a pod's claims tie it to.  A lost machine's claims
//! stay tied to it until it returns.
//!
//! A volume on one machine's own disk exists on that machine only.  A
//! block volume that a CSI driver attached to a machine stays attached
//! until the machine confirms that its pod is gone.  A lost machine
//! never confirms, so a replacement pod on another machine waits for the
//! detach.  An in-tree iSCSI volume has no `VolumeAttachment`, so
//! transect uses the claim's access mode instead: a claim that only one
//! machine may mount is tied to the machine that its pod runs on.  A
//! claim that many machines may mount, such as an NFS share's, ties its
//! pod to no machine.

use k8s_openapi::api::core::v1::Pod;
use k8s_openapi::api::storage::v1::VolumeAttachment;

use super::mounts::pod_claims;
use super::{Backend, Storage, Type};
use transect_graph::edges::Claim;

/// The access modes that let only one machine mount a claim.
const ONE_MACHINE: [&str; 2] = ["ReadWriteOnce", "ReadWriteOncePod"];

/// What ties one of a pod's claims to a machine.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Pin {
    /// Its volume is on the machine's own disk.
    Disk { claim: Claim, machine: String },
    /// Its volume is attached to the machine, and is of `kind`.
    Attached {
        claim: Claim,
        kind: Type,
        machine: String,
    },
}

impl Pin {
    pub fn machine(&self) -> &str {
        match self {
            Self::Disk { machine, .. } | Self::Attached { machine, .. } => machine,
        }
    }

    pub fn claim(&self) -> &Claim {
        match self {
            Self::Disk { claim, .. } | Self::Attached { claim, .. } => claim,
        }
    }
}

impl Storage {
    /// Records the machine of each attachment.  An attachment with no
    /// `persistentVolumeName`, such as an inline volume's, is skipped,
    /// because it belongs to no claim.
    pub fn with_attachments<'a>(
        mut self,
        attachments: impl IntoIterator<Item = &'a VolumeAttachment>,
    ) -> Self {
        for attachment in attachments {
            let spec = &attachment.spec;
            let Some(volume) = spec.source.persistent_volume_name.clone() else {
                continue;
            };
            let machines = self.attachments.entry(volume).or_default();
            machines.push(spec.node_name.clone());
        }
        self
    }

    /// One `Pin` for each machine that one of `pod`'s claims ties it to,
    /// in the order that the pod's volumes list the claims.
    pub fn pins(&self, pod: &Pod) -> Vec<Pin> {
        let (Some(namespace), Some(name)) = (
            pod.metadata.namespace.as_deref(),
            pod.metadata.name.as_deref(),
        ) else {
            return Vec::new();
        };
        let runs_on = pod.spec.as_ref().and_then(|spec| spec.node_name.as_deref());
        let claims = pod_claims(pod, namespace, name);
        claims
            .into_iter()
            .flat_map(|claim| self.pins_of(claim, runs_on))
            .collect()
    }

    fn pins_of(&self, claim: Claim, runs_on: Option<&str>) -> Vec<Pin> {
        let Some(volume) = self.volume_of(&claim) else {
            return Vec::new();
        };
        if let Backend::Machine(Some(machine)) = &volume.backend {
            let machine = machine.clone();
            return vec![Pin::Disk { claim, machine }];
        }
        if !self.one_machine(&claim) {
            return Vec::new();
        }
        let kind = volume.kind.clone();
        let name = self.claims.get(&claim).cloned().flatten();
        let attached = name.and_then(|name| self.attachments.get(&name));
        let machines: Vec<&str> = match (attached, &kind, runs_on) {
            (Some(machines), _, _) => machines.iter().map(String::as_str).collect(),
            (None, Type::Iscsi, Some(machine)) => vec![machine],
            _ => Vec::new(),
        };
        machines
            .into_iter()
            .map(|machine| Pin::Attached {
                claim: claim.clone(),
                kind: kind.clone(),
                machine: machine.into(),
            })
            .collect()
    }

    /// Every claim whose volume is on `machine`'s own disk or attached
    /// to it, sorted by claim.  An attached volume counts whatever the
    /// claim's access mode, because it stays attached until the machine
    /// detaches it.
    pub fn on_machine(&self, machine: &str) -> Vec<Pin> {
        let mut pins: Vec<Pin> = self
            .claims
            .iter()
            .filter_map(|(claim, name)| {
                let name = name.as_deref()?;
                let volume = self.volumes.get(name)?;
                let claim = claim.clone();
                let machine = machine.to_string();
                if volume.backend == Backend::Machine(Some(machine.clone())) {
                    return Some(Pin::Disk { claim, machine });
                }
                let attached = self.attachments.get(name)?;
                attached.contains(&machine).then(|| Pin::Attached {
                    claim,
                    kind: volume.kind.clone(),
                    machine,
                })
            })
            .collect();
        pins.sort_by(|a, b| a.claim().cmp(b.claim()));
        pins
    }

    /// Whether every access mode that `claim` requests allows only one
    /// machine.  A claim that requests no mode returns false, because
    /// nothing then limits it to one machine.
    fn one_machine(&self, claim: &Claim) -> bool {
        let modes = self.modes.get(claim).map_or(&[][..], Vec::as_slice);
        !modes.is_empty() && modes.iter().all(|m| ONE_MACHINE.contains(&m.as_str()))
    }
}

#[cfg(test)]
mod tests {
    use k8s_openapi::api::core::v1::PersistentVolumeClaim;
    use rstest::rstest;

    use super::*;
    use crate::storage::fixtures::{csi, iscsi, local, nfs};

    /// A claim in shop, named for its volume and bound to it, that
    /// requests `mode`.
    fn claim(volume: &str, mode: &str) -> PersistentVolumeClaim {
        serde_yaml::from_str(&format!(
            "metadata: {{namespace: shop, name: {volume}}}\nspec: {{volumeName: {volume}, accessModes: [{mode}]}}"
        ))
        .unwrap()
    }

    fn attachment(volume: &str, machine: &str) -> VolumeAttachment {
        serde_yaml::from_str(&format!(
            "metadata: {{name: {volume}-{machine}}}\nspec: {{attacher: csi.example.com, nodeName: {machine}, source: {{persistentVolumeName: {volume}}}}}"
        ))
        .unwrap()
    }

    /// Storage with a volume for each case below, each bound to a claim
    /// of the volume's name.  The `block` and `shared` volumes are
    /// attached to alpha.
    fn storage() -> Storage {
        let volumes = [
            csi("block", "csi.example.com", "fast"),
            csi("unattached", "csi.example.com", "fast"),
            csi("shared", "csi.example.com", "files"),
            iscsi("lun"),
            iscsi("lun-shared"),
            nfs("media", "nas.example.com"),
            local("cache", "alpha"),
        ];
        let claims = [
            claim("block", "ReadWriteOnce"),
            claim("unattached", "ReadWriteOncePod"),
            claim("shared", "ReadWriteMany"),
            claim("lun", "ReadWriteOnce"),
            claim("lun-shared", "ReadOnlyMany"),
            claim("media", "ReadWriteMany"),
            claim("cache", "ReadWriteOnce"),
        ];
        let attachments = [attachment("block", "alpha"), attachment("shared", "alpha")];
        Storage::build(&claims, &volumes, &[]).with_attachments(&attachments)
    }

    /// A pod in shop on alpha that mounts the claim `claim`.
    fn pod(claim: &str) -> Pod {
        serde_yaml::from_str(&format!(
            "metadata: {{namespace: shop, name: web-0}}\nspec: {{nodeName: alpha, containers: [], volumes: [{{name: data, persistentVolumeClaim: {{claimName: {claim}}}}}]}}"
        ))
        .unwrap()
    }

    fn attached(claim: &str, kind: Type) -> Pin {
        Pin::Attached {
            claim: Claim::new("shop", claim),
            kind,
            machine: "alpha".into(),
        }
    }

    #[rstest]
    #[case::on_its_machines_disk("cache", vec![Pin::Disk { claim: Claim::new("shop", "cache"), machine: "alpha".into() }])]
    #[case::attached_block("block", vec![attached("block", Type::Class("fast".into()))])]
    #[case::block_attached_nowhere("unattached", vec![])]
    #[case::attached_but_shared("shared", vec![])]
    #[case::in_tree_iscsi_by_its_mode("lun", vec![attached("lun", Type::Iscsi)])]
    #[case::in_tree_iscsi_many_may_read("lun-shared", vec![])]
    #[case::nfs("media", vec![])]
    #[case::no_such_claim("gone", vec![])]
    fn ties_a_pod_to_the_machine_its_volume_needs(#[case] claim: &str, #[case] pins: Vec<Pin>) {
        assert_eq!(storage().pins(&pod(claim)), pins);
    }

    #[rstest]
    #[case::alpha("alpha", vec![
        attached("block", Type::Class("fast".into())),
        Pin::Disk { claim: Claim::new("shop", "cache"), machine: "alpha".into() },
        attached("shared", Type::Class("files".into())),
    ])]
    #[case::a_machine_with_none("beta", vec![])]
    fn lists_the_volumes_on_a_machine(#[case] machine: &str, #[case] pins: Vec<Pin>) {
        assert_eq!(storage().on_machine(machine), pins);
    }
}
