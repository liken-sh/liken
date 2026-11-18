//! The text of the outside-systems band and the volume lists.  The words are
//! fixed here, and the addresses, drivers, classes, names, and numbers
//! come from the cluster.

use std::fmt;

use super::ground::{Counts, Ground, GroundBox, Totals};
use super::mounts::{Group, Mounts, Sharing};
use super::pod_mounts::{PodMount, Source};
use super::{Backend, Type, Volume};

impl fmt::Display for Counts {
    /// "6 NFS · 2 iSCSI".  Volumes with no `StorageClass` show as
    /// "6 volumes".
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        for (index, (kind, count)) in self.0.iter().enumerate() {
            if index > 0 {
                write!(f, " · ")?;
            }
            match kind {
                Type::Nfs => write!(f, "{count} NFS")?,
                Type::Iscsi => write!(f, "{count} iSCSI")?,
                Type::Class(class) => write!(f, "{count} {class}")?,
                Type::Local => write!(f, "{count} local")?,
                Type::Unclassed => write!(f, "{}", volumes(*count))?,
            }
        }
        Ok(())
    }
}

impl Type {
    /// The type's name as the page shows it above a list of volumes:
    /// "NFS", "iSCSI", a `StorageClass` name such as "fast", "local", or
    /// "no StorageClass".
    pub fn label(&self) -> String {
        match self {
            Type::Nfs => "NFS".into(),
            Type::Iscsi => "iSCSI".into(),
            Type::Class(class) => class.clone(),
            Type::Local => "local".into(),
            Type::Unclassed => "no StorageClass".into(),
        }
    }
}

impl fmt::Display for Totals {
    /// "6 NFS · 2 iSCSI", "3 depend on it", or both: "6 NFS · 1
    /// depends on it".
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "{}", self.counts)?;
        if self.dependents > 0 && !self.counts.is_empty() {
            write!(f, " · ")?;
        }
        match self.dependents {
            0 => Ok(()),
            1 => write!(f, "1 depends on it"),
            count => write!(f, "{count} depend on it"),
        }
    }
}

impl GroundBox {
    /// The counts that the page shows after the box's name: the whole
    /// cluster's, or the selected namespace's after its name.  `None`
    /// when the selected namespace has no volumes or dependents on the
    /// box, so the page shows the name alone.
    pub fn text(&self, ground: &Ground) -> Option<String> {
        match (&self.here, &ground.selected) {
            (None, _) => Some(self.all.to_string()),
            (Some(here), _) if here.is_empty() => None,
            (Some(here), Some(namespace)) => Some(format!("{namespace}: {here}")),
            (Some(here), None) => Some(here.to_string()),
        }
    }
}

impl GroundBox {
    /// The box's intent lines joined with spaces, or `None` when it has
    /// none.
    pub fn about_title(&self) -> Option<String> {
        let lines: Vec<&str> = self.about.iter().map(|line| line.text.as_str()).collect();
        (!lines.is_empty()).then(|| lines.join(" "))
    }
}

impl Ground {
    /// The count under the band's name, such as "12 volumes · 3
    /// dependencies", where a dependency is a box that at least one
    /// object depends on.
    pub fn total_text(&self) -> String {
        match self.depended_on() {
            0 => volumes(self.total()),
            1 => format!("{} · 1 dependency", volumes(self.total())),
            count => format!("{} · {count} dependencies", volumes(self.total())),
        }
    }
}

/// The ground line of a namespace with no volume outside the cluster.
pub const NOTHING_UNDER: &str = "no volumes outside the cluster";

/// "1 volume", "12 volumes".
pub fn volumes(count: usize) -> String {
    match count {
        1 => "1 volume".into(),
        count => format!("{count} volumes"),
    }
}

impl Mounts {
    /// The volumes column's heading: "Volumes · shared" when every pod
    /// mounts every claim, "Volumes · one per pod" when each claim
    /// belongs to one pod, and "Volumes" for one pod or a mix.
    pub fn heading(&self) -> &'static str {
        match self.sharing {
            Sharing::Shared => "Volumes · shared",
            Sharing::PerPod => "Volumes · one per pod",
            Sharing::Neither => "Volumes",
        }
    }
}

impl Group {
    /// Where the group's data is stored: "nas.example.com · NFS",
    /// "csi.example.com · fast", "alpha · local".
    pub fn backend(&self) -> String {
        backend(self.volume.as_ref())
    }
}

fn backend(volume: Option<&Volume>) -> String {
    let Some(volume) = volume else {
        return "waiting for a volume".into();
    };
    let place = match &volume.backend {
        Backend::Address(name) | Backend::Driver(name) | Backend::Machine(Some(name)) => {
            name.as_str()
        }
        Backend::Machine(None) => "its pod's machine",
    };
    match &volume.kind {
        Type::Nfs => format!("{place} · NFS"),
        Type::Iscsi => format!("{place} · iSCSI"),
        Type::Class(class) => format!("{place} · {class}"),
        Type::Local => format!("{place} · local"),
        Type::Unclassed => place.into(),
    }
}

