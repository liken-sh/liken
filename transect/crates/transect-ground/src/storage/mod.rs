//! Storage: each `PersistentVolumeClaim` followed to its
//! `PersistentVolume` and on to where the data is stored, which is an
//! NFS server, an iSCSI target, a CSI driver, or one machine's own disk.
//!
//! The outside-systems band draws the backends outside the cluster, one box per
//! address that a volume names, or one per CSI driver when the volume
//! names no address, with the box's volumes counted by type.  The
//! Kubernetes API does not show that several drivers and addresses
//! belong to one storage appliance, so they stay separate boxes unless
//! an `Outside` resource groups them.  A volume whose data is stored on
//! a machine's own disk is not in the outside-systems band.  That includes each
//! volume of a CSI driver whose `data-on` is `machines`.

mod console;
mod ground;
mod mounts;
mod pinned;
mod pod_mounts;
mod read;
mod sentence;
mod source;
mod stores_on;

use std::collections::{BTreeMap, HashMap};

use k8s_openapi::api::core::v1::{PersistentVolume, PersistentVolumeClaim};
use k8s_openapi::api::storage::v1::StorageClass;
use k8s_openapi::apimachinery::pkg::api::resource::Quantity;

use transect_graph::edges::{Claim, Object, Provenance};

/// The CSI drivers that store their volumes on the disks of the machines
/// that mount them, each with the provenance of the intent that declares
/// it.
pub type OnMachines = HashMap<String, Provenance>;

pub use console::consoles;
pub use ground::{Counts, Ground, under};
pub use mounts::{Mounts, mounts, pod_claims};
pub use pinned::Pin;
pub use pod_mounts::{PodMount, Source as MountSource, pod_mounts};
pub use sentence::{NOTHING_UNDER, volumes};
use source::source;
pub use stores_on::{inline_nfs, stores_on};

/// Where a volume stores its data.
#[derive(Clone, Debug, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub enum Backend {
    /// A server outside the cluster, by the address the volume names:
    /// an NFS server, or an iSCSI portal's host.
    Address(String),
    /// A CSI driver, or another provisioner, by name, for a volume that
    /// names no address.
    Driver(String),
    /// One machine's own disk, by its `Node`'s name, or `None` when the
    /// volume names no machine and its data is stored wherever its pod
    /// runs.
    Machine(Option<String>),
}

impl Backend {
    /// The name of the outside system that shows this backend, or `None` for
    /// a machine's disk, which is inside the cluster.
    pub fn outside(&self) -> Option<&str> {
        match self {
            Self::Address(name) | Self::Driver(name) => Some(name),
            Self::Machine(_) => None,
        }
    }
}

/// What an outside system's box counts its volumes by.
#[derive(Clone, Debug, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub enum Type {
    Nfs,
    Iscsi,
    /// A CSI or provisioned volume, by its `StorageClass`.
    Class(String),
    /// A CSI volume with no `StorageClass`, such as one created by hand.
    Unclassed,
    Local,
}

#[derive(Clone, Debug, PartialEq)]
pub struct Volume {
    pub backend: Backend,
    pub kind: Type,
    /// The provenance of the intent that declares where the volume stores
    /// its data, when the API alone does not show it.
    pub declared_in: Option<Provenance>,
}

#[derive(Debug, Default)]
pub struct Storage {
    /// Every `PersistentVolume` whose source transect reads, by name.
    volumes: HashMap<String, Volume>,
    /// Every claim, and the name of the volume bound to it, if any.
    claims: HashMap<Claim, Option<String>>,
    /// The size of each claim as its quantity is written, such as
    /// "8Gi".
    sizes: HashMap<Claim, String>,
    /// The access modes each claim asks for, such as `ReadWriteOnce`.
    modes: HashMap<Claim, Vec<String>>,
    /// The machines each volume is attached to, by the volume's name,
    /// from the cluster's `VolumeAttachment` objects.
    attachments: HashMap<String, Vec<String>>,
}

