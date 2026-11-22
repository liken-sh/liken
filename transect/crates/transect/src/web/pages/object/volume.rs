//! The page of a `PersistentVolume`, at `/volumes/{pv}` and at the URL
//! of its `PersistentVolumeClaim`.  The board shows the volume's
//! capacity, usage, and access modes in the center, its claim on the
//! left, the pods and `CronJob`s that mount it, and on the right where
//! its data is stored.  A claim that is not bound to a volume gets the
//! same page, filled from the claim.

use k8s_openapi::api::core::v1::{PersistentVolume, PersistentVolumeClaim};
use k8s_openapi::apimachinery::pkg::apis::meta::v1::ObjectMeta;
use k8s_openapi::jiff::Timestamp;
use kube::ResourceExt;
use transect_resources::resources::OffLine;

use super::super::super::model::Model;
use super::super::super::snapshot::Snapshot;
use super::{card, refer_object};
use transect_graph::edges::Claim;
use transect_graph::edges::Object;
use transect_graph::links::Reference;
use transect_ground::storage::Backend;
use transect_ground::volumes::{cron_jobs, fullness, mounters};
use transect_map::objects::{self, ObjectBoard, ObjectPage, Section, Side, words};
use transect_observe::health::Level;
use transect_resources::layers::Layers;

/// What the URL of a volume page identifies.
#[derive(Clone, Copy)]
pub enum VolumeAt<'a> {
    /// A `PersistentVolume`, by name.
    Volume(&'a str),
    /// A claim, by namespace and name.
    Claim(&'a str, &'a str),
}

/// The page for `at`, or `None` when the cluster has no such volume or
/// claim.
pub fn volume_page(
    model: &Model,
    layers: &Layers,
    at: VolumeAt,
    now: Timestamp,
) -> Option<ObjectPage> {
    let snapshot = &model.snapshot;
    let (volume, claim) = find(snapshot, at)?;
    let claim_key = claim.and_then(|claim| {
        let meta = &claim.metadata;
        Some(Claim::new(
            meta.namespace.as_deref()?,
            meta.name.as_deref()?,
        ))
    });
    let object = match (volume, &claim_key) {
        (Some(volume), _) => Object::new("", "PersistentVolume", None, &volume.name_any()),
        (None, Some(claim)) => Object::new(
            "",
            "PersistentVolumeClaim",
            Some(&claim.namespace),
            &claim.name,
        ),
        (None, None) => return None,
    };
    let path = match at {
        VolumeAt::Volume(name) => transect_graph::links::volume(name),
        VolumeAt::Claim(..) => transect_graph::links::page(&object, &snapshot.plurals)?,
    };
    let namespace = claim_key.as_ref().map(|claim| claim.namespace.as_str());
    let place = match namespace {
        Some(namespace) => Some((namespace.to_string(), snapshot.layer_of(namespace, layers)?)),
        None => None,
    };
    let level = level(volume, claim);
    let note = match (&claim_key, volume) {
        (Some(claim), Some(_)) => snapshot
            .volumes_used
            .missing_line(claim, &snapshot.resources.directory),
        (_, None) => Some(OffLine::plain("This claim waits for a volume.")),
        (None, Some(_)) => Some(OffLine::plain("No claim is bound to this volume.")),
    };
    // A claim has a name that people chose, and a volume that a claim
    // binds has a generated one. So a page that has a claim is titled by
    // the claim, and the volume's name is a fact in its box.
    let (kind, name) = match &claim_key {
        Some(claim) => ("PersistentVolumeClaim", claim.name.clone()),
        None => (object.kind.as_str(), object.name.clone()),
    };
    let center = objects::Center {
        kind: kind.into(),
        name: name.clone(),
        level,
        resting: None,
        note,
        chips: Vec::new(),
        messages: Vec::new(),
    };
    let mut sections = vec![belongs_to(snapshot, &object, claim_key.as_ref(), namespace)];
    sections.push(volume_facts(
        snapshot,
        volume,
        claim,
        claim_key.as_ref(),
        now,
    ));
    if let Some(claim) = &claim_key {
        sections.extend(mounted(snapshot, claim));
    }
    if let Some(volume) = volume {
        sections.push(where_stored(snapshot, volume));
    }
    let graph = model.graph();
    // The card describes the claim, like the title, when there is one.
    let (titled, created) = match (&claim_key, claim) {
        (Some(key), Some(claim)) => (
            Object::new("", "PersistentVolumeClaim", Some(&key.namespace), &key.name),
            Some(&claim.metadata),
        ),
        _ => (object.clone(), volume.map(|volume| &volume.metadata)),
    };
    let card = card::card(snapshot, &titled, created, graph, level, false, now);
    Some(ObjectPage {
        kind: kind.into(),
        name,
        place,
        label: "Volumes".into(),
        kind_href: None,
        path,
        board: ObjectBoard::build(Some(center), sections),
        card,
    })
}

