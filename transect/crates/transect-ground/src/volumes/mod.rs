//! The content of the volume page, `/volumes/{pv}`, and of the claim
//! page: what the volume stores, how full it is, which pods mount it and
//! whether each one can write to it, which CronJobs mount it, and where
//! its data is stored.  The bytes used come from the `volumeUsed` query
//! that a `MetricsSource` declares, and every other value comes from the
//! Kubernetes API.

use std::sync::Arc;

use k8s_openapi::api::batch::v1::CronJob;
use k8s_openapi::api::core::v1::{Pod, PodSpec};

use crate::machines::Amount;
use transect_graph::edges::Claim;

const KI: f64 = 1024.0;

/// A pod that mounts a claim: its name, the machine it runs on (`None`
/// until the pod is scheduled), and whether any of its containers may
/// write to the claim.
#[derive(Debug, PartialEq)]
pub struct Mounter {
    pub pod: String,
    pub machine: Option<String>,
    pub writes: bool,
}

/// The pods in `claim`'s namespace that mount it and have not finished,
/// by name.
pub fn mounters(pods: &[Arc<Pod>], claim: &Claim) -> Vec<Mounter> {
    let mut found: Vec<Mounter> = pods
        .iter()
        .filter(|pod| pod.metadata.namespace.as_deref() == Some(&claim.namespace))
        .filter(|pod| !finished(pod))
        .filter_map(|pod| {
            let name = pod.metadata.name.as_deref()?;
            let spec = pod.spec.as_ref()?;
            let writes = writes(spec, name, claim)?;
            Some(Mounter {
                pod: name.into(),
                machine: spec.node_name.clone(),
                writes,
            })
        })
        .collect();
    found.sort_by(|a, b| a.pod.cmp(&b.pod));
    found
}

fn finished(pod: &Pod) -> bool {
    let phase = pod.status.as_ref().and_then(|s| s.phase.as_deref());
    matches!(phase, Some("Succeeded" | "Failed"))
}

/// Whether the pod named `pod` may write to `claim`, or `None` when the
/// pod does not mount the claim.  The pod may not write when its volume
/// source is read-only, or when no container mounts the volume
/// read-write.  Kubernetes names an ephemeral volume's claim
/// `<pod>-<volume>`.
fn writes(spec: &PodSpec, pod: &str, claim: &Claim) -> Option<bool> {
    let volume = spec.volumes.iter().flatten().find(|volume| {
        match (&volume.persistent_volume_claim, &volume.ephemeral) {
            (Some(source), _) => source.claim_name == claim.name,
            (None, Some(_)) => format!("{pod}-{}", volume.name) == claim.name,
            (None, None) => false,
        }
    })?;
    let read_only = volume
        .persistent_volume_claim
        .as_ref()
        .and_then(|source| source.read_only)
        .unwrap_or(false);
    let containers = spec
        .containers
        .iter()
        .chain(spec.init_containers.iter().flatten());
    let mounts = containers.flat_map(|container| container.volume_mounts.iter().flatten());
    let mut mounts = mounts.filter(|mount| mount.name == volume.name);
    Some(!read_only && mounts.any(|mount| mount.read_only != Some(true)))
}

/// The CronJobs in `claim`'s namespace whose pods mount it, by name.
pub fn cron_jobs(cron_jobs: &[Arc<CronJob>], claim: &Claim) -> Vec<String> {
    let mut found: Vec<String> = cron_jobs
        .iter()
        .filter(|job| job.metadata.namespace.as_deref() == Some(&claim.namespace))
        .filter(|job| {
            let template = job.spec.job_template.spec.as_ref();
            let volumes = template.and_then(|job| job.template.spec.as_ref()?.volumes.as_ref());
            volumes.into_iter().flatten().any(|volume| {
                let source = volume.persistent_volume_claim.as_ref();
                source.is_some_and(|source| source.claim_name == claim.name)
            })
        })
        .filter_map(|job| job.metadata.name.clone())
        .collect();
    found.sort();
    found
}

/// Bytes in binary units, with one decimal below 10: "512Mi", "3.2Gi",
/// "40Gi".
pub fn bytes(bytes: f64) -> String {
    let units = ["", "Ki", "Mi", "Gi", "Ti", "Pi"];
    let mut value = bytes.max(0.0);
    let mut unit = 0;
    while value >= KI && unit < units.len() - 1 {
        value /= KI;
        unit += 1;
    }
    match value < 10.0 && unit > 0 {
        true => format!("{value:.1}{}", units[unit]),
        false => format!("{value:.0}{}", units[unit]),
    }
}

/// How full a volume is, from its `capacity` quantity, such as "8Gi",
/// and the bytes `used`: the bytes used with their percentage of the
/// capacity, and the bytes free.  The bytes free are `None` when the
/// capacity does not parse, and when the bytes used exceed it, which
/// happens on a volume whose driver enforces no quota.
pub fn fullness(capacity: Option<&str>, used: f64) -> (String, Option<String>) {
    let parsed = capacity.and_then(Amount::parse).map(Amount::units);
    match (capacity, parsed.filter(|parsed| *parsed > 0.0)) {
        (Some(text), Some(parsed)) if used > parsed => {
            (format!("{}, over its {text} capacity", bytes(used)), None)
        }
        (_, Some(parsed)) => {
            let share = (used / parsed * 100.0).round();
            (
                format!("{} · {share}%", bytes(used)),
                Some(bytes(parsed - used)),
            )
        }
        _ => (bytes(used), None),
    }
}

#[cfg(test)]
mod tests;