impl Storage {
    /// Follows every claim in `claims` to its volume.  A volume whose
    /// source transect does not read, and that has no `StorageClass` to
    /// name it by, is left out of every count.
    #[cfg(any(test, feature = "fixtures"))]
    pub fn build<'a>(
        claims: impl IntoIterator<Item = &'a PersistentVolumeClaim>,
        volumes: impl IntoIterator<Item = &'a PersistentVolume>,
        classes: impl IntoIterator<Item = &'a StorageClass>,
    ) -> Self {
        Self::placed(claims, volumes, classes, &OnMachines::new())
    }

    /// Follows every claim to its volume as `build` does.  A volume of
    /// a CSI driver that `on_machines` lists is placed on the machines
    /// that mount it.
    pub fn placed<'a>(
        claims: impl IntoIterator<Item = &'a PersistentVolumeClaim>,
        volumes: impl IntoIterator<Item = &'a PersistentVolume>,
        classes: impl IntoIterator<Item = &'a StorageClass>,
        on_machines: &OnMachines,
    ) -> Self {
        let provisioners: HashMap<&str, &str> = classes
            .into_iter()
            .filter_map(|class| Some((class.metadata.name.as_deref()?, class.provisioner.as_str())))
            .collect();
        let volumes = volumes
            .into_iter()
            .filter_map(|volume| {
                let name = volume.metadata.name.clone()?;
                let (backend, kind, declared_in) = source(volume, &provisioners, on_machines)?;
                let volume = Volume {
                    backend,
                    kind,
                    declared_in,
                };
                Some((name, volume))
            })
            .collect();
        let mut sizes = HashMap::new();
        let mut modes = HashMap::new();
        let claims = claims
            .into_iter()
            .filter_map(|claim| {
                let meta = &claim.metadata;
                let key = Claim::new(meta.namespace.as_deref()?, meta.name.as_deref()?);
                if let Some(size) = size(claim) {
                    sizes.insert(key.clone(), size);
                }
                let spec = claim.spec.as_ref();
                let asked = spec.and_then(|spec| spec.access_modes.clone());
                modes.insert(key.clone(), asked.unwrap_or_default());
                let bound = spec.and_then(|spec| spec.volume_name.clone());
                Some((key, bound.filter(|name| !name.is_empty())))
            })
            .collect();
        Self {
            volumes,
            claims,
            sizes,
            modes,
            attachments: HashMap::new(),
        }
    }

    /// This storage with the name of each backend outside the cluster
    /// passed through `rename`, so that the backends of one storage
    /// appliance share one outside system's box.
    pub fn renamed(mut self, rename: impl Fn(&str) -> String) -> Self {
        for volume in self.volumes.values_mut() {
            match &mut volume.backend {
                Backend::Address(name) | Backend::Driver(name) => *name = rename(name),
                Backend::Machine(_) => {}
            }
        }
        self
    }

    /// The size of `claim`, such as "8Gi".
    pub fn size_of(&self, claim: &Claim) -> Option<&str> {
        self.sizes.get(claim).map(String::as_str)
    }

    /// The volume bound to `claim`, or `None` when the claim is unbound,
    /// unknown, or bound to a volume that `Storage` left out.
    pub fn volume_of(&self, claim: &Claim) -> Option<&Volume> {
        self.volumes.get(self.claims.get(claim)?.as_deref()?)
    }

    /// The `PersistentVolume` named `name`, when transect reads its
    /// source.
    pub fn volume(&self, name: &str) -> Option<&Volume> {
        self.volumes.get(name)
    }

    /// Every volume of the `StorageClass` `class`.
    pub fn volumes_of_class(&self, class: &str) -> Vec<&Volume> {
        let of_class =
            |volume: &&Volume| matches!(&volume.kind, Type::Class(name) if name == class);
        self.volumes.values().filter(of_class).collect()
    }

    /// Every claim transect has read, as an object.
    pub fn claim_objects(&self) -> impl Iterator<Item = Object> + '_ {
        let claim =
            |c: &Claim| Object::new("", "PersistentVolumeClaim", Some(&c.namespace), &c.name);
        self.claims.keys().map(claim)
    }

    /// The number of claims in `namespace`, bound or waiting, as
    /// `kubectl get pvc` lists them.
    pub fn claims_in(&self, namespace: &str) -> usize {
        self.claims
            .keys()
            .filter(|claim| claim.namespace == namespace)
            .count()
    }

    /// The claims bound to a volume on the outside system `name`, each with
    /// its volume's type, sorted by type and then by namespace and name.
    pub fn claims_on(&self, name: &str) -> Vec<(&Claim, &Type)> {
        let mut found: Vec<(&Claim, &Type)> = self
            .claims
            .keys()
            .filter_map(|claim| {
                let volume = self.volume_of(claim)?;
                (volume.backend.outside() == Some(name)).then_some((claim, &volume.kind))
            })
            .collect();
        found.sort_by(|(a, a_kind), (b, b_kind)| a_kind.cmp(b_kind).then_with(|| a.cmp(b)));
        found
    }

    /// Each outside system's box and the number of its volumes of each type.
    /// Without `namespace`, the count includes every volume in the
    /// cluster.  With `namespace`, it includes only the volumes that the
    /// namespace's claims are bound to.
    pub fn tally(&self, namespace: Option<&str>) -> BTreeMap<&str, BTreeMap<&Type, usize>> {
        let volumes: Vec<&Volume> = match namespace {
            None => self.volumes.values().collect(),
            Some(namespace) => self
                .claims
                .keys()
                .filter(|claim| claim.namespace == namespace)
                .filter_map(|claim| self.volume_of(claim))
                .collect(),
        };
        let mut tally: BTreeMap<&str, BTreeMap<&Type, usize>> = BTreeMap::new();
        for volume in volumes {
            if let Some(name) = volume.backend.outside() {
                *tally
                    .entry(name)
                    .or_default()
                    .entry(&volume.kind)
                    .or_default() += 1;
            }
        }
        tally
    }
}

