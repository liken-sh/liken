//! Where one `PersistentVolume` stores its data, read from the volume
//! source in its spec.  For a CSI driver whose `data-on` is `machines`,
//! the machine comes from the volume's node affinity.

use std::collections::HashMap;

use k8s_openapi::api::core::v1::{PersistentVolume, PersistentVolumeSpec, VolumeNodeAffinity};

use super::{Backend, OnMachines, Type};
use transect_graph::edges::Provenance;

/// The node label that a local volume's node affinity uses to name its
/// machine.
const HOSTNAME: &str = "kubernetes.io/hostname";

/// Where a volume stores its data, its type, and, when the location
/// comes from declared intent and not from the volume, the provenance of
/// that intent.
pub type Placed = (Backend, Type, Option<Provenance>);

/// The backend and type of `volume`, or `None` when transect does not
/// read its source and no `StorageClass` gives its provisioner.
/// `provisioners` maps each `StorageClass` name to its provisioner, and
/// `on_machines` lists the CSI drivers that store each volume on the
/// machine that mounts it.
pub fn source(
    volume: &PersistentVolume,
    provisioners: &HashMap<&str, &str>,
    on_machines: &OnMachines,
) -> Option<Placed> {
    let (backend, kind) = read(volume, provisioners)?;
    let Backend::Driver(driver) = &backend else {
        return Some((backend, kind, None));
    };
    let spec = volume.spec.as_ref()?;
    match on_machines.get(driver) {
        Some(stated) => Some((Backend::Machine(machine(spec)), kind, Some(stated.clone()))),
        None => Some((backend, kind, None)),
    }
}

fn read(volume: &PersistentVolume, provisioners: &HashMap<&str, &str>) -> Option<(Backend, Type)> {
    let spec = volume.spec.as_ref()?;
    let class = spec.storage_class_name.as_deref().filter(|c| !c.is_empty());
    if let Some(nfs) = &spec.nfs {
        return Some((Backend::Address(nfs.server.clone()), Type::Nfs));
    }
    if let Some(iscsi) = &spec.iscsi {
        let host = host_of(&iscsi.target_portal);
        return Some((Backend::Address(host.into()), Type::Iscsi));
    }
    // Only the driver defines a CSI volume's attributes, so the API does
    // not show which server the driver connects to, if any.  The backend
    // is the driver, and the type comes from the volume's
    // `StorageClass`.
    if let Some(csi) = &spec.csi {
        return Some((Backend::Driver(csi.driver.clone()), of_class(class)));
    }
    if spec.local.is_some() || spec.host_path.is_some() {
        return Some((Backend::Machine(machine(spec)), Type::Local));
    }
    // For any other source, such as a cloud provider's disk, the backend
    // is the provisioner of the volume's `StorageClass`, which is the
    // closest equivalent of a CSI driver.
    let provisioner = provisioners.get(class?)?;
    Some((Backend::Driver((*provisioner).into()), of_class(class)))
}

fn of_class(class: Option<&str>) -> Type {
    class.map_or(Type::Unclassed, |class| Type::Class(class.into()))
}

/// The host of an iSCSI portal, which is `host`, `host:port`, or
/// `[v6]:port`.  The port is dropped so that a storage appliance that
/// serves NFS and iSCSI from one address is one backend.
fn host_of(portal: &str) -> &str {
    if let Some(rest) = portal.strip_prefix('[') {
        return rest.split(']').next().unwrap_or(rest);
    }
    match portal.rsplit_once(':') {
        Some((host, _)) if !host.contains(':') => host,
        _ => portal,
    }
}

