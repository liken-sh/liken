use rstest::rstest;

use super::*;
use transect_supply::gitops::Ready;
use transect_supply::gitops::fixtures::object;

fn tree() -> Detail {
    Detail {
        object: object("Tree", "ops", "apps"),
        ready: Ready::True,
        revision: Some("main@sha1:0a1b2c3".into()),
        source: Some(object("Repo", "ops", "fleet")),
        url: Some("https://forge.example.com/ops/fleet".into()),
        path: Some("./apps".into()),
        chart: None,
        applies: true,
    }
}

fn deployment(namespace: &str, name: &str) -> Object {
    Object::new("apps", "Deployment", Some(namespace), name)
}

/// Every object except a ConfigMap has a page.
fn href(object: &Object) -> Option<String> {
    (object.kind != "ConfigMap").then(|| format!("/{}", object.name))
}

fn group(namespace: &str, kind: &str, members: &[(&str, Option<&str>)]) -> Group {
    Group {
        namespace: Some(namespace.into()),
        kind: kind.into(),
        members: members
            .iter()
            .map(|(text, href)| Member {
                text: (*text).into(),
                href: href.map(Into::into),
            })
            .collect(),
    }
}

#[rstest]
fn groups_what_it_applies_by_namespace_and_kind() {
    let applied = [
        deployment("shop", "web"),
        deployment("blog", "web"),
        Object::new("", "ConfigMap", Some("shop"), "settings"),
        deployment("shop", "api"),
    ];
    let page = GitOpsPage::build(tree(), None, &applied, href, "/x".into());
    assert_eq!(
        page.reconciling,
        vec![
            group("blog", "Deployment", &[("web", Some("/web"))]),
            group("shop", "ConfigMap", &[("settings", None)]),
            group(
                "shop",
                "Deployment",
                &[("api", Some("/api")), ("web", Some("/web"))]
            ),
        ]
    );
}

#[rstest]
#[case::in_its_own_namespace("ops", "Repo fleet")]
#[case::in_another("sources", "Repo sources/fleet")]
fn names_its_source(#[case] namespace: &str, #[case] text: &str) {
    let mut detail = tree();
    detail.source.as_mut().unwrap().namespace = Some(namespace.into());
    let page = GitOpsPage::build(detail, None, [], href, "/x".into());
    assert_eq!(page.source.map(|source| source.text).as_deref(), Some(text));
}

#[rstest]
#[case::ready(Ready::True, ("ready", None))]
#[case::failing(Ready::False("build failed".into()), ("not ready", Some("build failed")))]
#[case::reconciling(Ready::Unknown("in progress".into()), ("reconciling", Some("in progress")))]
#[case::never_reconciled(Ready::Missing, ("not reconciled yet", None))]
fn says_whether_its_tool_reports_it_ready(
    #[case] ready: Ready,
    #[case] expected: (&str, Option<&str>),
) {
    let mut detail = tree();
    detail.ready = ready;
    let page = GitOpsPage::build(detail, None, [], href, "/x".into());
    assert_eq!((page.ready_text(), page.message()), expected);
}

#[rstest]
fn a_source_lists_what_reconciles_from_it() {
    let mut detail = tree();
    detail.object = object("Repo", "ops", "fleet");
    detail.applies = false;
    let page = GitOpsPage::build(detail, None, [], href, "/x".into());
    assert_eq!(
        (
            page.reconciling_heading(),
            page.revision_label(),
            page.nothing_text()
        ),
        (
            "Reconciles from it",
            "Fetched",
            "Nothing transect reads reconciles from it."
        )
    );
}

#[rstest]
fn lists_the_objects_of_its_kind_in_its_namespace() {
    let objects = [
        object("Tree", "ops", "infra"),
        object("Tree", "ops", "apps"),
        object("Tree", "other", "shop"),
        object("Repo", "ops", "fleet"),
    ];
    let page = GitOpsPage::build(tree(), None, [], href, "/x".into()).with_siblings(&objects, href);
    let found: Vec<(&str, bool)> = page
        .siblings
        .iter()
        .map(|sibling| (sibling.name.as_str(), sibling.current))
        .collect();
    assert_eq!(found, vec![("apps", true), ("infra", false)]);
}
