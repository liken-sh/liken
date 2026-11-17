use rstest::rstest;

use super::*;
use crate::map::section::layout::{At, Column, Side};

fn plain(title: &str) -> SectionBox {
    SectionBox::new(title.into(), vec!["a line".into()], Look::Plain)
}

fn host(name: &str, pods: usize) -> Host {
    Host {
        name: name.into(),
        pods,
        control_plane: false,
    }
}

fn hosts(count: usize) -> Vec<Host> {
    (1..=count)
        .map(|i| host(&format!("machine-{i}"), 1))
        .collect()
}

fn held(name: &str, size: &str, kind: Type) -> Held {
    Held {
        name: name.into(),
        href: format!("/namespaces/shop/persistentvolumeclaims/{name}"),
        size: Some(size.into()),
        kind: Some(kind),
    }
}

fn nfs(claims: &[&str]) -> Backing {
    Backing {
        backend: Some(Backend::Address("nas.example.com".into())),
        claims: claims.iter().map(|c| held(c, "8Gi", Type::Nfs)).collect(),
        inline: Vec::new(),
        machines: BTreeSet::new(),
    }
}

fn local(machine: &str) -> Backing {
    Backing {
        backend: Some(Backend::Machine(Some(machine.into()))),
        claims: vec![held("cache", "1Gi", Type::Local)],
        inline: Vec::new(),
        machines: BTreeSet::new(),
    }
}

fn parts(ingresses: usize, services: usize, backings: Vec<Backing>, hosts: Vec<Host>) -> Parts {
    Parts {
        layer: "apps".into(),
        ingresses: (0..ingresses).map(|_| plain("shop.example.com")).collect(),
        services: (0..services).map(|_| plain("Service web")).collect(),
        routes: (0..ingresses)
            .map(|i| (i, 0))
            .filter(|_| services > 0)
            .collect(),
        workload: Some(plain("Deployment web")),
        backings,
        hosts,
        ..Parts::default()
    }
}

/// Each row's band and how many boxes each of its tiers holds.
fn outline(rows: &[Row]) -> Vec<(Band, Vec<usize>)> {
    let counts = |row: &Row| row.tiers.iter().map(Vec::len).collect();
    rows.iter()
        .map(|row| (row.band.clone(), counts(row)))
        .collect()
}

fn layer() -> Band {
    Band::Layer("apps".into())
}

#[rstest]
#[case::a_web_application(
    parts(1, 1, vec![nfs(&["media"])], hosts(1)),
    vec![(Band::Entry, vec![1, 1]), (layer(), vec![1]), (Band::Volumes, vec![1]), (Band::Machines, vec![1]), (Band::Ground, vec![1])]
)]
#[case::no_ingress(
    parts(0, 1, vec![], hosts(1)),
    vec![(Band::Entry, vec![1]), (layer(), vec![1]), (Band::Machines, vec![1])]
)]
#[case::no_entry(
    parts(0, 0, vec![], hosts(1)),
    vec![(layer(), vec![1]), (Band::Machines, vec![1])]
)]
#[case::three_machines(
    parts(0, 0, vec![], hosts(3)),
    vec![(layer(), vec![1]), (Band::Machines, vec![3])]
)]
#[case::a_daemon_set_on_six(
    parts(0, 0, vec![], hosts(6)),
    vec![(layer(), vec![1]), (Band::Machines, vec![1])]
)]
#[case::claims_on_two_backends(
    parts(0, 0, vec![nfs(&["media", "backups"]), local("alpha")], hosts(1)),
    vec![(layer(), vec![1]), (Band::Volumes, vec![2]), (Band::Machines, vec![1]), (Band::Ground, vec![1])]
)]
#[case::no_pods(
    parts(0, 1, vec![nfs(&["media"])], vec![]),
    vec![(Band::Entry, vec![1]), (layer(), vec![1]), (Band::Volumes, vec![1]), (Band::Ground, vec![1])]
)]
fn lays_the_spine_in_the_maps_bands(
    #[case] parts: Parts,
    #[case] expected: Vec<(Band, Vec<usize>)>,
) {
    assert_eq!(outline(&rows(&parts)), expected);
}

/// The title and lines of every box in the row of `band`.
fn texts(rows: &[Row], band: Band) -> Vec<(String, Vec<String>)> {
    let row = rows.iter().find(|row| row.band == band).unwrap();
    let boxes = row.tiers.iter().flatten();
    boxes.map(|b| (b.title.clone(), b.lines.clone())).collect()
}

fn text(title: &str, lines: &[&str]) -> (String, Vec<String>) {
    (title.into(), lines.iter().map(|l| l.to_string()).collect())
}

#[rstest]
#[case::the_only_pod(vec![host("alpha", 1)], vec![text("alpha", &["runs the pod"])])]
#[case::two_machines(
    vec![host("alpha", 2), host("beta", 1)],
    vec![text("alpha", &["runs 2 pods"]), text("beta", &["runs 1 pod"])]
)]
#[case::past_the_most(hosts(6), vec![text("on 6 machines", &["runs 6 pods"])])]
fn names_the_machines(#[case] hosts: Vec<Host>, #[case] expected: Vec<(String, Vec<String>)>) {
    let rows = rows(&parts(0, 0, vec![], hosts));
    assert_eq!(texts(&rows, Band::Machines), expected);
}