impl PodMount {
    /// The claim's name and the text for where its data is stored, as
    /// separate values so that a page can link the name.  Returns `None`
    /// when the mount is not a claim.
    pub fn claim(&self) -> Option<(&str, String)> {
        match &self.source {
            Source::Claim { name, volume, .. } => Some((name, backend(volume.as_ref()))),
            _ => None,
        }
    }

    /// What is mounted at the path, such as "Secret app-config".
    pub fn what(&self) -> String {
        match &self.source {
            Source::Claim { name, volume, .. } => format!("{name} · {}", backend(volume.as_ref())),
            Source::Secret(name) => format!("Secret {name}"),
            Source::ConfigMap(name) => format!("ConfigMap {name}"),
            Source::EmptyDir { memory: true } => "emptyDir in memory".into(),
            Source::EmptyDir { memory: false } => "emptyDir".into(),
            Source::HostPath(path) => format!("hostPath {path}"),
            Source::Projected => format!("projected {}", self.volume),
            Source::Other(kind) => format!("{kind} {}", self.volume),
        }
    }

    /// The line of detail that the page shows under the mount, when
    /// there is one: the claim's size, "read-only", and the containers
    /// that mount it, as in "8Gi · read-only · in app, proxy".
    pub fn details(&self) -> Option<String> {
        let size = match &self.source {
            Source::Claim { size, .. } => size.clone(),
            _ => None,
        };
        let read_only = self.read_only.then(|| "read-only".to_string());
        let containers = self
            .containers
            .as_ref()
            .map(|c| format!("in {}", c.join(", ")));
        let parts: Vec<String> = size
            .into_iter()
            .chain(read_only)
            .chain(containers)
            .collect();
        (!parts.is_empty()).then(|| parts.join(" · "))
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn group(backend: Backend, kind: Type) -> Group {
        Group {
            volume: Some(Volume {
                backend,
                kind,
                declared_in: None,
            }),
            mounts: vec![],
        }
    }

    #[rstest]
    #[case::nfs(group(Backend::Address("nas.example.com".into()), Type::Nfs), "nas.example.com · NFS")]
    #[case::iscsi(group(Backend::Address("san.example.com".into()), Type::Iscsi), "san.example.com · iSCSI")]
    #[case::a_class(group(Backend::Driver("csi.example.com".into()), Type::Class("fast".into())), "csi.example.com · fast")]
    #[case::unclassed(group(Backend::Driver("csi.example.com".into()), Type::Unclassed), "csi.example.com")]
    #[case::a_machine(group(Backend::Machine(Some("alpha".into())), Type::Local), "alpha · local")]
    #[case::the_pods_machine(
        group(Backend::Machine(None), Type::Local),
        "its pod's machine · local"
    )]
    #[case::waiting(Group { volume: None, mounts: vec![] }, "waiting for a volume")]
    fn a_group_names_its_backend(#[case] group: Group, #[case] text: &str) {
        assert_eq!(group.backend(), text);
    }

    fn mount(source: Source, containers: Option<&[&str]>, read_only: bool) -> PodMount {
        PodMount {
            path: "/data".into(),
            volume: "data".into(),
            source,
            containers: containers.map(|c| c.iter().map(|n| n.to_string()).collect()),
            read_only,
        }
    }

    fn claim(size: Option<&str>) -> Source {
        Source::Claim {
            name: "media".into(),
            size: size.map(Into::into),
            volume: Some(Volume {
                backend: Backend::Address("nas.example.com".into()),
                kind: Type::Nfs,
                declared_in: None,
            }),
            shared_with: vec![],
        }
    }

    #[rstest]
    #[case::a_claim(
        mount(claim(Some("8Gi")), None, false),
        "media · nas.example.com · NFS",
        Some("8Gi")
    )]
    #[case::a_secret_in_one_container(
        mount(Source::Secret("app-config".into()), Some(&["app"]), true),
        "Secret app-config",
        Some("read-only · in app")
    )]
    #[case::a_config_map(mount(Source::ConfigMap("settings".into()), None, false), "ConfigMap settings", None)]
    #[case::memory(mount(Source::EmptyDir { memory: true }, Some(&["app", "proxy"]), false), "emptyDir in memory", Some("in app, proxy"))]
    #[case::disk(mount(Source::EmptyDir { memory: false }, None, false), "emptyDir", None)]
    #[case::a_host_path(mount(Source::HostPath("/var/run".into()), None, false), "hostPath /var/run", None)]
    #[case::projected(
        mount(Source::Projected, None, true),
        "projected data",
        Some("read-only")
    )]
    #[case::other(mount(Source::Other("csi"), None, false), "csi data", None)]
    fn a_pod_mount_names_what_it_mounts_and_its_details(
        #[case] mount: PodMount,
        #[case] what: &str,
        #[case] details: Option<&str>,
    ) {
        assert_eq!(
            (mount.what().as_str(), mount.details().as_deref()),
            (what, details)
        );
    }

    #[rstest]
    #[case::shared(Sharing::Shared, "Volumes · shared")]
    #[case::per_pod(Sharing::PerPod, "Volumes · one per pod")]
    #[case::neither(Sharing::Neither, "Volumes")]
    fn the_heading_follows_how_pods_share_claims(#[case] sharing: Sharing, #[case] heading: &str) {
        let mounts = Mounts {
            groups: vec![],
            sharing,
        };
        assert_eq!(mounts.heading(), heading);
    }

    #[rstest]
    #[case::two_types(Counts(vec![(Type::Nfs, 6), (Type::Iscsi, 2)]), "6 NFS · 2 iSCSI")]
    #[case::a_class(Counts(vec![(Type::Class("fast".into()), 3)]), "3 fast")]
    #[case::local(Counts(vec![(Type::Local, 1)]), "1 local")]
    #[case::unclassed(Counts(vec![(Type::Unclassed, 6)]), "6 volumes")]
    #[case::one_unclassed(Counts(vec![(Type::Unclassed, 1)]), "1 volume")]
    fn counts_list_each_type(#[case] counts: Counts, #[case] text: &str) {
        assert_eq!(counts.to_string(), text);
    }

    fn totals(counts: Counts, dependents: usize) -> Totals {
        Totals { counts, dependents }
    }

    fn ground(selected: Option<&str>, here: Option<Totals>) -> (Ground, GroundBox) {
        let one = GroundBox {
            name: "nas.example.com".into(),
            all: totals(Counts(vec![(Type::Nfs, 27), (Type::Iscsi, 71)]), 0),
            here,
            about: Vec::new(),
            page: String::new(),
            link: None,
        };
        let ground = Ground {
            boxes: vec![],
            selected: selected.map(Into::into),
        };
        (ground, one)
    }

    #[rstest]
    #[case::nothing_selected(None, None, Some("27 NFS · 71 iSCSI"))]
    #[case::the_selected_namespaces_counts(
        Some("shop"),
        Some(totals(Counts(vec![(Type::Nfs, 6), (Type::Iscsi, 2)]), 0)),
        Some("shop: 6 NFS · 2 iSCSI")
    )]
    #[case::the_selected_namespaces_dependents(
        Some("shop"),
        Some(totals(Counts::default(), 2)),
        Some("shop: 2 depend on it")
    )]
    #[case::volumes_and_a_dependent(
        Some("shop"),
        Some(totals(Counts(vec![(Type::Nfs, 6)]), 1)),
        Some("shop: 6 NFS · 1 depends on it")
    )]
    #[case::none_of_the_selected_namespace(Some("shop"), Some(Totals::default()), None)]
    fn a_box_counts_the_selected_namespace_or_the_cluster(
        #[case] selected: Option<&str>,
        #[case] here: Option<Totals>,
        #[case] text: Option<&str>,
    ) {
        let (ground, one) = ground(selected, here);
        assert_eq!(one.text(&ground).as_deref(), text);
    }

    fn line(text: &str) -> transect_resources::intent::Line {
        transect_resources::intent::Line {
            text: text.into(),
            provenance: Default::default(),
        }
    }

    #[rstest]
    #[case::no_lines(vec![], None)]
    #[case::joins_the_lines_with_spaces(
        vec![line("Holds the volumes."), line("Its updates restart iSCSI.")],
        Some("Holds the volumes. Its updates restart iSCSI.")
    )]
    fn a_box_title_joins_its_lines_of_intent(
        #[case] about: Vec<transect_resources::intent::Line>,
        #[case] title: Option<&str>,
    ) {
        let (_, mut one) = ground(None, None);
        one.about = about;
        assert_eq!(one.about_title().as_deref(), title);
    }

    #[rstest]
    #[case::volumes_only(vec![totals(Counts(vec![(Type::Nfs, 2)]), 0)], "2 volumes")]
    #[case::one_dependency(vec![totals(Counts(vec![(Type::Nfs, 2)]), 0), totals(Counts::default(), 4)], "2 volumes · 1 dependency")]
    #[case::two_dependencies(vec![totals(Counts::default(), 1), totals(Counts::default(), 1)], "0 volumes · 2 dependencies")]
    fn the_band_counts_volumes_and_dependencies(#[case] boxes: Vec<Totals>, #[case] text: &str) {
        let boxes = boxes.into_iter().map(|all| GroundBox {
            name: "a.example.com".into(),
            all,
            here: None,
            about: Vec::new(),
            page: String::new(),
            link: None,
        });
        let ground = Ground {
            boxes: boxes.collect(),
            selected: None,
        };
        assert_eq!(ground.total_text(), text);
    }

    #[rstest]
    #[case::none(0, "0 volumes")]
    #[case::one(1, "1 volume")]
    #[case::several(98, "98 volumes")]
    fn pluralizes_volumes(#[case] count: usize, #[case] text: &str) {
        assert_eq!(volumes(count), text);
    }
}