/// The volume and the claim for `at`. A volume URL finds the claim in
/// the volume's `claimRef`, and a claim URL finds the volume in the
/// claim's `volumeName`. Either can be `None`, but not both.
fn find<'a>(
    snapshot: &'a Snapshot,
    at: VolumeAt,
) -> Option<(
    Option<&'a PersistentVolume>,
    Option<&'a PersistentVolumeClaim>,
)> {
    let paged = &snapshot.paged;
    let volume_named = |name: &str| {
        let mut volumes = paged.volumes.iter();
        volumes
            .find(|volume| volume.name_any() == name)
            .map(AsRef::as_ref)
    };
    let claim_named = |namespace: &str, name: &str| {
        let mut claims = paged.claims.iter();
        let found = claims.find(|claim| {
            claim.metadata.namespace.as_deref() == Some(namespace) && claim.name_any() == name
        });
        found.map(AsRef::as_ref)
    };
    match at {
        VolumeAt::Volume(name) => {
            let volume = volume_named(name)?;
            let claim_ref = volume
                .spec
                .as_ref()
                .and_then(|spec| spec.claim_ref.as_ref());
            let claim = claim_ref.and_then(|claim_ref| {
                claim_named(claim_ref.namespace.as_deref()?, claim_ref.name.as_deref()?)
            });
            Some((Some(volume), claim))
        }
        VolumeAt::Claim(namespace, name) => {
            let claim = claim_named(namespace, name)?;
            let bound = claim
                .spec
                .as_ref()
                .and_then(|spec| spec.volume_name.as_deref());
            Some((bound.and_then(volume_named), Some(claim)))
        }
    }
}

fn level(
    volume: Option<&PersistentVolume>,
    claim: Option<&PersistentVolumeClaim>,
) -> Option<Level> {
    let volume_phase = volume.and_then(|v| v.status.as_ref()?.phase.as_deref());
    let claim_phase = claim.and_then(|c| c.status.as_ref()?.phase.as_deref());
    (volume_phase == Some("Failed") || claim_phase == Some("Lost")).then_some(Level::Failing)
}

/// The "Belongs to" section: the claim bound to the volume. It is empty
/// on the page of a claim that has no volume yet.
fn belongs_to(
    snapshot: &Snapshot,
    object: &Object,
    claim: Option<&Claim>,
    namespace: Option<&str>,
) -> Section {
    let claim = claim
        .filter(|_| object.kind == "PersistentVolume")
        .map(|claim| {
            let claim = Object::new(
                "",
                "PersistentVolumeClaim",
                Some(&claim.namespace),
                &claim.name,
            );
            refer_object(snapshot, &claim, namespace)
        });
    Section::new("Belongs to", Side::BelongsTo).fact("claimed by", claim.into_iter().collect())
}

/// The facts of the volume, or of the claim when it has no volume yet.
fn volume_facts(
    snapshot: &Snapshot,
    volume: Option<&PersistentVolume>,
    claim: Option<&PersistentVolumeClaim>,
    claim_key: Option<&Claim>,
    now: Timestamp,
) -> Section {
    let spec = volume.and_then(|volume| volume.spec.as_ref());
    let capacity = spec
        .and_then(|spec| spec.capacity.as_ref()?.get("storage"))
        .map(|quantity| quantity.0.clone())
        .or_else(|| claim_key.and_then(|claim| snapshot.storage.size_of(claim).map(Into::into)));
    let used = claim_key.and_then(|claim| snapshot.volumes_used.used(claim));
    let (used, free) = match used {
        Some(used) => {
            let (used, free) = fullness(capacity.as_deref(), used);
            (Some(used), free)
        }
        None => (None, None),
    };
    let modes = spec
        .and_then(|spec| spec.access_modes.clone())
        .or_else(|| claim?.spec.as_ref()?.access_modes.clone())
        .unwrap_or_default();
    let phase = volume
        .and_then(|volume| volume.status.as_ref()?.phase.clone())
        .or_else(|| claim?.status.as_ref()?.phase.clone());
    let meta: Option<&ObjectMeta> = volume
        .map(|volume| &volume.metadata)
        .or(claim.map(|claim| &claim.metadata));
    let age = meta
        .and_then(|meta| meta.creation_timestamp.as_ref())
        .map(|created| {
            let minutes = now.duration_since(created.0).as_secs() / 60;
            transect_resources::knowledge::age(minutes)
        });
    let named = volume
        .filter(|_| claim.is_some())
        .map(|volume| volume.name_any());
    Section::new("Volume", Side::Own)
        .literals("PersistentVolume", named.into_iter().collect())
        .text("capacity", capacity)
        .text("used", used)
        .text("free", free)
        .literals("access modes", modes)
        .text(
            "reclaim policy",
            spec.and_then(|spec| spec.persistent_volume_reclaim_policy.clone()),
        )
        .text("phase", phase)
        .text("age", age)
}