/// The size of `claim`: its capacity once bound, which can be more than
/// the request when a provisioner rounds up, and its requested storage
/// until then.
fn size(claim: &PersistentVolumeClaim) -> Option<String> {
    let capacity = claim.status.as_ref().and_then(|s| s.capacity.as_ref());
    let requests = claim.spec.as_ref().and_then(|spec| {
        let resources = spec.resources.as_ref()?;
        resources.requests.as_ref()
    });
    let storage = |quantities: &BTreeMap<String, Quantity>| quantities.get("storage").cloned();
    let quantity = capacity
        .and_then(storage)
        .or_else(|| requests.and_then(storage))?;
    Some(quantity.0)
}

#[cfg(any(test, feature = "fixtures"))]
pub mod fixtures {
    use super::*;

    fn yaml<K: serde::de::DeserializeOwned>(text: &str) -> K {
        serde_yaml::from_str(text).unwrap()
    }

    /// A `PersistentVolume` named `name` with `spec` as YAML.
    pub fn volume(name: &str, spec: &str) -> PersistentVolume {
        yaml(&format!("metadata: {{name: {name}}}\nspec: {{{spec}}}"))
    }

    pub fn nfs(name: &str, server: &str) -> PersistentVolume {
        volume(name, &format!("nfs: {{server: {server}, path: /{name}}}"))
    }

    pub fn csi(name: &str, driver: &str, class: &str) -> PersistentVolume {
        volume(
            name,
            &format!("storageClassName: {class}, csi: {{driver: {driver}, volumeHandle: {name}}}"),
        )
    }

    pub fn iscsi(name: &str) -> PersistentVolume {
        volume(
            name,
            "iscsi: {targetPortal: 'san.example.com:3260', iqn: iqn.example, lun: 0}",
        )
    }

    pub fn local(name: &str, machine: &str) -> PersistentVolume {
        volume(
            name,
            &format!(
                "local: {{path: /{name}}}, nodeAffinity: {{required: {{nodeSelectorTerms: [{{matchExpressions: [{{key: kubernetes.io/hostname, operator: In, values: [{machine}]}}]}}]}}}}"
            ),
        )
    }

    /// A claim in `namespace` that requests `request` and, once bound,
    /// has the capacity `capacity`, both given as YAML quantities.
    pub fn sized(
        namespace: &str,
        request: Option<&str>,
        capacity: Option<&str>,
    ) -> PersistentVolumeClaim {
        let request = request
            .map(|q| format!("resources: {{requests: {{storage: {q}}}}}"))
            .unwrap_or_default();
        let capacity = capacity
            .map(|q| format!("capacity: {{storage: {q}}}"))
            .unwrap_or_default();
        yaml(&format!(
            "metadata: {{namespace: {namespace}, name: sized}}\nspec: {{{request}}}\nstatus: {{{capacity}}}"
        ))
    }

