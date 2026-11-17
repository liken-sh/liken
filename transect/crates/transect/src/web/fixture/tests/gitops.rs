//! The reader over the fixture cluster's GitOps objects, which hold
//! every kind its tool has: two appliers that applied one object, git,
//! OCI, Helm, and bucket sources, a commit pin, and a branch revision.

use rstest::rstest;

use transect_graph::edges::{Endpoint, Object};
use transect_supply::gitops::*;

/// The fixture cluster's GitOps objects, read through the knowledge
/// its tool ships.
fn gitops() -> GitOps {
    super::super::snapshot().gitops
}

const TREE: (&str, &str) = ("kustomize.toolkit.fluxcd.io", "Kustomization");
const RELEASE: (&str, &str) = ("helm.toolkit.fluxcd.io", "HelmRelease");
const SOURCES: &str = "source.toolkit.fluxcd.io";

fn tree(name: &str) -> Object {
    Object::new(TREE.0, TREE.1, Some("acme-ops"), name)
}

fn release() -> Object {
    Object::new(
        RELEASE.0,
        RELEASE.1,
        Some("acme-shop"),
        "storefront-release",
    )
}

fn source(kind: &str, name: &str) -> Object {
    Object::new(SOURCES, kind, Some("acme-ops"), name)
}

/// `object`'s detail as its page reads it: its source, its URL, its
/// directory, its chart, its revision, and whether it applies.
type Shown = (
    Option<Object>,
    Option<String>,
    Option<String>,
    Option<String>,
    Option<String>,
    bool,
);

fn shown(
    source: Option<Object>,
    url: Option<&str>,
    path: Option<&str>,
    chart: Option<&str>,
    revision: Option<&str>,
    applies: bool,
) -> Shown {
    let own = |text: Option<&str>| text.map(String::from);
    (
        source,
        own(url),
        own(path),
        own(chart),
        own(revision),
        applies,
    )
}

#[rstest]
#[case::a_tree_on_a_branch(
    tree("shop-apps"),
    shown(
        Some(source("GitRepository", "shop-git")),
        Some("https://github.com/acme/shop"),
        Some("./apps/shop"),
        None,
        Some("main@sha1:1111111111111111111111111111111111111111"),
        true
    )
)]
#[case::a_tree_at_the_root(
    tree("ops-apps"),
    shown(
        Some(source("OCIRepository", "ops-oci")),
        Some("oci://registry.example.test/acme/ops"),
        Some("./"),
        None,
        Some("v3@sha256:3333333333333333333333333333333333333333333333333333333333333333"),
        true
    )
)]
#[case::a_tree_from_a_bucket(
    tree("backup-apps"),
    shown(
        Some(source("Bucket", "backup-bucket")),
        Some("storage.example.test/acme-backups"),
        Some("./ledger"),
        None,
        Some("sha256:4444444444444444444444444444444444444444444444444444444444444444"),
        true
    )
)]
#[case::a_release(
    release(),
    shown(
        Some(source("HelmRepository", "charts-helm")),
        Some("https://charts.example.test"),
        None,
        Some("storefront-chart"),
        Some("1.4.2"),
        true
    )
)]
#[case::a_pinned_source(
    source("GitRepository", "ledger-git"),
    shown(
        None,
        Some("https://codeberg.org/acme/ledger"),
        None,
        None,
        Some("sha1:2222222222222222222222222222222222222222"),
        false
    )
)]
#[case::a_source_with_no_artifact(
    source("HelmRepository", "charts-helm"),
    shown(None, Some("https://charts.example.test"), None, None, None, false)
)]
fn reads_what_each_objects_page_shows(#[case] object: Object, #[case] expected: Shown) {
    let detail = gitops().detail(&object).unwrap();
    let found = (
        detail.source,
        detail.url,
        detail.path,
        detail.chart,
        detail.revision,
        detail.applies,
    );
    assert_eq!(found, expected);
}

#[rstest]
#[case::ready(tree("shop-apps"), Ready::True)]
#[case::failing(tree("backup-apps"), Ready::False("kustomize build failed: missing resource".into()))]
fn reads_whether_each_is_ready(#[case] object: Object, #[case] expected: Ready) {
    assert_eq!(gitops().detail(&object).unwrap().ready, expected);
}