/// The pods that mount `claim`, split by whether they write, each with
/// the machine it runs on, and the `CronJob`s whose pods would mount it.
fn mounted(snapshot: &Snapshot, claim: &Claim) -> Vec<Section> {
    let here = Some(claim.namespace.as_str());
    let (mut writers, mut readers) = (Vec::new(), Vec::new());
    for mounter in mounters(&snapshot.pods, claim) {
        let pod = Object::new("", "Pod", here, &mounter.pod);
        let machine = match &mounter.machine {
            Some(machine) => machine_link(machine),
            None => words("no machine yet"),
        };
        let pair = (refer_object(snapshot, &pod, here), machine);
        match mounter.writes {
            true => writers.push(pair),
            false => readers.push(pair),
        }
    }
    let jobs = cron_jobs(&snapshot.cron_jobs, claim)
        .into_iter()
        .map(|name| {
            let job = Object::new("batch", "CronJob", here, &name);
            refer_object(snapshot, &job, here)
        });
    vec![
        Section::new("Mounted by", Side::Into)
            .placed("writes", writers)
            .placed("reads only", readers)
            .fact("CronJobs, when they run", jobs.collect())
            .or("No running pod mounts it."),
    ]
}

fn machine_link(machine: &str) -> Reference {
    Reference {
        text: machine.into(),
        href: Some(transect_graph::links::machine(machine)),
    }
}

/// Where the volume's data is stored: its `StorageClass`, the CSI driver
/// or provisioner behind it, the server or machine that holds the data,
/// and the path on it.
fn where_stored(snapshot: &Snapshot, volume: &PersistentVolume) -> Section {
    let spec = volume.spec.as_ref();
    let class = spec
        .and_then(|spec| spec.storage_class_name.clone())
        .filter(|class| !class.is_empty());
    let provisioner = class.as_deref().and_then(|class| {
        let classes = snapshot.paged.storage_classes.iter();
        let found = classes
            .into_iter()
            .find(|found| found.name_any() == class)?;
        Some(found.provisioner.clone())
    });
    let driver = spec
        .and_then(|spec| Some(spec.csi.as_ref()?.driver.clone()))
        .or(provisioner);
    let class = class.map(|class| {
        let class = Object::new("storage.k8s.io", "StorageClass", None, &class);
        refer_object(snapshot, &class, None)
    });
    let driver = driver.map(|driver| {
        let csi = Object::new("storage.k8s.io", "CSIDriver", None, &driver);
        match snapshot.pages().has(&csi) {
            true => refer_object(snapshot, &csi, None),
            false => words(driver),
        }
    });
    let on = snapshot
        .storage
        .volume(&volume.name_any())
        .map(|placed| match &placed.backend {
            Backend::Address(name) | Backend::Driver(name) => Reference {
                text: name.clone(),
                href: Some(snapshot.outsides.page_of(name)),
            },
            Backend::Machine(Some(machine)) => machine_link(machine),
            Backend::Machine(None) => words("the machine that mounts it"),
        });
    let path = spec.and_then(|spec| {
        let nfs = spec
            .nfs
            .as_ref()
            .map(|nfs| format!("{}:{}", nfs.server, nfs.path));
        let local = spec.local.as_ref().map(|local| local.path.clone());
        let host = spec.host_path.as_ref().map(|host| host.path.clone());
        nfs.or(local).or(host)
    });
    Section::new("Where its bytes are", Side::Out)
        .fact("StorageClass", class.into_iter().collect())
        .fact("provisioned by", driver.into_iter().collect())
        .fact("on", on.into_iter().collect())
        .literals("at", path.into_iter().collect())
        .or("The API does not show where this volume keeps its data.")
}