    /// A claim in `namespace`, bound to the volume `bound` when there is
    /// one.
    pub fn claim(namespace: &str, name: &str, bound: Option<&str>) -> PersistentVolumeClaim {
        let spec = bound
            .map(|volume| format!("volumeName: {volume}"))
            .unwrap_or_default();
        yaml(&format!(
            "metadata: {{namespace: {namespace}, name: {name}}}\nspec: {{{spec}}}"
        ))
    }

    /// A cluster with two namespaces, a shop and a blog.  The shop has
    /// two volumes on an NFS server, one on a CSI driver, one on a
    /// machine, and one claim that still waits for a volume.  The blog
    /// has one volume on the CSI driver.  One more volume on the NFS
    /// server is bound to no claim.
    pub fn storage() -> Storage {
        let volumes = [
            nfs("pv-media", "nas.example.com"),
            nfs("pv-backups", "nas.example.com"),
            nfs("pv-released", "nas.example.com"),
            csi("pv-database", "csi.example.com", "fast"),
            csi("pv-posts", "csi.example.com", "fast"),
            local("pv-cache", "alpha"),
        ];
        let claims = [
            claim("shop", "media", Some("pv-media")),
            claim("shop", "backups", Some("pv-backups")),
            claim("shop", "database", Some("pv-database")),
            claim("shop", "cache", Some("pv-cache")),
            claim("shop", "waiting", None),
            claim("blog", "posts", Some("pv-posts")),
        ];
        Storage::build(&claims, &volumes, &[])
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::fixtures::storage;
    use super::*;

    fn tally(storage: &Storage, namespace: Option<&str>) -> Vec<(String, Vec<(Type, usize)>)> {
        storage
            .tally(namespace)
            .into_iter()
            .map(|(name, kinds)| {
                let kinds = kinds
                    .into_iter()
                    .map(|(kind, n)| (kind.clone(), n))
                    .collect();
                (name.to_string(), kinds)
            })
            .collect()
    }

    #[rstest]
    #[case::the_whole_cluster(None, vec![
        ("csi.example.com".into(), vec![(Type::Class("fast".into()), 2)]),
        ("nas.example.com".into(), vec![(Type::Nfs, 3)]),
    ])]
    #[case::one_namespace(Some("shop"), vec![
        ("csi.example.com".into(), vec![(Type::Class("fast".into()), 1)]),
        ("nas.example.com".into(), vec![(Type::Nfs, 2)]),
    ])]
    #[case::a_namespace_with_no_claims(Some("wiki"), vec![])]
    fn tallies_the_volumes_outside_the_cluster(
        #[case] namespace: Option<&str>,
        #[case] expected: Vec<(String, Vec<(Type, usize)>)>,
    ) {
        assert_eq!(tally(&storage(), namespace), expected);
    }

    #[rstest]
    #[case::bound_and_waiting("shop", 5)]
    #[case::one("blog", 1)]
    #[case::none("wiki", 0)]
    fn counts_every_claim_in_a_namespace(#[case] namespace: &str, #[case] claims: usize) {
        assert_eq!(storage().claims_in(namespace), claims);
    }

    #[rstest]
    #[case::its_capacity_once_bound(Some("5Gi"), Some("8Gi"), Some("8Gi"))]
    #[case::its_request_until_then(Some("5Gi"), None, Some("5Gi"))]
    #[case::neither(None, None, None)]
    fn reads_a_claims_size(
        #[case] request: Option<&str>,
        #[case] capacity: Option<&str>,
        #[case] size: Option<&str>,
    ) {
        let claims = [super::fixtures::sized("shop", request, capacity)];
        let storage = Storage::build(&claims, &[], &[]);
        assert_eq!(storage.size_of(&Claim::new("shop", "sized")), size);
    }

    #[rstest]
    #[case::bound(Claim::new("shop", "cache"), Some(Backend::Machine(Some("alpha".into()))))]
    #[case::waiting(Claim::new("shop", "waiting"), None)]
    #[case::no_such_claim(Claim::new("shop", "gone"), None)]
    fn follows_a_claim_to_its_volume(#[case] claim: Claim, #[case] backend: Option<Backend>) {
        let storage = storage();
        let found = storage
            .volume_of(&claim)
            .map(|volume| volume.backend.clone());
        assert_eq!(found, backend);
    }
}