/// The machine that a volume's node affinity restricts it to, which is
/// the one hostname in its required terms.  Returns `None` when there
/// is no such single hostname.  A `hostPath` volume often has no
/// affinity, and then its data is on whichever machine its pod runs on.
fn machine(spec: &PersistentVolumeSpec) -> Option<String> {
    let VolumeNodeAffinity { required } = spec.node_affinity.as_ref()?;
    let mut hosts = required
        .as_ref()?
        .node_selector_terms
        .iter()
        .flat_map(|term| term.match_expressions.iter().flatten())
        .filter(|expression| expression.key == HOSTNAME && expression.operator == "In")
        .flat_map(|expression| expression.values.iter().flatten());
    let host = hosts.next()?;
    hosts.next().is_none().then(|| host.clone())
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;
    use crate::storage::fixtures::volume;

    #[rstest]
    #[case::nfs(
        "storageClassName: media, nfs: {server: nas.example.com, path: /media}",
        Some((Backend::Address("nas.example.com".into()), Type::Nfs))
    )]
    #[case::iscsi_with_a_port(
        "iscsi: {targetPortal: '10.0.0.5:3260', iqn: iqn.example, lun: 0}",
        Some((Backend::Address("10.0.0.5".into()), Type::Iscsi))
    )]
    #[case::iscsi_without_a_port(
        "iscsi: {targetPortal: san.example.com, iqn: iqn.example, lun: 0}",
        Some((Backend::Address("san.example.com".into()), Type::Iscsi))
    )]
    #[case::iscsi_on_ipv6(
        "iscsi: {targetPortal: '[fd00::5]:3260', iqn: iqn.example, lun: 0}",
        Some((Backend::Address("fd00::5".into()), Type::Iscsi))
    )]
    #[case::csi_with_a_class(
        "storageClassName: fast, csi: {driver: csi.example.com, volumeHandle: a}",
        Some((Backend::Driver("csi.example.com".into()), Type::Class("fast".into())))
    )]
    #[case::csi_without_a_class(
        "csi: {driver: git.example.com, volumeHandle: a}",
        Some((Backend::Driver("git.example.com".into()), Type::Unclassed))
    )]
    #[case::local_on_one_machine(
        "local: {path: /data}, nodeAffinity: {required: {nodeSelectorTerms: [{matchExpressions: [{key: kubernetes.io/hostname, operator: In, values: [alpha]}]}]}}",
        Some((Backend::Machine(Some("alpha".into())), Type::Local))
    )]
    #[case::local_on_either_of_two(
        "local: {path: /data}, nodeAffinity: {required: {nodeSelectorTerms: [{matchExpressions: [{key: kubernetes.io/hostname, operator: In, values: [alpha, beta]}]}]}}",
        Some((Backend::Machine(None), Type::Local))
    )]
    #[case::host_path_anywhere(
        "hostPath: {path: /data}",
        Some((Backend::Machine(None), Type::Local))
    )]
    #[case::another_source_by_its_class(
        "storageClassName: disk, gcePersistentDisk: {pdName: a}",
        Some((Backend::Driver("disks.example.com".into()), Type::Class("disk".into())))
    )]
    #[case::another_source_with_no_class("gcePersistentDisk: {pdName: a}", None)]
    fn reads_where_a_volume_keeps_its_data(
        #[case] spec: &str,
        #[case] expected: Option<(Backend, Type)>,
    ) {
        let provisioners = HashMap::from([("disk", "disks.example.com")]);
        assert_eq!(read(&volume("pv-1", spec), &provisioners), expected);
    }

    fn stated() -> Provenance {
        Provenance::Intent(transect_graph::edges::Stated::Annotation {
            field: "data-on".into(),
            on: "CSIDriver disk.example.com".into(),
        })
    }

    /// A driver that stores its data on the machines that mount its
    /// volumes stores a volume on the one machine in the volume's
    /// affinity, or on whichever machine runs its pod.
    #[rstest]
    #[case::pinned_to_one_machine(
        "storageClassName: node, csi: {driver: disk.example.com, volumeHandle: a}, nodeAffinity: {required: {nodeSelectorTerms: [{matchExpressions: [{key: kubernetes.io/hostname, operator: In, values: [alpha]}]}]}}",
        Some((Backend::Machine(Some("alpha".into())), Type::Class("node".into()), Some(stated())))
    )]
    #[case::wherever_its_pod_runs(
        "storageClassName: node, csi: {driver: disk.example.com, volumeHandle: a}",
        Some((Backend::Machine(None), Type::Class("node".into()), Some(stated())))
    )]
    #[case::another_driver(
        "storageClassName: fast, csi: {driver: csi.example.com, volumeHandle: a}",
        Some((Backend::Driver("csi.example.com".into()), Type::Class("fast".into()), None))
    )]
    #[case::not_a_driver(
        "nfs: {server: nas.example.com, path: /media}",
        Some((Backend::Address("nas.example.com".into()), Type::Nfs, None))
    )]
    fn keeps_a_drivers_data_on_machines_when_intent_says_so(
        #[case] spec: &str,
        #[case] expected: Option<Placed>,
    ) {
        let on_machines = OnMachines::from([("disk.example.com".to_string(), stated())]);
        let found = source(&volume("pv-1", spec), &HashMap::new(), &on_machines);
        assert_eq!(found, expected);
    }
}
