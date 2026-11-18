//! Tests of the GitOps reader over the fixture's Flux objects:
//! `Kustomization` and `HelmRelease` objects, and git, OCI, Helm, and
//! bucket sources. One `GitRepository` is pinned to a commit, and one
//! follows a branch. One `Deployment` has the labels of both a
//! `Kustomization` and a `HelmRelease`.

use rstest::rstest;

use transect_graph::edges::{Endpoint, Object};
use transect_supply::gitops::*;

/// The fixture's GitOps objects, read with the Flux knowledge in
/// `gitops.yaml`.
fn gitops() -> GitOps {
    super::super::snapshot().gitops
}

const KUSTOMIZATION: (&str, &str) = ("kustomize.toolkit.fluxcd.io", "Kustomization");
const HELM_RELEASE: (&str, &str) = ("helm.toolkit.fluxcd.io", "HelmRelease");
const SOURCES: &str = "source.toolkit.fluxcd.io";

fn kustomization(name: &str) -> Object {
    Object::new(KUSTOMIZATION.0, KUSTOMIZATION.1, Some("acme-ops"), name)
}

fn helm_release() -> Object {
    Object::new(
        HELM_RELEASE.0,
        HELM_RELEASE.1,
        Some("acme-shop"),
        "storefront-release",
    )
}

fn source(kind: &str, name: &str) -> Object {
    Object::new(SOURCES, kind, Some("acme-ops"), name)
}

/// The detail of `object` as its page reads it: source, URL, directory,
/// chart, revision, and whether it applies.
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
#[case::a_kustomization_on_a_branch(
    kustomization("shop-apps"),
    shown(
        Some(source("GitRepository", "shop-git")),
        Some("https://github.com/acme/shop"),
        Some("./apps/shop"),
        None,
        Some("main@sha1:1111111111111111111111111111111111111111"),
        true
    )
)]
#[case::a_kustomization_at_the_root(
    kustomization("ops-apps"),
    shown(
        Some(source("OCIRepository", "ops-oci")),
        Some("oci://registry.example.test/acme/ops"),
        Some("./"),
        None,
        Some("v3@sha256:3333333333333333333333333333333333333333333333333333333333333333"),
        true
    )
)]
#[case::a_kustomization_from_a_bucket(
    kustomization("backup-apps"),
    shown(
        Some(source("Bucket", "backup-bucket")),
        Some("storage.example.test/acme-backups"),
        Some("./ledger"),
        None,
        Some("sha256:4444444444444444444444444444444444444444444444444444444444444444"),
        true
    )
)]
#[case::a_helm_release(
    helm_release(),
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
#[case::ready(kustomization("shop-apps"), Ready::True)]
#[case::failing(kustomization("backup-apps"), Ready::False("kustomize build failed: missing resource".into()))]
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
        (end(helm_release()), end(kustomization("shop-apps"))),
        (
            end(helm_release()),
            end(source("HelmRepository", "charts-helm")),
        ),
        (
            end(kustomization("shop-apps")),
            end(source("GitRepository", "shop-git")),
        ),
        (
            end(kustomization("ledger-apps")),
            end(source("GitRepository", "ledger-git")),
        ),
        (
            end(kustomization("ops-apps")),
            end(source("OCIRepository", "ops-oci")),
        ),
        (
            end(kustomization("backup-apps")),
            end(source("Bucket", "backup-bucket")),
        ),
    ];
    expected.sort_by_key(|pair| format!("{pair:?}"));
    assert_eq!(found, expected);
}

/// Each `Kustomization` is dated by its source's
/// `artifact.lastUpdateTime`, because it applied the revision that the
/// source last fetched.
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

/// The applier of each fixture workload, found from the labels that
/// Flux sets. The storefront has the labels of a `Kustomization` and of
/// a `HelmRelease`, and the `HelmRelease` is its applier.
#[rstest]
#[case::both_labels("storefront", Some(helm_release()))]
#[case::a_kustomizations_labels("log-shipper", Some(kustomization("ops-apps")))]
fn reads_each_workloads_applier(#[case] name: &str, #[case] expected: Option<Object>) {
    let snapshot = super::super::snapshot();
    let metas = snapshot.deployments.iter().map(|d| &d.metadata);
    let metas = metas.chain(snapshot.daemon_sets.iter().map(|d| &d.metadata));
    let meta = metas
        .into_iter()
        .find(|meta| meta.name.as_deref() == Some(name));
    assert_eq!(gitops().appliers().of(meta.unwrap()), expected);
}