#[rstest]
fn reads_the_ref_each_source_follows() {
    let gitops = gitops();
    let reference = |object: Object| gitops.found(&object).unwrap().reference.clone();
    assert_eq!(
        (
            reference(source("GitRepository", "shop-git")),
            reference(source("GitRepository", "ledger-git")),
            reference(source("OCIRepository", "ops-oci")),
        ),
        (
            GitRef {
                branch: Some("main".into()),
                ..GitRef::default()
            },
            GitRef {
                commit: Some("2222222222222222222222222222222222222222".into()),
                ..GitRef::default()
            },
            GitRef {
                tag: Some("v3".into()),
                ..GitRef::default()
            },
        )
    );
}

#[rstest]
fn draws_an_edge_from_each_applier_to_its_source_and_to_what_applied_it() {
    let mut found: Vec<(Endpoint, Endpoint)> = gitops()
        .edges(&Applied::default())
        .into_iter()
        .map(|edge| (edge.from, edge.to))
        .collect();
    found.sort_by_key(|pair| format!("{pair:?}"));
    let end = Endpoint::Object;
    let mut expected = vec![
        (end(release()), end(tree("shop-apps"))),
        (end(release()), end(source("HelmRepository", "charts-helm"))),
        (
            end(tree("shop-apps")),
            end(source("GitRepository", "shop-git")),
        ),
        (
            end(tree("ledger-apps")),
            end(source("GitRepository", "ledger-git")),
        ),
        (
            end(tree("ops-apps")),
            end(source("OCIRepository", "ops-oci")),
        ),
        (
            end(tree("backup-apps")),
            end(source("Bucket", "backup-bucket")),
        ),
    ];
    expected.sort_by_key(|pair| format!("{pair:?}"));
    assert_eq!(found, expected);
}

/// Each tree applied its source's revision, so each is dated by the
/// source's fetch.
#[rstest]
fn dates_each_revision_by_its_sources_fetch() {
    let found: Vec<(String, String, Option<String>, String)> = gitops()
        .revisions()
        .into_iter()
        .map(|r| {
            let (reference, commit) = r.parts();
            let parts = format!("{reference} {}", commit.unwrap_or("-"));
            (
                r.applier.name.clone(),
                parts,
                r.url.clone(),
                r.at.to_string(),
            )
        })
        .collect();
    let row = |name: &str, parts: &str, url: &str, at: &str| {
        (
            name.to_string(),
            parts.to_string(),
            Some(url.to_string()),
            at.to_string(),
        )
    };
    assert_eq!(
        found,
        vec![
            row(
                "backup-apps",
                "sha256:4444444444444444444444444444444444444444444444444444444444444444 -",
                "storage.example.test/acme-backups",
                "2026-01-01T07:00:00Z"
            ),
            row(
                "ledger-apps",
                " 2222222222222222222222222222222222222222",
                "https://codeberg.org/acme/ledger",
                "2026-01-01T10:00:00Z"
            ),
            row(
                "ops-apps",
                "v3 -",
                "oci://registry.example.test/acme/ops",
                "2026-01-01T09:00:00Z"
            ),
            row(
                "shop-apps",
                "main 1111111111111111111111111111111111111111",
                "https://github.com/acme/shop",
                "2026-01-01T11:00:00Z"
            ),
        ]
    );
}

#[rstest]
fn lists_each_source_with_its_url() {
    let gitops = gitops();
    let mut found: Vec<(&str, &str)> = gitops
        .sources()
        .map(|(object, url)| (object.name.as_str(), url))
        .collect();
    found.sort();
    assert_eq!(
        found,
        vec![
            ("backup-bucket", "storage.example.test/acme-backups"),
            ("charts-helm", "https://charts.example.test"),
            ("ledger-git", "https://codeberg.org/acme/ledger"),
            ("ops-oci", "oci://registry.example.test/acme/ops"),
            ("shop-git", "https://github.com/acme/shop"),
        ]
    );
}

/// Each fixture workload's applier, by its marks.  The storefront has
/// a tree's marks and a release's, and the release's marks win.
#[rstest]
#[case::both_marks("storefront", Some(release()))]
#[case::a_trees_marks("log-shipper", Some(tree("ops-apps")))]
fn reads_each_workloads_applier(#[case] name: &str, #[case] expected: Option<Object>) {
    let snapshot = super::super::snapshot();
    let metas = snapshot.deployments.iter().map(|d| &d.metadata);
    let metas = metas.chain(snapshot.daemon_sets.iter().map(|d| &d.metadata));
    let meta = metas
        .into_iter()
        .find(|meta| meta.name.as_deref() == Some(name));
    assert_eq!(gitops().appliers().of(meta.unwrap()), expected);
}