#[rstest]
fn counts_several_claims_and_names_one_claim() {
    let rows = rows(&parts(
        0,
        0,
        vec![nfs(&["media", "backups"]), local("alpha")],
        hosts(1),
    ));
    assert_eq!(
        texts(&rows, Band::Volumes),
        vec![
            text(
                "2 claims",
                &["nas.example.com · NFS · 16Gi", "media, backups"]
            ),
            text("cache", &["alpha · local · 1Gi"]),
        ]
    );
    assert_eq!(
        texts(&rows, Band::Ground),
        vec![text("nas.example.com", &["NFS: 2 claims · 16Gi"])]
    );
}

#[rstest]
fn shapes_count_the_title_as_a_line() {
    let rows = rows(&parts(1, 2, vec![], hosts(1)));
    let tiers: Vec<Vec<Vec<usize>>> = shapes(&rows, &[]).into_iter().map(|s| s.tiers).collect();
    assert_eq!(
        tiers,
        vec![vec![vec![2], vec![2, 2]], vec![vec![2]], vec![vec![2]]]
    );
}

fn mounted(backing: Backing, machines: &[&str]) -> Backing {
    Backing {
        machines: machines.iter().map(|m| m.to_string()).collect(),
        ..backing
    }
}

/// Each link as the bands and places of its ends, and its label.
fn wiring(parts: &Parts) -> Vec<(String, String, String)> {
    let rows = rows(parts);
    let end = |at: At| {
        let column = match at.column {
            Column::Tier(tier) => tier.to_string(),
            Column::Side(Side::Left) => "left".into(),
            Column::Side(Side::Right) => "right".into(),
        };
        format!("{} {column}.{}", rows[at.row].band.label(), at.index)
    };
    let links = links(parts, &rows);
    links
        .into_iter()
        .map(|l| (end(l.from), end(l.to), l.label))
        .collect()
}

fn wire(from: &str, to: &str, label: &str) -> (String, String, String) {
    (from.into(), to.into(), label.into())
}

#[rstest]
#[case::one_straight_spine(
    parts(1, 1, vec![mounted(nfs(&["media"]), &["machine-1"])], hosts(1)),
    vec![
        wire("Entry 0.0", "Entry 1.0", "routes to"),
        wire("Entry 1.0", "apps 0.0", "selects"),
        wire("apps 0.0", "Volumes 0.0", "stores on"),
        wire("Volumes 0.0", "Machines 0.0", "mounted on"),
        wire("Machines 0.0", "Ground 0.0", "NFS"),
    ]
)]
#[case::a_fan_to_three_machines_and_back(
    parts(0, 0, vec![mounted(nfs(&["media"]), &["machine-1", "machine-2", "machine-3"])], hosts(3)),
    vec![
        wire("apps 0.0", "Volumes 0.0", "stores on"),
        wire("Volumes 0.0", "Machines 0.0", "mounted on"),
        wire("Volumes 0.0", "Machines 0.1", "mounted on"),
        wire("Volumes 0.0", "Machines 0.2", "mounted on"),
        wire("Machines 0.0", "Ground 0.0", "NFS"),
        wire("Machines 0.1", "Ground 0.0", "NFS"),
        wire("Machines 0.2", "Ground 0.0", "NFS"),
    ]
)]
#[case::folded_machines(
    parts(0, 0, vec![mounted(nfs(&["media"]), &["machine-1", "machine-6"])], hosts(6)),
    vec![
        wire("apps 0.0", "Volumes 0.0", "stores on"),
        wire("Volumes 0.0", "Machines 0.0", "mounted on"),
        wire("Machines 0.0", "Ground 0.0", "NFS"),
    ]
)]
#[case::a_local_volume_ends_on_its_machine(
    parts(0, 0, vec![mounted(local("machine-1"), &["machine-1"])], hosts(1)),
    vec![
        wire("apps 0.0", "Volumes 0.0", "stores on"),
        wire("Volumes 0.0", "Machines 0.0", "mounted on"),
    ]
)]
#[case::no_pod_leads_claims_to_the_ground(
    parts(0, 2, vec![nfs(&["media"])], vec![]),
    vec![
        wire("Entry 0.0", "apps 0.0", "selects"),
        wire("Entry 0.1", "apps 0.0", "selects"),
        wire("apps 0.0", "Volumes 0.0", "stores on"),
        wire("Volumes 0.0", "Ground 0.0", "NFS"),
    ]
)]
fn wires_the_spine(#[case] parts: Parts, #[case] expected: Vec<(String, String, String)>) {
    assert_eq!(wiring(&parts), expected);
}

mod drawn;

#[rstest]
fn a_title_breaks_after_each_dot() {
    let ground = SectionBox::new("nas.home.example.com".into(), vec![], Look::Ground);
    assert_eq!(
        ground.title_parts(),
        vec!["nas.", "home.", "example.", "com"]
    );
}
